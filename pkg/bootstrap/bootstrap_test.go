package bootstrap

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/fgrosse/zaptest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/reddit/achilles-sdk/pkg/internal/tests"
	testv1alpha1 "github.com/reddit/achilles-sdk/pkg/internal/tests/api/test/v1alpha1"
	"github.com/reddit/achilles-sdk/pkg/logging"
	"github.com/reddit/achilles-sdk/pkg/shard"
	"github.com/reddit/achilles-sdk/pkg/test"
)

var _ = DescribeTable("buildRestConfig should fail",
	func(inCluster bool, kubeContext string, want string) {
		opts := &Options{
			InCluster:   inCluster,
			KubeContext: kubeContext,
		}
		_, err := buildRestConfig(opts)
		Expect(err).Should(MatchError(want))
	},
	Entry("implicitly", false, "", errNoValidKubeContext),
	Entry("when both inCluster and context are set",
		true, "foo", errKubeContextSetInCluster),
	Entry("nonexistent context", false, "foo", "context \"foo\" does not exist"),
)

var _ = Describe("cacheOptions", func() {
	It("defaults SyncPeriod from the flag-provided value", func() {
		opts := &Options{SyncPeriod: 5 * time.Hour}

		Expect(cacheOptions(opts).SyncPeriod).To(HaveValue(Equal(5 * time.Hour)))
	})

	It("prefers a programmatically set Cache.SyncPeriod over the flag", func() {
		syncPeriod := 2 * time.Hour
		opts := &Options{
			SyncPeriod: 5 * time.Hour,
			Cache:      cache.Options{SyncPeriod: &syncPeriod},
		}

		Expect(cacheOptions(opts).SyncPeriod).To(HaveValue(Equal(2 * time.Hour)))
	})

	It("passes through the other cache options", func() {
		selector := labels.SelectorFromSet(labels.Set{"app": "foo"})
		opts := &Options{
			SyncPeriod: 5 * time.Hour,
			Cache: cache.Options{
				ByObject: map[client.Object]cache.ByObject{
					&corev1.Pod{}: {Label: selector},
				},
			},
		}

		cacheOpts := cacheOptions(opts)
		Expect(cacheOpts.ByObject).To(HaveKeyWithValue(&corev1.Pod{}, cache.ByObject{Label: selector}))
		Expect(cacheOpts.SyncPeriod).To(HaveValue(Equal(5 * time.Hour)))
	})

})
var _ = Describe("buildManager", func() {
	DescribeTable("preserves built-in Kubernetes types",
		func(gctx context.Context, schemes runtime.SchemeBuilder) {
			log := zaptest.LoggerWriter(GinkgoWriter).Sugar()
			ctx := logging.NewContext(gctx, log)
			testEnv, err := test.NewEnvTestBuilder(ctx).
				WithLog(log.Desugar()).
				Start()
			Expect(err).NotTo(HaveOccurred())
			defer func() { Expect(testEnv.Stop()).To(Succeed()) }()

			mgr, err := buildManager(testEnv.Cfg, log, schemes, &Options{}, nil)
			Expect(err).NotTo(HaveOccurred())

			// These reads must reach the API server, rather than fail locally
			// because the manager's scheme cannot resolve built-in Go types.
			Expect(mgr.GetAPIReader().Get(ctx, client.ObjectKey{Name: "default"}, &corev1.Namespace{})).To(Succeed())
			err = mgr.GetAPIReader().Get(ctx, client.ObjectKey{Namespace: "default", Name: "missing"}, &appsv1.Deployment{})
			Expect(apierrors.IsNotFound(err)).To(BeTrue(), "expected an API NotFound response, got: %v", err)
		},
		Entry("without a caller scheme", nil),
		Entry("with only custom types in the caller scheme", runtime.SchemeBuilder{testv1alpha1.AddToScheme}),
	)

	It("allows ByObject configuration of custom types", func(gctx context.Context) {
		log := zaptest.LoggerWriter(GinkgoWriter).Sugar()
		ctx := logging.NewContext(gctx, log)

		testEnv, err := test.NewEnvTestBuilder(ctx).
			WithCRDDirectoryPaths([]string{
				filepath.Join(tests.RootDir(), "pkg", "internal", "tests", "cluster", "crd", "bases"),
			}).
			WithLog(log.Desugar()).
			Start()
		Expect(err).ToNot(HaveOccurred())
		defer func() { Expect(testEnv.Stop()).To(Succeed()) }()

		schemes := runtime.SchemeBuilder{}
		schemes.Register(testv1alpha1.AddToScheme)
		opts := &Options{
			Cache: cache.Options{
				ByObject: map[client.Object]cache.ByObject{
					&testv1alpha1.TestClaim{}: {},
				},
			},
		}

		_, err = buildManager(testEnv.Cfg, log, schemes, opts, nil)
		Expect(err).NotTo(HaveOccurred())
	})

	// Exercises the whole mechanism against a real API server, whose optimistic concurrency is what
	// makes lease acquisition mutually exclusive.
	It("divides the objects between instances and ignores unassigned values", func(gctx context.Context) {
		log := zaptest.LoggerWriter(GinkgoWriter).Sugar()
		ctx := logging.NewContext(gctx, log)

		testEnv, err := test.NewEnvTestBuilder(ctx).
			WithCRDDirectoryPaths([]string{
				filepath.Join(tests.RootDir(), "pkg", "internal", "tests", "cluster", "crd", "bases"),
			}).
			WithScheme(testScheme()).
			WithLog(log.Desugar()).
			Start()
		Expect(err).ToNot(HaveOccurred())
		defer func() { Expect(testEnv.Stop()).To(Succeed()) }()

		for name, labelSet := range map[string]map[string]string{
			"in-a":      {shard.DefaultKey: "a"},
			"in-b":      {shard.DefaultKey: "b"},
			"in-c":      {shard.DefaultKey: "c"},
			"unlabeled": nil,
		} {
			Expect(testEnv.Client.Create(ctx, &testv1alpha1.TestClaim{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", Labels: labelSet},
			})).To(Succeed())
		}

		opts := &Options{Shard: ShardOptions{Types: []client.Object{&testv1alpha1.TestClaim{}}}}
		mgr, err := buildManager(testEnv.Cfg, log, runtime.SchemeBuilder{testv1alpha1.AddToScheme}, opts, nil)
		Expect(err).NotTo(HaveOccurred())

		mgrCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		go func() {
			defer GinkgoRecover()
			Expect(mgr.GetCache().Start(mgrCtx)).To(Succeed())
		}()
		Expect(mgr.GetCache().WaitForCacheSync(mgrCtx)).To(BeTrue())

		gvks, err := shardedGVKs(mgr.GetScheme(), opts.Shard.Types)
		Expect(err).NotTo(HaveOccurred())
		group, err := shard.GroupFor(gvks)
		Expect(err).NotTo(HaveOccurred())
		Expect(group.Name).To(Equal("testclaim"), "the group is named after what it partitions")

		list := inventoryFunc(mgr, shard.DefaultKey, gvks)
		Expect(list(mgrCtx)).To(Equal(shard.Inventory{
			Values:    sets.New("a", "b", "c"),
			Unlabeled: true,
		}))

		newOwner := func(instance, identity string, values ...string) *shard.Owner {
			return shard.NewOwner(testEnv.Client, shard.OwnerConfig{
				Shard:     mustParseShard(values...),
				Group:     group,
				Instance:  instance,
				Namespace: "default",
				Identity:  identity,
				List:      list,
				Log:       log,
			})
		}

		// Value "c" is in use but assigned to neither instance, and "a" is assigned to both.
		instA := newOwner("ctl-a", "pod-1", "a", shard.UnlabeledValue)
		instB := newOwner("ctl-b", "pod-2", "a", "b")

		Expect(instA.Sync(mgrCtx)).To(Succeed())
		Expect(instB.Sync(mgrCtx)).To(Succeed())

		claim := func(labelSet map[string]string) client.Object {
			return &testv1alpha1.TestClaim{ObjectMeta: metav1.ObjectMeta{Name: "claim", Labels: labelSet}}
		}
		inA := claim(map[string]string{shard.DefaultKey: "a"})
		inB := claim(map[string]string{shard.DefaultKey: "b"})
		inC := claim(map[string]string{shard.DefaultKey: "c"})

		Expect(instA.Ownership(claim(nil))).To(Equal(shard.Owned), "the unlabeled partition is claimed explicitly")
		Expect(instB.Ownership(inB)).To(Equal(shard.Owned))

		// Configured with the same value, so exactly one holds it and the other defers rather than
		// duplicating the work.
		Expect(instA.Ownership(inA)).To(Equal(shard.Owned))
		Expect(instB.Ownership(inA)).To(Equal(shard.Pending))

		// Assignment is an operator decision, so an unassigned value is reconciled by nobody
		// rather than falling to whichever instance would otherwise absorb it.
		Expect(instA.Ownership(inC)).To(Equal(shard.NotManaged))
		Expect(instB.Ownership(inC)).To(Equal(shard.NotManaged))
	})
})

func mustParseShard(values ...string) *shard.Shard {
	s, err := shard.Parse(shard.DefaultKey, values)
	Expect(err).NotTo(HaveOccurred())
	return s
}

func testScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
	Expect(testv1alpha1.AddToScheme(scheme)).To(Succeed())
	return scheme
}

func TestBootstrap(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Bootstrap")
}

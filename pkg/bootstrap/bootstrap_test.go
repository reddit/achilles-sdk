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

	It("restricts a sharded type's informer to the shard's slice", func(gctx context.Context) {
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

		for name, shardValue := range map[string]string{"mine": "a", "theirs": "b"} {
			Expect(testEnv.Client.Create(ctx, &testv1alpha1.TestClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: "default",
					Labels:    map[string]string{shard.DefaultKey: shardValue},
				},
			})).To(Succeed())
		}

		schemes := runtime.SchemeBuilder{testv1alpha1.AddToScheme}
		mgr, err := buildManager(testEnv.Cfg, log, schemes, &Options{
			ShardedTypes: []client.Object{&testv1alpha1.TestClaim{}},
		}, mustParseShard("shard.infrared.reddit.com/key=a"))
		Expect(err).NotTo(HaveOccurred())

		mgrCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		go func() {
			defer GinkgoRecover()
			Expect(mgr.GetCache().Start(mgrCtx)).To(Succeed())
		}()
		Expect(mgr.GetCache().WaitForCacheSync(mgrCtx)).To(BeTrue())

		// The cached client, unlike the API reader, sees only what the informer holds.
		var claims testv1alpha1.TestClaimList
		Expect(mgr.GetClient().List(mgrCtx, &claims)).To(Succeed())

		names := []string{}
		for _, c := range claims.Items {
			names = append(names, c.Name)
		}
		Expect(names).To(ConsistOf("mine"), "the other shard's objects must not reach this instance")
	})
})

func mustParseShard(raw string) *shard.Shard {
	s, err := shard.Parse(shard.DefaultKey, raw)
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

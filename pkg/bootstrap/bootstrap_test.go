package bootstrap

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/fgrosse/zaptest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/reddit/achilles-sdk/pkg/internal/tests"
	testv1alpha1 "github.com/reddit/achilles-sdk/pkg/internal/tests/api/test/v1alpha1"
	"github.com/reddit/achilles-sdk/pkg/logging"
	"github.com/reddit/achilles-sdk/pkg/test"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

		_, err = buildManager(testEnv.Cfg, log, schemes, opts)
		Expect(err).NotTo(HaveOccurred())
	})
})

func TestBootstrap(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Bootstrap")
}

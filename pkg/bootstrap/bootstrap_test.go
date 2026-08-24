package bootstrap

import (
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
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

func TestBootstrap(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Bootstrap")
}

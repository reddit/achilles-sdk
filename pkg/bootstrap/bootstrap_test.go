package bootstrap

import (
	"context"
	"net/url"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	clientmetrics "k8s.io/client-go/tools/metrics"
)

type recordingLatencyMetric struct {
	verb     string
	host     string
	duration time.Duration
	calls    int
}

func (m *recordingLatencyMetric) Observe(_ context.Context, verb string, u url.URL, duration time.Duration) {
	m.verb = verb
	m.host = u.Host
	m.duration = duration
	m.calls++
}

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

var _ = Describe("registerClientRateLimiterMetrics", func() {
	It("records rate limiter latency and preserves the existing observer", func() {
		registry := prometheus.NewRegistry()
		previous := &recordingLatencyMetric{}
		original := clientmetrics.RateLimiterLatency
		clientmetrics.RateLimiterLatency = previous
		DeferCleanup(func() {
			clientmetrics.RateLimiterLatency = original
		})

		Expect(registerClientRateLimiterMetrics(registry)).To(Succeed())
		// Re-registering must not install a second observer that would count
		// every request twice.
		Expect(registerClientRateLimiterMetrics(registry)).To(Succeed())

		clientmetrics.RateLimiterLatency.Observe(
			context.Background(),
			"GET",
			url.URL{Host: "kubernetes.default.svc"},
			250*time.Millisecond,
		)

		Expect(previous.calls).To(Equal(1))
		Expect(previous.verb).To(Equal("GET"))
		Expect(previous.host).To(Equal("kubernetes.default.svc"))
		Expect(previous.duration).To(Equal(250 * time.Millisecond))

		metricFamilies, err := registry.Gather()
		Expect(err).NotTo(HaveOccurred())
		Expect(metricFamilies).To(HaveLen(1))
		Expect(metricFamilies[0].GetName()).To(Equal(restClientRateLimiterDurationMetricName))
		Expect(metricFamilies[0].Metric).To(HaveLen(1))

		metric := metricFamilies[0].Metric[0]
		Expect(metric.GetHistogram().GetSampleCount()).To(Equal(uint64(1)))
		Expect(metric.GetHistogram().GetSampleSum()).To(BeNumerically("~", 0.25))
		upperBounds := make([]float64, 0, len(metric.GetHistogram().Bucket))
		for _, bucket := range metric.GetHistogram().Bucket {
			upperBounds = append(upperBounds, bucket.GetUpperBound())
		}
		Expect(upperBounds).To(Equal([]float64{
			0.005, 0.025, 0.1, 0.25, 0.5, 1, 2, 4, 8, 15, 30, 60,
		}))

		labels := map[string]string{}
		for _, label := range metric.Label {
			labels[label.GetName()] = label.GetValue()
		}
		Expect(labels).To(Equal(map[string]string{
			"host": "kubernetes.default.svc",
			"verb": "GET",
		}))
	})
})

func TestBootstrap(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Bootstrap")
}

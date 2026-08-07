package metrics

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/runtime"
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

func TestMustMakeMetricsRegistersClientRateLimiterMetrics(t *testing.T) {
	registry := prometheus.NewRegistry()
	previous := &recordingLatencyMetric{}
	original := clientmetrics.RateLimiterLatency
	clientmetrics.RateLimiterLatency = previous
	t.Cleanup(func() {
		clientmetrics.RateLimiterLatency = original
	})

	MustMakeMetrics(runtime.NewScheme(), registry)
	// Re-registering must not install a second observer that would count every
	// request twice.
	if err := registerClientRateLimiterMetrics(registry); err != nil {
		t.Fatalf("re-registering client rate limiter metrics: %v", err)
	}

	clientmetrics.RateLimiterLatency.Observe(
		context.Background(),
		"GET",
		url.URL{Host: "kubernetes.default.svc"},
		250*time.Millisecond,
	)

	if previous.calls != 1 {
		t.Fatalf("expected previous observer to be called once, got %d calls", previous.calls)
	}
	if previous.verb != "GET" {
		t.Errorf("expected previous observer verb GET, got %q", previous.verb)
	}
	if previous.host != "kubernetes.default.svc" {
		t.Errorf("expected previous observer host kubernetes.default.svc, got %q", previous.host)
	}
	if previous.duration != 250*time.Millisecond {
		t.Errorf("expected previous observer duration 250ms, got %s", previous.duration)
	}

	metricFamilies, err := registry.Gather()
	if err != nil {
		t.Fatalf("gathering metrics: %v", err)
	}

	for _, metricFamily := range metricFamilies {
		if metricFamily.GetName() != "rest_client_rate_limiter_duration_seconds" {
			continue
		}

		if len(metricFamily.Metric) != 1 {
			t.Fatalf("expected one rate limiter metric, got %d", len(metricFamily.Metric))
		}

		metric := metricFamily.Metric[0]
		if metric.GetHistogram().GetSampleCount() != 1 {
			t.Errorf("expected sample count 1, got %d", metric.GetHistogram().GetSampleCount())
		}
		if metric.GetHistogram().GetSampleSum() != 0.25 {
			t.Errorf("expected sample sum 0.25, got %f", metric.GetHistogram().GetSampleSum())
		}

		upperBounds := make([]float64, 0, len(metric.GetHistogram().Bucket))
		for _, bucket := range metric.GetHistogram().Bucket {
			upperBounds = append(upperBounds, bucket.GetUpperBound())
		}
		expectedUpperBounds := []float64{0.005, 0.025, 0.1, 0.25, 0.5, 1, 2, 4, 8, 15, 30, 60}
		if len(upperBounds) != len(expectedUpperBounds) {
			t.Fatalf("expected %d histogram buckets, got %d", len(expectedUpperBounds), len(upperBounds))
		}
		for i := range upperBounds {
			if upperBounds[i] != expectedUpperBounds[i] {
				t.Errorf("expected bucket %d upper bound %f, got %f", i, expectedUpperBounds[i], upperBounds[i])
			}
		}

		labels := map[string]string{}
		for _, label := range metric.Label {
			labels[label.GetName()] = label.GetValue()
		}
		if labels["host"] != "kubernetes.default.svc" {
			t.Errorf("expected host label kubernetes.default.svc, got %q", labels["host"])
		}
		if labels["verb"] != "GET" {
			t.Errorf("expected verb label GET, got %q", labels["verb"])
		}
		return
	}

	t.Fatal("rate limiter metric was not registered")
}

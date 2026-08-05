package metrics

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	clientmetrics "k8s.io/client-go/tools/metrics"
)

const restClientRateLimiterDurationMetricName = "rest_client_rate_limiter_duration_seconds"

var restClientRateLimiterDurationBuckets = []float64{
	0.005,
	0.025,
	0.1,
	0.25,
	0.5,
	1,
	2,
	4,
	8,
	15,
	30,
	60,
}

type rateLimiterLatencyAdapter struct {
	next   clientmetrics.LatencyMetric
	metric *prometheus.HistogramVec
}

func (a *rateLimiterLatencyAdapter) Observe(
	ctx context.Context,
	verb string,
	u url.URL,
	duration time.Duration,
) {
	a.next.Observe(ctx, verb, u, duration)
	a.metric.WithLabelValues(verb, u.Host).Observe(duration.Seconds())
}

func registerClientRateLimiterMetrics(registrar prometheus.Registerer) error {
	metric := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    restClientRateLimiterDurationMetricName,
			Help:    "Client side rate limiter latency in seconds. Broken down by verb, and host.",
			Buckets: restClientRateLimiterDurationBuckets,
		},
		[]string{"verb", "host"},
	)

	if err := registrar.Register(metric); err != nil {
		var alreadyRegisteredError prometheus.AlreadyRegisteredError
		if errors.As(err, &alreadyRegisteredError) {
			return nil
		}
		return err
	}

	clientmetrics.RateLimiterLatency = &rateLimiterLatencyAdapter{
		next:   clientmetrics.RateLimiterLatency,
		metric: metric,
	}
	return nil
}

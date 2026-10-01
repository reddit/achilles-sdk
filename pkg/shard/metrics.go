package shard

import (
	"github.com/prometheus/client_golang/prometheus"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	valuesHeld = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "achilles_shard_values_held",
		Help: "Number of shard values this instance holds the Lease for.",
	}, []string{"instance"})

	// Contention is legal but always unintended: it means two instances were configured with the
	// same shard value, so which of them serves it is arbitrary rather than designed.
	valuesContested = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "achilles_shard_values_contested",
		Help: "Number of shard values this instance serves but another instance holds.",
	}, []string{"instance"})

	// A value every instance reports as ignored is one whose objects are reconciled by nobody:
	//
	//	min by (value) (achilles_shard_values_ignored) == 1
	valuesIgnored = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "achilles_shard_values_ignored",
		Help: "1 when a shard value objects carry is not served by this instance, 0 when it is.",
	}, []string{"instance", "value"})
)

func init() {
	ctrlmetrics.Registry.MustRegister(valuesHeld, valuesContested, valuesIgnored)
}

func recordClaims(instance string, held, contested int) {
	valuesHeld.WithLabelValues(instance).Set(float64(held))
	valuesContested.WithLabelValues(instance).Set(float64(contested))
}

// recordIgnored republishes this instance's per-value series from scratch, so that a value falling
// out of use stops being reported rather than lingering at its last reading.
func recordIgnored(instance string, ignored map[string]bool) {
	valuesIgnored.DeletePartialMatch(prometheus.Labels{"instance": instance})

	for value, isIgnored := range ignored {
		reading := 0.0
		if isIgnored {
			reading = 1.0
		}
		valuesIgnored.WithLabelValues(instance, value).Set(reading)
	}
}

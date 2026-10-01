package shard

import (
	"github.com/prometheus/client_golang/prometheus"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	valuesHeld = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "achilles_shard_values_held",
		Help: "Number of shard values this instance holds the ownership lease for.",
	}, []string{"group", "shard"})

	// Contention is legal but always unintended: it means two instances were configured with the
	// same shard value.
	valuesContested = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "achilles_shard_values_contested",
		Help: "Number of shard values this instance manages but another instance holds.",
	}, []string{"group", "shard"})

	// The alertable one, via min(): a value every instance reports as ignored is a value whose
	// objects are reconciled by nobody, because no instance was configured with it.
	//
	//	min by (group, value) (achilles_shard_values_ignored) == 1
	//
	// Reported for every value in use, 0 included, so that "ignored by all" is answerable without
	// knowing how many instances are running.
	valuesIgnored = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "achilles_shard_values_ignored",
		Help: "1 when a shard value in use is not managed by this instance, 0 when it is.",
	}, []string{"group", "shard", "value"})

	overlapDetected = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "achilles_shard_overlap",
		Help: "1 when another instance of this controller manages one of this instance's shard values.",
	}, []string{"group", "shard"})
)

func init() {
	ctrlmetrics.Registry.MustRegister(valuesHeld, valuesContested, valuesIgnored, overlapDetected)
}

func recordOwnership(group, shard string, held, contested int) {
	valuesHeld.WithLabelValues(group, shard).Set(float64(held))
	valuesContested.WithLabelValues(group, shard).Set(float64(contested))
}

// recordIgnored republishes the per-value series from scratch, so that a value falling out of use
// stops being reported rather than lingering at its last value forever.
func recordIgnored(group, shard string, ignored map[string]bool) {
	valuesIgnored.DeletePartialMatch(prometheus.Labels{"group": group, "shard": shard})

	for value, isIgnored := range ignored {
		v := 0.0
		if isIgnored {
			v = 1.0
		}
		valuesIgnored.WithLabelValues(group, shard, value).Set(v)
	}
}

func recordOverlap(group, shard string, overlapping bool) {
	value := 0.0
	if overlapping {
		value = 1.0
	}
	overlapDetected.WithLabelValues(group, shard).Set(value)
}

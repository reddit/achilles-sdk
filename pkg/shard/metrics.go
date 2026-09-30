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

	// Contention is legal but always unintended: it means two deployments were given selectors
	// that claim the same value.
	valuesContested = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "achilles_shard_values_contested",
		Help: "Number of shard values this instance selects but another shard holds.",
	}, []string{"group", "shard"})

	// The alertable one: an unowned value's objects are reconciled by nobody.
	valuesUnowned = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "achilles_shard_values_unowned",
		Help: "Number of shard values in use whose ownership lease no instance holds.",
	}, []string{"group", "shard"})

	overlapDetected = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "achilles_shard_selector_overlap",
		Help: "1 when another shard of this controller advertises an overlapping selector.",
	}, []string{"group", "shard"})

	reconcilesSkipped = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "achilles_shard_reconciles_skipped_total",
		Help: "Reconciles skipped because this instance does not own the object's shard value.",
	}, []string{"group", "shard", "reason"})
)

func init() {
	ctrlmetrics.Registry.MustRegister(
		valuesHeld, valuesContested, valuesUnowned, overlapDetected, reconcilesSkipped,
	)
}

func recordOwnership(group, shard string, held, contested, unowned int) {
	valuesHeld.WithLabelValues(group, shard).Set(float64(held))
	valuesContested.WithLabelValues(group, shard).Set(float64(contested))
	valuesUnowned.WithLabelValues(group, shard).Set(float64(unowned))
}

func recordOverlap(group, shard string, overlapping bool) {
	value := 0.0
	if overlapping {
		value = 1.0
	}
	overlapDetected.WithLabelValues(group, shard).Set(value)
}

func recordSkip(group, shard, reason string) {
	reconcilesSkipped.WithLabelValues(group, shard, reason).Inc()
}

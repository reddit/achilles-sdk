package metrics

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/reddit/achilles-sdk-api/api"
)

const (
	// ConditionDeleted is a value for the "achilles_resource_readiness" metric's "type" label, indicating that the object
	// is in terminating state.
	ConditionDeleted = "Deleted"
)

// Sink is a prometheus metrics sink for standard achilles metrics.
type Sink struct {
	readinessGauge              *prometheus.GaugeVec
	triggerCounter              *prometheus.CounterVec
	stateDurationHistogram      *prometheus.HistogramVec
	suspendGauge                *prometheus.GaugeVec
	processingDurationHistogram *prometheus.HistogramVec
}

// NewSink returns a new achilles metrics Sink.
func NewSink() *Sink {
	return &Sink{
		readinessGauge: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "achilles_resource_readiness",
				Help: "The status condition of type \"Ready\" for an Achilles resource.",
			},
			conditionGaugeLabel{}.names(),
		),
		triggerCounter: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "achilles_trigger",
				Help: "Total number of triggers per triggering event and triggered object.",
			},
			triggerCounterLabel{}.names(),
		),
		stateDurationHistogram: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "achilles_state_duration_seconds",
				Buckets: []float64{0.5, 0.90, 0.99},
				Help:    "Histogram of the time that a state has taken per reconciled object",
			},
			stateDurationHistogramLabel{}.names(),
		),
		suspendGauge: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "achilles_object_suspended",
				Help: "Gauge reporting whether the object is suspended or not",
			},
			suspendGaugeLabel{}.names(),
		),
		processingDurationHistogram: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "achilles_processing_duration_seconds",
				Buckets: []float64{0.5, 0.90, 0.99},
				Help:    "Histogram of the time taken to process an object spec update",
			},
			processingDurationHistogramLabel{}.names(),
		),
	}
}

// Reset resets all metrics.
func (r *Sink) Reset() {
	r.readinessGauge.Reset()
	r.triggerCounter.Reset()
	r.stateDurationHistogram.Reset()
	r.suspendGauge.Reset()
	r.processingDurationHistogram.Reset()
}

// Collectors returns a slice of Prometheus collectors, which can be used to register them in a metrics registry.
func (r *Sink) Collectors() []prometheus.Collector {
	return []prometheus.Collector{
		r.readinessGauge,
		r.triggerCounter,
		r.stateDurationHistogram,
		r.suspendGauge,
		r.processingDurationHistogram,
	}
}

// RecordCondition records the status condition for the types True, False, and Deleted, for the
// specified object and condition.
func (r *Sink) RecordCondition(
	ref client.ObjectKey,
	gvk schema.GroupVersionKind,
	condition api.Condition,
	deleted bool,
) {
	for _, status := range []string{
		string(metav1.ConditionTrue),
		string(metav1.ConditionFalse),
		string(metav1.ConditionUnknown),
		ConditionDeleted,
	} {
		var value float64
		if deleted {
			if status == ConditionDeleted {
				value = 1
			}
		} else {
			if status == string(condition.Status) {
				value = 1
			}
		}
		r.readinessGauge.WithLabelValues(
			conditionGaugeLabel{
				group:         gvk.Group,
				version:       gvk.Version,
				kind:          gvk.Kind,
				name:          ref.Name,
				namespace:     ref.Namespace,
				conditionType: condition.Type.String(),
				status:        status,
			}.values()...,
		).Set(value)
	}
}

// DeleteCondition deletes the status condition for the types True, False, and Deleted, for the
// specified object and condition.
// Returns the number of metrics deleted.
func (r *Sink) DeleteCondition(
	ref client.ObjectKey,
	gvk schema.GroupVersionKind,
	condition api.Condition,
) int {
	var numDeleted int

	for _, status := range []string{
		string(metav1.ConditionTrue),
		string(metav1.ConditionFalse),
		string(metav1.ConditionUnknown),
		ConditionDeleted,
	} {
		deleted := r.readinessGauge.DeleteLabelValues(
			conditionGaugeLabel{
				group:         gvk.Group,
				version:       gvk.Version,
				kind:          gvk.Kind,
				name:          ref.Name,
				namespace:     ref.Namespace,
				conditionType: condition.Type.String(),
				status:        status,
			}.values()...,
		)
		if deleted {
			numDeleted += 1
		}
	}

	return numDeleted
}

// RecordTrigger increments the counter for the given controller, qualified by the triggering object GVK and object ref
// and reconciled object ref.
func (r *Sink) RecordTrigger(
	triggerGVK schema.GroupVersionKind,
	requestObjKey client.ObjectKey,
	event string,
	triggerType string,
	controllerName string,
) {
	r.triggerCounter.WithLabelValues(
		triggerCounterLabel{
			group:        triggerGVK.Group,
			version:      triggerGVK.Version,
			kind:         triggerGVK.Kind,
			reqName:      requestObjKey.Name,
			reqNamespace: requestObjKey.Namespace,
			event:        event,
			triggerType:  triggerType,
			controller:   controllerName,
		}.values()...,
	).Inc()
}

// DeleteTrigger deletes the trigger metric for the specified requested object and controller name,
// and ALL triggering GVKs, event types, and trigger types.
func (r *Sink) DeleteTrigger(
	requestObjKey client.ObjectKey,
	controllerName string,
) int {
	return r.triggerCounter.DeletePartialMatch(
		triggerCounterLabel{
			reqName:      requestObjKey.Name,
			reqNamespace: requestObjKey.Namespace,
			controller:   controllerName,
		}.partialValues(),
	)
}

// RecordStateDuration records the duration taken to execute a state in the FSM reconciler.
func (r *Sink) RecordStateDuration(
	gvk schema.GroupVersionKind,
	state string,
	duration time.Duration,
) {

	r.stateDurationHistogram.WithLabelValues(
		stateDurationHistogramLabel{
			group:   gvk.Group,
			version: gvk.Version,
			kind:    gvk.Kind,
			state:   state,
		}.values()...,
	).Observe(duration.Seconds())
}

// RecordSuspend records whether the object is suspended or not
func (r *Sink) RecordSuspend(
	ref client.ObjectKey,
	gvk schema.GroupVersionKind,
	suspended bool,
) {
	var value float64
	if suspended {
		value = 1
	}
	r.suspendGauge.WithLabelValues(
		suspendGaugeLabel{
			group:     gvk.Group,
			version:   gvk.Version,
			kind:      gvk.Kind,
			name:      ref.Name,
			namespace: ref.Namespace,
		}.values()...,
	).Set(value)
}

// RecordProcessingDuration records the time taken to process an object of a given metadata.generation.
func (r *Sink) RecordProcessingDuration(
	gvk schema.GroupVersionKind,
	duration time.Duration,
	success bool,
) {

	r.processingDurationHistogram.WithLabelValues(
		processingDurationHistogramLabel{
			group:   gvk.Group,
			version: gvk.Version,
			kind:    gvk.Kind,
			success: strconv.FormatBool(success),
		}.values()...,
	).Observe(duration.Seconds())
}

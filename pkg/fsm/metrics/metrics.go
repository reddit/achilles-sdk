package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/reddit/achilles-sdk-api/api"
	"github.com/reddit/achilles-sdk/pkg/fsm/types"
	"github.com/reddit/achilles-sdk/pkg/meta"
)

type conditionedObject interface {
	api.Conditioned
	client.Object
}

type Metrics struct {
	scheme  *runtime.Scheme
	sink    *Sink
	options types.MetricsOptions
}

// MustMakeMetrics creates a new Metrics with a new metrics Sink, and the Metrics.Scheme set to that of the given manager.
func MustMakeMetrics(scheme *runtime.Scheme, registrar prometheus.Registerer) *Metrics {
	metricsRecorder := NewSink()
	registrar.MustRegister(metricsRecorder.Collectors()...)

	return &Metrics{
		scheme: scheme,
		sink:   metricsRecorder,
	}
}

// MustMakeMetricsWithOptions creates a new Metrics with a new metrics Sink, the Metrics.Scheme set to that of the given manager and MetricsOptions.
func MustMakeMetricsWithOptions(scheme *runtime.Scheme, registrar prometheus.Registerer, options types.MetricsOptions) *Metrics {
	metricsRecorder := NewSink()
	registrar.MustRegister(metricsRecorder.Collectors()...)

	return &Metrics{
		scheme:  scheme,
		sink:    metricsRecorder,
		options: options,
	}
}

// Reset resets all metrics.
func (m *Metrics) Reset() {
	m.sink.Reset()
}

// RecordTrigger records an event trigger for the given triggering object and triggered object.
func (m *Metrics) RecordTrigger(
	triggerGVK schema.GroupVersionKind,
	requestObjKey client.ObjectKey,
	event string,
	triggerType string,
	controllerName string,
) {
	if m.sink == nil || m.options.IsMetricDisabled(types.AchillesResourceTrigger) {
		return
	}

	m.sink.RecordTrigger(triggerGVK, requestObjKey, event, triggerType, controllerName)
}

// DeleteTrigger deletes an event trigger for the given triggered object and controller name.
func (m *Metrics) DeleteTrigger(
	requestObjKey client.ObjectKey,
	controllerName string,
) {
	if m.sink == nil || m.options.IsMetricDisabled(types.AchillesResourceTrigger) {
		return
	}

	m.sink.DeleteTrigger(requestObjKey, controllerName)
}

// RecordReadiness records the meta.ReadyCondition status for the given obj.
func (m *Metrics) RecordReadiness(obj conditionedObject) {
	if m.options.IsMetricDisabled(types.AchillesResourceReadiness) {
		return
	}
	m.RecordCondition(obj, api.TypeReady)
}

// DeleteReadiness deletes the meta.ReadyCondition status metric for the given obj.
func (m *Metrics) DeleteReadiness(obj conditionedObject) {
	if m.options.IsMetricDisabled(types.AchillesResourceReadiness) {
		return
	}
	m.DeleteCondition(obj, api.TypeReady)
}

// RecordCondition records the status of the given conditionType for the given obj.
func (m *Metrics) RecordCondition(obj conditionedObject, conditionType api.ConditionType) {
	if m.sink == nil || m.options.IsMetricDisabled(types.AchillesResourceCondition) {
		return
	}

	condition := obj.GetCondition(conditionType)
	typedObjectRef := meta.MustTypedObjectRefFromObject(obj, m.scheme)

	m.sink.RecordCondition(
		typedObjectRef.ObjectKey(),
		typedObjectRef.GroupVersionKind(),
		condition,
		!obj.GetDeletionTimestamp().IsZero(),
	)
}

// DeleteCondition deletes the status of the given conditionType for the given obj.
func (m *Metrics) DeleteCondition(obj conditionedObject, conditionType api.ConditionType) {
	if m.sink == nil || m.options.IsMetricDisabled(types.AchillesResourceCondition) {
		return
	}

	condition := obj.GetCondition(conditionType)
	typedObjectRef := meta.MustTypedObjectRefFromObject(obj, m.scheme)

	m.sink.DeleteCondition(
		typedObjectRef.ObjectKey(),
		typedObjectRef.GroupVersionKind(),
		condition,
	)
}

// RecordStateDuration records the duration of the state for the given GVK.
func (m *Metrics) RecordStateDuration(gvk schema.GroupVersionKind, state string, duration time.Duration) {
	if m.sink == nil || m.options.IsMetricDisabled(types.AchillesStateDuration) {
		return
	}

	m.sink.RecordStateDuration(gvk, state, duration)
}

// RecordSuspend records status of the object to be 1 if suspended and 0 if unsuspended
func (m *Metrics) RecordSuspend(obj client.Object, suspend bool) {
	if m.sink == nil || m.options.IsMetricDisabled(types.AchillesSuspend) {
		return
	}

	typedObjectRef := meta.MustTypedObjectRefFromObject(obj, m.scheme)
	m.sink.RecordSuspend(typedObjectRef.ObjectKey(), typedObjectRef.GroupVersionKind(), suspend)
}

package metrics

import (
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/reddit/achilles-sdk-api/api"
	"github.com/reddit/achilles-sdk/pkg/fsm/metrics/internal"
	"github.com/reddit/achilles-sdk/pkg/fsm/types"
	"github.com/reddit/achilles-sdk/pkg/meta"
)

type conditionedObject interface {
	api.Conditioned
	client.Object
}

type processingStartTimes interface {
	// GetRange returns the processing start times for all requests with name, namespace, and generation <= observedGeneration.
	GetRange(name string, namespace string, observedGeneration int64, success bool) []time.Time
	// Set sets the processing start time for the given request and whether it failed.
	Set(name string, namespace string, observedGeneration int64, startTime time.Time)
	// SetRangeFailed sets the processing start time for the given request and marks it as failed.
	// This is to avoid double counting the processing duration for failed requests.
	SetRangeFailed(name string, namespace string, observedGeneration int64)
	// DeleteRange deletes all processing start times for the given (name, namespace) where generation <= observedGeneration.
	DeleteRange(name string, namespace string, observedGeneration int64)
}

type Metrics struct {
	scheme  *runtime.Scheme
	sink    *Sink
	options types.MetricsOptions

	// a map of GVK to processingStartTimes
	processingStartTimesByGVK map[schema.GroupVersionKind]processingStartTimes
}

// MustMakeMetrics creates a new Metrics with a new metrics Sink, and the Metrics.Scheme set to that of the given manager.
func MustMakeMetrics(scheme *runtime.Scheme, registrar prometheus.Registerer) *Metrics {
	metricsRecorder := NewSink()
	registrar.MustRegister(metricsRecorder.Collectors()...)

	return &Metrics{
		scheme:                    scheme,
		sink:                      metricsRecorder,
		processingStartTimesByGVK: make(map[schema.GroupVersionKind]processingStartTimes),
	}
}

// MustMakeMetricsWithOptions creates a new Metrics with a new metrics Sink, the Metrics.Scheme set to that of the given manager and MetricsOptions.
func MustMakeMetricsWithOptions(scheme *runtime.Scheme, registrar prometheus.Registerer, options types.MetricsOptions) *Metrics {
	metricsRecorder := NewSink()
	registrar.MustRegister(metricsRecorder.Collectors()...)

	return &Metrics{
		scheme:                    scheme,
		sink:                      metricsRecorder,
		options:                   options,
		processingStartTimesByGVK: make(map[schema.GroupVersionKind]processingStartTimes),
	}
}

// InitializeForGVK initializes metrics for the given GVK.
// NOTE: this is not thread-safe, but should only be called in synchronous code in application start up.
func (m *Metrics) InitializeForGVK(gvk schema.GroupVersionKind) {
	// initialize processingStartTimes for the given GVK
	if _, ok := m.processingStartTimesByGVK[gvk]; !ok {
		m.processingStartTimesByGVK[gvk] = internal.NewProcessingStartTimes()
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
	if m.sink == nil {
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

// RecordProcessingStart records the start time of processing for the given GVK and request.
// This doesn't record a metric, but the start time is used to calculate the processing duration later.
func (m *Metrics) RecordProcessingStart(
	gvk schema.GroupVersionKind,
	req reconcile.Request,
	gen int64,
) error {
	if m.sink == nil || m.options.IsMetricDisabled(types.AchillesProcessingDuration) {
		return nil
	}

	// get the processing start time for the given GVK
	// NOTE: this does not need to be guarded by a mutex because it's guaranteed to only receive concurrent reads
	processingStartTimes, ok := m.processingStartTimesByGVK[gvk]
	if !ok {
		return fmt.Errorf("no processing start time found for GVK %s, missing a call to metrics.InitializeForGVK()", gvk.String())
	}

	processingStartTimes.Set(req.Name, req.Namespace, gen, time.Now())

	return nil
}

// RecordProcessingDuration records the time taken to process an object of a given metadata.generation.
func (m *Metrics) RecordProcessingDuration(
	gvk schema.GroupVersionKind,
	req reconcile.Request,
	gen int64,
	success bool,
) error {
	if m.sink == nil || m.options.IsMetricDisabled(types.AchillesProcessingDuration) {
		return nil
	}

	// get the processing start time for the given GVK
	processingStartTimes, ok := m.processingStartTimesByGVK[gvk]
	if !ok {
		return fmt.Errorf("no processing start time found for GVK %s, missing a call to metrics.InitializeForGVK()", gvk.String())
	}

	// get the processing start time for the given request
	startTimes := processingStartTimes.GetRange(req.Name, req.Namespace, gen, success)

	now := time.Now()
	for _, startTime := range startTimes {
		duration := now.Sub(startTime)
		m.sink.RecordProcessingDuration(gvk, duration, success)
	}

	if success {
		// if the processing was successful, delete all matched items from the tree to prevent unbounded memory growth
		processingStartTimes.DeleteRange(req.Name, req.Namespace, gen)
	} else {
		// if the processing failed, mark all items of (name, namespace) with generation < observedGeneration as failed to avoid subsequent double counting
		processingStartTimes.SetRangeFailed(req.Name, req.Namespace, gen)
	}

	return nil
}

// RecordEvent records a metric for an event for the given object.
func (m *Metrics) RecordEvent(
	triggerGVK schema.GroupVersionKind,
	objectName string,
	objectNamespace string,
	eventType string,
	reason string,
	controllerName string,
) {
	if m.sink == nil {
		return
	}

	m.sink.RecordEvent(triggerGVK, objectName, objectNamespace, eventType, reason, controllerName)
}

// DeleteEvent deletes event metrics for the given triggered object and controller name.
func (m *Metrics) DeleteEvent(
	obj client.Object,
) {
	if m.sink == nil {
		return
	}

	typedObjectRef := meta.MustTypedObjectRefFromObject(obj, m.scheme)
	m.sink.DeleteEvent(typedObjectRef.ObjectKey(), typedObjectRef.GroupVersionKind())
}

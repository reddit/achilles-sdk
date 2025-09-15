package events

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/reddit/achilles-sdk/pkg/fsm/metrics"
)

const (
	eventReadyReason = "Ready"

	eventTypeNormal  = "Normal"
	eventTypeWarning = "Warning"
)

type EventRecorder struct {
	recorder record.EventRecorder
	metrics  *metrics.Metrics

	controllerName string

	client client.Client
	scheme *runtime.Scheme
}

// NewEventRecorder creates a new EventRecorder for the given controller and manager.
// Metrics is optional and can be nil. If provided, it will be used to emit metrics for each event.
func NewEventRecorder(controllerName string, manager ctrl.Manager, metrics *metrics.Metrics) *EventRecorder {
	return &EventRecorder{
		recorder:       manager.GetEventRecorderFor(controllerName),
		metrics:        metrics,
		controllerName: controllerName,
		client:         manager.GetClient(),
		scheme:         manager.GetScheme(),
	}
}

// RecordReady records a ready event for the given object
// It will only record the event the first time it is called (i.e. deduplication is always enabled for the Ready event)
func (e *EventRecorder) RecordReady(obj client.Object, message string) {
	if message == "" {
		message = "Object is ready"
	}
	e.RecordEvent(obj, eventReadyReason, message, true)
}

// RecordWarning records a warning event for the given object.
// If deduplicationEnabled is true, it will only record the event the first time it is called.
func (e *EventRecorder) RecordWarning(obj client.Object, reason string, message string, deduplicationEnabled bool) {
	if deduplicationEnabled {
		if e.isDuplicateEventForObject(obj, eventTypeWarning, reason, message) {
			return // Skip recording duplicate event
		}
	}

	e.recorder.Event(obj, eventTypeWarning, reason, message)

	if e.metrics != nil {
		e.metrics.RecordEvent(obj.GetObjectKind().GroupVersionKind(), obj.GetName(), obj.GetNamespace(), eventTypeWarning, reason, e.controllerName)
	}
}

// RecordEvent records a normal event for the given object.
// If deduplicationEnabled is true, it will only record the event the first time it is called.
func (e *EventRecorder) RecordEvent(obj client.Object, reason string, message string, deduplicationEnabled bool) {
	if deduplicationEnabled {
		if e.isDuplicateEventForObject(obj, eventTypeNormal, reason, message) {
			return // Skip recording duplicate event
		}
	}

	e.recorder.Event(obj, eventTypeNormal, reason, message)

	if e.metrics != nil {
		e.metrics.RecordEvent(obj.GetObjectKind().GroupVersionKind(), obj.GetName(), obj.GetNamespace(), eventTypeNormal, reason, e.controllerName)
	}
}

// isDuplicateEventForObject checks if an event with the same type, reason, and message already exists for the object.
func (e *EventRecorder) isDuplicateEventForObject(obj client.Object, eventType, reason, message string) bool {
	// Get existing events using UID field selector
	eventList := &corev1.EventList{}
	fieldSelector := client.MatchingFields{
		"involvedObject.uid": string(obj.GetUID()),
	}
	err := e.client.List(context.Background(), eventList, fieldSelector)
	if err != nil {
		// If we can't retrieve existing events, err on the side of caution and allow the event
		return false
	}

	// Check if any existing event matches the type, reason, and message
	for _, event := range eventList.Items {
		if event.Type == eventType && event.Reason == reason && event.Message == message {
			return true
		}
	}
	return false
}

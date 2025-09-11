package events

import (
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/reddit/achilles-sdk/pkg/fsm/metrics"
)

const (
	EventReadyReason = "Ready"

	EventTypeNormal  = "Normal"
	EventTypeWarning = "Warning"
)

type EventRecorder struct {
	recorder record.EventRecorder
	metrics  *metrics.Metrics

	controllerName string
}

// NewEventRecorder creates a new EventRecorder for the given controller and manager.
// metrics is optional and can be nil. if provided, it will be used to emit metrics for each event.
func NewEventRecorder(controllerName string, manager ctrl.Manager, metrics *metrics.Metrics) *EventRecorder {
	return &EventRecorder{recorder: manager.GetEventRecorderFor(controllerName), metrics: metrics, controllerName: controllerName}
}

// RecordReady records a ready event for the given object.
// message is optional and defaults to "Object is ready".
func (e *EventRecorder) RecordReady(obj client.Object, message string) {
	if message == "" {
		message = "Object is ready"
	}
	e.recorder.Event(obj, EventTypeNormal, EventReadyReason, message)

	if e.metrics != nil {
		e.metrics.RecordEvent(obj.GetObjectKind().GroupVersionKind(), obj.GetName(), obj.GetNamespace(), EventTypeNormal, EventReadyReason, e.controllerName)
	}
}

// RecordWarning records a warning event for the given object.
func (e *EventRecorder) RecordWarning(obj client.Object, reason string, message string) {
	e.recorder.Event(obj, EventTypeWarning, reason, message)

	if e.metrics != nil {
		e.metrics.RecordEvent(obj.GetObjectKind().GroupVersionKind(), obj.GetName(), obj.GetNamespace(), EventTypeWarning, reason, e.controllerName)
	}
}

// RecordEvent records a normal event for the given object.
func (e *EventRecorder) RecordEvent(obj client.Object, reason string, message string) {
	e.recorder.Event(obj, EventTypeNormal, reason, message)

	if e.metrics != nil {
		e.metrics.RecordEvent(obj.GetObjectKind().GroupVersionKind(), obj.GetName(), obj.GetNamespace(), EventTypeNormal, reason, e.controllerName)
	}
}

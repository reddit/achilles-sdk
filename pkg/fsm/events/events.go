package events

import (
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	EventReadyReason = "Ready"

	EventTypeNormal  = "Normal"
	EventTypeWarning = "Warning"
)

type EventRecorder struct {
	recorder record.EventRecorder
}

// NewEventRecorder creates a new EventRecorder for the given controller and manager.
func NewEventRecorder(controllerName string, manager ctrl.Manager) *EventRecorder {
	return &EventRecorder{recorder: manager.GetEventRecorderFor(controllerName)}
}

// RecordReady records a ready event for the given object.
// message is optional and defaults to "Object is ready".
func (e *EventRecorder) RecordReady(obj client.Object, message string) {
	if message == "" {
		message = "Object is ready"
	}
	e.recorder.Event(obj, EventTypeNormal, EventReadyReason, message)
}

// RecordWarning records a warning event for the given object.
func (e *EventRecorder) RecordWarning(obj client.Object, reason string, message string) {
	e.recorder.Event(obj, EventTypeWarning, reason, message)
}

// RecordEvent records a normal event for the given object.
func (e *EventRecorder) RecordEvent(obj client.Object, reason string, message string) {
	e.recorder.Event(obj, EventTypeNormal, reason, message)
}

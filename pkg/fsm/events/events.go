// Package events provides Kubernetes event recording
//
// The events package offers a high-level interface for recording Kubernetes events
// with deduplication to optionally prevent spam and reduce noise in event logs.
//
// # Basic Usage
//
// Create an EventRecorder using NewEventRecorder:
//
//	eventRecorder := events.NewEventRecorder("my-controller", manager, metrics)
//
// Record different types of events:
//
//	// Record a ready event (always deduplicated)
//	eventRecorder.RecordReady(obj, "Object is now ready")
//
//	// Record a normal event with optional deduplication
//	eventRecorder.RecordEvent(obj, "ProcessingComplete", "Processing finished successfully", true)
//
//	// Record a warning event with optional deduplication
//	eventRecorder.RecordWarning(obj, "ValidationFailed", "Invalid configuration detected", true)
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
//
// The EventRecorder provides a high-level interface for recording Kubernetes events.
//
// Parameters:
//   - controllerName: Name of the controller (used for event attribution)
//   - manager: Controller-runtime manager
//   - metrics: Optional metrics recorder (can be nil to disable metrics)
//
// Returns a configured EventRecorder ready for use.
func NewEventRecorder(controllerName string, manager ctrl.Manager, metrics *metrics.Metrics) *EventRecorder {
	return &EventRecorder{
		recorder:       manager.GetEventRecorderFor(controllerName),
		metrics:        metrics,
		controllerName: controllerName,
		client:         manager.GetClient(),
		scheme:         manager.GetScheme(),
	}
}

// RecordReady records a ready event for the given object.
//
// This method always enables deduplication to prevent spam of "ready" events.
// If message is empty, it defaults to "Object is ready".
//
// Parameters:
//   - obj: The Kubernetes object to record the event for
//   - message: Optional message (defaults to "Object is ready" if empty)
//
// Example:
//
//	eventRecorder.RecordReady(pod, "Pod is ready to serve traffic")
func (e *EventRecorder) RecordReady(obj client.Object, message string) {
	if message == "" {
		message = "Object is ready"
	}
	e.RecordEvent(obj, eventReadyReason, message, true)
}

// RecordWarning records a warning event for the given object.
//
// Warning events indicate errors, failures, or problematic conditions.
//
// Parameters:
//   - obj: The Kubernetes object to record the event for
//   - reason: The reason for the warning (e.g., "ValidationFailed", "ResourceNotFound")
//   - message: Descriptive message explaining the warning
//   - deduplicationEnabled: If true, identical events will not be recorded
//
// Example:
//
//	eventRecorder.RecordWarning(pod, "ImagePullFailed", "Failed to pull image: nginx:latest", true)
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
//
// Normal events indicate successful operations, state changes, or informational updates.
//
// Parameters:
//   - obj: The Kubernetes object to record the event for
//   - reason: The reason for the event (e.g., "ProcessingComplete", "ConfigUpdated")
//   - message: Descriptive message explaining the event
//   - deduplicationEnabled: If true, identical events will not be recorded
//
// Example:
//
//	eventRecorder.RecordEvent(deployment, "ScalingComplete", "Scaled to 3 replicas", true)
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

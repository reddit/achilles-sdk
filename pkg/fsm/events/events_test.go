package events

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// MockEventRecorder is a mock implementation of record.EventRecorder
type MockEventRecorder struct {
	mock.Mock
}

func (m *MockEventRecorder) Event(object runtime.Object, eventType, reason, message string) {
	m.Called(object, eventType, reason, message)
}

func (m *MockEventRecorder) Eventf(object runtime.Object, eventType, reason, messageFmt string, args ...interface{}) {
	m.Called(object, eventType, reason, messageFmt, args)
}

func (m *MockEventRecorder) AnnotatedEventf(object runtime.Object, annotations map[string]string, eventType, reason, messageFmt string, args ...interface{}) {
	m.Called(object, annotations, eventType, reason, messageFmt, args)
}

func TestEventRecorder_RecordEvent(t *testing.T) {
	scheme := runtime.NewScheme()
	corev1.AddToScheme(scheme)

	tests := []struct {
		name                 string
		deduplicationEnabled bool
		existingEvents       []corev1.Event
		newEventType         string
		newEventReason       string
		newEventMessage      string
		expectedEventCount   int
		expectEventRecorded  bool
	}{
		{
			name:                 "deduplication disabled - should always record",
			deduplicationEnabled: false,
			existingEvents: []corev1.Event{
				{
					TypeMeta: metav1.TypeMeta{Kind: "Event"},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-event-1",
						Namespace: "default",
					},
					Type:           "Normal",
					Reason:         "TestReason",
					Message:        "Test message",
					FirstTimestamp: metav1.Now(),
					InvolvedObject: corev1.ObjectReference{
						Name:       "test-configmap",
						Namespace:  "default",
						Kind:       "ConfigMap",
						APIVersion: "v1",
						UID:        "test-configmap-uid",
					},
				},
			},
			newEventType:        "Normal",
			newEventReason:      "TestReason",
			newEventMessage:     "Test message",
			expectedEventCount:  1,
			expectEventRecorded: true,
		},
		{
			name:                 "deduplication enabled - duplicate event - our event should NOT be recorded",
			deduplicationEnabled: true,
			existingEvents: []corev1.Event{
				{
					TypeMeta: metav1.TypeMeta{Kind: "Event"},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-event-1",
						Namespace: "default",
					},
					Type:           "Normal",
					Reason:         "TestReason",
					Message:        "Test message",
					FirstTimestamp: metav1.Now(),
					InvolvedObject: corev1.ObjectReference{
						Name:       "test-configmap",
						Namespace:  "default",
						Kind:       "ConfigMap",
						APIVersion: "v1",
						UID:        "test-configmap-uid",
					},
				},
			},
			newEventType:        "Normal",
			newEventReason:      "TestReason",
			newEventMessage:     "Test message",
			expectedEventCount:  0,
			expectEventRecorded: false,
		},
		{
			name:                 "deduplication enabled - different reason - our event should be recorded",
			deduplicationEnabled: true,
			existingEvents: []corev1.Event{
				{
					TypeMeta: metav1.TypeMeta{Kind: "Event"},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-event-1",
						Namespace: "default",
					},
					Type:           "Normal",
					Reason:         "TestReason",
					Message:        "Test message",
					FirstTimestamp: metav1.Now(),
					InvolvedObject: corev1.ObjectReference{
						Name:       "test-configmap",
						Namespace:  "default",
						Kind:       "ConfigMap",
						APIVersion: "v1",
						UID:        "test-configmap-uid",
					},
				},
			},
			newEventType:        "Normal",
			newEventReason:      "DifferentReason",
			newEventMessage:     "Test message",
			expectedEventCount:  1,
			expectEventRecorded: true,
		},
		{
			name:                 "deduplication enabled - different message - our event should be recorded",
			deduplicationEnabled: true,
			existingEvents: []corev1.Event{
				{
					TypeMeta: metav1.TypeMeta{Kind: "Event"},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-event-1",
						Namespace: "default",
					},
					Type:           "Normal",
					Reason:         "TestReason",
					Message:        "Test message",
					FirstTimestamp: metav1.Now(),
					InvolvedObject: corev1.ObjectReference{
						Name:       "test-configmap",
						Namespace:  "default",
						Kind:       "ConfigMap",
						APIVersion: "v1",
						UID:        "test-configmap-uid",
					},
				},
			},
			newEventType:        "Normal",
			newEventReason:      "TestReason",
			newEventMessage:     "Different message",
			expectedEventCount:  1,
			expectEventRecorded: true,
		},
		{
			name:                 "deduplication enabled - no existing events - our event should be recorded",
			deduplicationEnabled: true,
			existingEvents:       []corev1.Event{},
			newEventType:         "Normal",
			newEventReason:       "TestReason",
			newEventMessage:      "Test message",
			expectedEventCount:   1,
			expectEventRecorded:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a fake client with existing events and field index for UID
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithIndex(&corev1.Event{}, "involvedObject.uid", func(obj client.Object) []string {
					event := obj.(*corev1.Event)
					return []string{string(event.InvolvedObject.UID)}
				}).
				Build()

			// Add existing events to the fake client
			for _, event := range tt.existingEvents {
				err := fakeClient.Create(context.Background(), &event)
				assert.NoError(t, err)
			}

			// Create mock event recorder
			mockRecorder := &MockEventRecorder{}

			// Create EventRecorder
			eventRecorder := &EventRecorder{
				recorder:       mockRecorder,
				metrics:        nil, // No metrics for this test
				controllerName: "test-controller",
				client:         fakeClient,
				scheme:         scheme,
			}

			// Create test object
			testObj := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-configmap",
					Namespace: "default",
					UID:       "test-configmap-uid",
				},
			}
			// Set the GVK explicitly
			testObj.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("ConfigMap"))

			// Set up mock expectations
			if tt.expectEventRecorded {
				// RecordEventWithDeduplication always records "Normal" events
				mockRecorder.On("Event", testObj, "Normal", tt.newEventReason, tt.newEventMessage).Once()
			}

			// Record the event
			eventRecorder.RecordEvent(testObj, tt.newEventReason, tt.newEventMessage, tt.deduplicationEnabled)

			// Verify mock expectations
			mockRecorder.AssertExpectations(t)
		})
	}
}

func TestEventRecorder_RecordReady(t *testing.T) {
	scheme := runtime.NewScheme()
	corev1.AddToScheme(scheme)

	tests := []struct {
		name                string
		existingEvents      []corev1.Event
		message             string
		expectEventRecorded bool
	}{
		{
			name: "duplicate ready event - our ready event should NOT be recorded",
			existingEvents: []corev1.Event{
				{
					TypeMeta: metav1.TypeMeta{Kind: "Event"},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-event-1",
						Namespace: "default",
					},
					Type:           "Normal",
					Reason:         "Ready",
					Message:        "Object is ready",
					FirstTimestamp: metav1.Now(),
					InvolvedObject: corev1.ObjectReference{
						Name:       "test-configmap",
						Namespace:  "default",
						Kind:       "ConfigMap",
						APIVersion: "v1",
						UID:        "test-configmap-uid",
					},
				},
			},
			message:             "",
			expectEventRecorded: false,
		},
		{
			name: "different ready message - our ready event should be recorded",
			existingEvents: []corev1.Event{
				{
					TypeMeta: metav1.TypeMeta{Kind: "Event"},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-event-1",
						Namespace: "default",
					},
					Type:           "Normal",
					Reason:         "Ready",
					Message:        "Object is ready",
					FirstTimestamp: metav1.Now(),
					InvolvedObject: corev1.ObjectReference{
						Name:       "test-configmap",
						Namespace:  "default",
						Kind:       "ConfigMap",
						APIVersion: "v1",
						UID:        "test-configmap-uid",
					},
				},
			},
			message:             "Custom ready message",
			expectEventRecorded: true,
		},
		{
			name:                "no existing events - our ready event should be recorded",
			existingEvents:      []corev1.Event{},
			message:             "",
			expectEventRecorded: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a fake client with existing events and field index for UID
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithIndex(&corev1.Event{}, "involvedObject.uid", func(obj client.Object) []string {
					event := obj.(*corev1.Event)
					return []string{string(event.InvolvedObject.UID)}
				}).
				Build()

			// Add existing events to the fake client
			for _, event := range tt.existingEvents {
				err := fakeClient.Create(context.Background(), &event)
				assert.NoError(t, err)
			}

			// Create mock event recorder
			mockRecorder := &MockEventRecorder{}

			// Create EventRecorder
			eventRecorder := &EventRecorder{
				recorder:       mockRecorder,
				metrics:        nil,
				controllerName: "test-controller",
				client:         fakeClient,
				scheme:         scheme,
			}

			// Create test object
			testObj := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-configmap",
					Namespace: "default",
					UID:       "test-configmap-uid",
				},
			}
			// Set the GVK explicitly
			testObj.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("ConfigMap"))

			// Set up mock expectations
			if tt.expectEventRecorded {
				expectedMessage := tt.message
				if expectedMessage == "" {
					expectedMessage = "Object is ready"
				}
				mockRecorder.On("Event", testObj, "Normal", "Ready", expectedMessage).Once()
			}

			// Record the ready event
			eventRecorder.RecordReady(testObj, tt.message)

			// Verify mock expectations
			mockRecorder.AssertExpectations(t)
		})
	}
}

func TestEventRecorder_RecordWarning(t *testing.T) {
	scheme := runtime.NewScheme()
	corev1.AddToScheme(scheme)

	tests := []struct {
		name                 string
		deduplicationEnabled bool
		existingEvents       []corev1.Event
		reason               string
		message              string
		expectEventRecorded  bool
	}{
		{
			name:                 "deduplication enabled - duplicate warning event - our warning event should NOT be recorded",
			deduplicationEnabled: true,
			existingEvents: []corev1.Event{
				{
					TypeMeta: metav1.TypeMeta{Kind: "Event"},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-event-1",
						Namespace: "default",
					},
					Type:           "Warning",
					Reason:         "TestWarning",
					Message:        "Test warning message",
					FirstTimestamp: metav1.Now(),
					InvolvedObject: corev1.ObjectReference{
						Name:       "test-configmap",
						Namespace:  "default",
						Kind:       "ConfigMap",
						APIVersion: "v1",
						UID:        "test-configmap-uid",
					},
				},
			},
			reason:              "TestWarning",
			message:             "Test warning message",
			expectEventRecorded: false,
		},
		{
			name:                 "deduplication enabled - different warning reason - our warning event should be recorded",
			deduplicationEnabled: true,
			existingEvents: []corev1.Event{
				{
					TypeMeta: metav1.TypeMeta{Kind: "Event"},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-event-1",
						Namespace: "default",
					},
					Type:           "Warning",
					Reason:         "TestWarning",
					Message:        "Test warning message",
					FirstTimestamp: metav1.Now(),
					InvolvedObject: corev1.ObjectReference{
						Name:       "test-configmap",
						Namespace:  "default",
						Kind:       "ConfigMap",
						APIVersion: "v1",
						UID:        "test-configmap-uid",
					},
				},
			},
			reason:              "DifferentWarning",
			message:             "Test warning message",
			expectEventRecorded: true,
		},
		{
			name:                 "deduplication disabled - duplicate warning event - our warning event should be recorded",
			deduplicationEnabled: false,
			existingEvents: []corev1.Event{
				{
					TypeMeta: metav1.TypeMeta{Kind: "Event"},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-event-1",
						Namespace: "default",
					},
					Type:           "Warning",
					Reason:         "TestWarning",
					Message:        "Test warning message",
					FirstTimestamp: metav1.Now(),
					InvolvedObject: corev1.ObjectReference{
						Name:       "test-configmap",
						Namespace:  "default",
						Kind:       "ConfigMap",
						APIVersion: "v1",
						UID:        "test-configmap-uid",
					},
				},
			},
			reason:              "TestWarning",
			message:             "Test warning message",
			expectEventRecorded: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a fake client with existing events and field index for UID
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithIndex(&corev1.Event{}, "involvedObject.uid", func(obj client.Object) []string {
					event := obj.(*corev1.Event)
					return []string{string(event.InvolvedObject.UID)}
				}).
				Build()

			// Add existing events to the fake client
			for _, event := range tt.existingEvents {
				err := fakeClient.Create(context.Background(), &event)
				assert.NoError(t, err)
			}

			// Create mock event recorder
			mockRecorder := &MockEventRecorder{}

			// Create EventRecorder
			eventRecorder := &EventRecorder{
				recorder:       mockRecorder,
				metrics:        nil,
				controllerName: "test-controller",
				client:         fakeClient,
				scheme:         scheme,
			}

			// Create test object
			testObj := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-configmap",
					Namespace: "default",
					UID:       "test-configmap-uid",
				},
			}
			// Set the GVK explicitly
			testObj.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("ConfigMap"))

			// Set up mock expectations
			if tt.expectEventRecorded {
				mockRecorder.On("Event", testObj, "Warning", tt.reason, tt.message).Once()
			}

			// Record the warning event
			eventRecorder.RecordWarning(testObj, tt.reason, tt.message, tt.deduplicationEnabled)

			// Verify mock expectations
			mockRecorder.AssertExpectations(t)
		})
	}
}

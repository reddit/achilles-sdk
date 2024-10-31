package status_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/reddit/achilles-sdk-api/api"
	"github.com/reddit/achilles-sdk/pkg/status"
)

var mockGeneration int64 = 1

type conditionedResource struct {
	generation int64
	api.ConditionedStatus
}

func (r *conditionedResource) GetGeneration() int64 {
	return r.generation
}

func newConditionedResource(conditions []api.Condition) *conditionedResource {
	return &conditionedResource{
		ConditionedStatus: api.ConditionedStatus{
			Conditions: conditions,
		},
	}
}

func TestResourceReadyTrue(t *testing.T) {
	conditions := []api.Condition{
		{
			Type:   "TypeA",
			Status: corev1.ConditionTrue,
		},
		{
			Type:   api.TypeReady,
			Status: corev1.ConditionTrue,
			Reason: api.ReasonAvailable,
		},
	}

	actual := status.ResourceReady(newConditionedResource(conditions))

	if diff := cmp.Diff(actual, true); diff != "" {
		t.Errorf("Unexpected result for ResourceReady: \n%s", diff)
	}
}

func TestResourceReadyFalse(t *testing.T) {
	conditionsA := []api.Condition{
		{
			Type:   "TypeA",
			Status: corev1.ConditionTrue,
		},
		{
			Type:   api.TypeReady,
			Status: corev1.ConditionFalse,
			Reason: api.ReasonReconcileError,
		},
	}

	actualA := status.ResourceReady(newConditionedResource(conditionsA))
	if diff := cmp.Diff(actualA, false); diff != "" {
		t.Errorf("Unexpected result for ResourceReady: \n%s", diff)
	}

	// missing TypeReady, should return false
	conditionsB := []api.Condition{
		{
			Type:   "TypeA",
			Status: corev1.ConditionTrue,
		},
	}
	actualB := status.ResourceReady(newConditionedResource(conditionsB))
	if diff := cmp.Diff(actualB, false); diff != "" {
		t.Errorf("Unexpected result for ResourceReady: \n%s", diff)
	}
}

func TestNewReadyConditionSuccess(t *testing.T) {
	conditions := []api.Condition{
		{
			Type:   "TypeA",
			Status: corev1.ConditionTrue,
		},
		{
			Type:   "TypeB",
			Status: corev1.ConditionTrue,
		},
	}

	expected := api.Condition{
		Type:               api.TypeReady,
		Status:             corev1.ConditionTrue,
		Reason:             status.ReasonSuccess,
		Message:            status.ReadySuccessMessage,
		ObservedGeneration: mockGeneration,
	}

	actual := status.NewReadyCondition(mockGeneration, conditions...)
	// don't compare LastTransitionTime
	actual.LastTransitionTime = metav1.Time{}

	if diff := cmp.Diff(actual, expected); diff != "" {
		t.Errorf("Unexpected result for NewReadyCondition: \n%s", diff)
	}
}

func TestNewReadyConditionFailure(t *testing.T) {
	conditions := []api.Condition{
		{
			Type:               "TypeA",
			Status:             corev1.ConditionTrue,
			LastTransitionTime: metav1.Now(),
		},
		{
			Type:               "TypeB",
			Status:             corev1.ConditionUnknown,
			LastTransitionTime: metav1.Now(),
		},
		{
			Type:               "TypeC",
			Status:             corev1.ConditionFalse,
			LastTransitionTime: metav1.Now(),
		},
	}

	expected := api.Condition{
		Type:               api.TypeReady,
		Status:             corev1.ConditionFalse,
		Reason:             status.ReasonFailure,
		Message:            "Non-successful conditions: TypeB, TypeC",
		ObservedGeneration: mockGeneration,
	}

	actual := status.NewReadyCondition(mockGeneration, conditions...)
	// don't compare LastTransitionTime
	actual.LastTransitionTime = metav1.Time{}

	if diff := cmp.Diff(actual, expected); diff != "" {
		t.Errorf("Unexpected result for NewReadyCondition: \n%s", diff)
	}
}

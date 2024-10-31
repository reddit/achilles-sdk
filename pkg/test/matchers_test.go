package test

import (
	"fmt"
	"testing"

	"github.com/onsi/gomega/format"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/reddit/achilles-sdk-api/api"
)

type testConditionedStatus struct {
	api.ConditionedStatus
	Foo string
}

func TestMatchConditionedStatus_Match(t *testing.T) {
	expectedConditionedStatus := api.ConditionedStatus{
		Conditions: []api.Condition{
			{
				Type:               "ready",
				Status:             "status",
				LastTransitionTime: metav1.Now(),
				Reason:             "reason",
				Message:            "message",
				ObservedGeneration: 10, // Should be ignored
			},
			{
				Type:               "type",
				Status:             "status",
				LastTransitionTime: metav1.Now(),
				Reason:             "reason",
				Message:            "message",
			},
		},
	}
	actualConditionedStatus := api.ConditionedStatus{
		Conditions: []api.Condition{
			{
				Type:               "type",
				Status:             "status",
				LastTransitionTime: metav1.Now(),
				Reason:             "reason",
				Message:            "message",
				ObservedGeneration: 5, // Should be ignored
			},
			{
				Type:               "ready",
				Status:             "status",
				LastTransitionTime: metav1.Now(),
				Reason:             "reason",
				Message:            "message",
				ObservedGeneration: 2, // Should be ignored
			},
		},
	}

	matcher := conditionedStatusMatcher{expected: expectedConditionedStatus}
	match, err := matcher.Match(actualConditionedStatus)
	assert.NoError(t, err)
	assert.True(t, match, "Expected match to be true")
}

func TestMatchConditionedStatus_NoMatch(t *testing.T) {
	expectedConditionedStatus := api.ConditionedStatus{
		Conditions: []api.Condition{
			{
				Type:               "other",
				Status:             "status",
				LastTransitionTime: metav1.Now(),
				Reason:             "reason",
				Message:            "message",
				ObservedGeneration: 2,
			},
			{
				Type:               "type",
				Status:             "status",
				LastTransitionTime: metav1.Now(),
				Reason:             "reason",
				Message:            "message",
			},
		},
	}
	actualConditionedStatus := api.ConditionedStatus{
		Conditions: []api.Condition{
			{
				Type:               "type",
				Status:             "status",
				LastTransitionTime: metav1.Now(),
				Reason:             "reason",
				Message:            "message",
				ObservedGeneration: 5,
			},
			{
				Type:               "ready",
				Status:             "status",
				LastTransitionTime: metav1.Now(),
				Reason:             "reason",
				Message:            "message",
				ObservedGeneration: 2,
			},
		},
	}

	matcher := conditionedStatusMatcher{expected: expectedConditionedStatus}
	match, err := matcher.Match(actualConditionedStatus)
	assert.NoError(t, err)
	assert.False(t, match, "Expected match to be false")
}

func TestMatchConditionedStatusWithBadExpectedType(t *testing.T) {
	expectedStatus := "incorrect type"
	matcher := conditionedStatusMatcher{expected: expectedStatus}
	match, err := matcher.Match(testConditionedStatus{})
	expectedErr := fmt.Errorf("ConditionedStatusMatcher expects a api.ConditionedStatus. Got expected:\n%s", format.Object(expectedStatus, 1))

	assert.False(t, match, "Unexpected match")
	assert.EqualError(t, err, expectedErr.Error(), "Unexpected error")
}

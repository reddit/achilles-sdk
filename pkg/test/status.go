package test

//
// Test util functions for pkg/status.
//
import (
	"github.com/reddit/achilles-sdk-api/api"
	"github.com/reddit/achilles-sdk/pkg/status"
)

// NewReadyConditionForTest creates a new Ready condition for testing purposes.
// It sets the observed generation to 0.
func NewReadyConditionForTest(conditions ...api.Condition) api.Condition {
	return NewReadyConditionForTestWithObservedGeneration(0, conditions...)
}

// NewReadyConditionForTestWithObservedGeneration creates a new Ready condition for testing purposes.
// It sets the observed generation to the provided value.
func NewReadyConditionForTestWithObservedGeneration(obsGen int64, conditions ...api.Condition) api.Condition {
	return status.NewReadyCondition(obsGen, conditions...)
}

package status

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/reddit/achilles-sdk-api/api"
)

const (
	ReasonSuccess             = "ConditionsSuccessful"
	ReasonFailure             = "ConditionsFailed"
	ReadySuccessMessage       = "All conditions successful."
	readyFailureMessagePrefix = "Non-successful conditions: "
)

var (
	ManagedResourcesReadyType = api.ConditionType("ManagedResourcesReady")

	ManagedResourcesReadyCondition = api.Condition{
		Type:               ManagedResourcesReadyType,
		Status:             corev1.ConditionTrue,
		LastTransitionTime: metav1.Now(),
		Message:            "All managed resources ready.",
	}
)

// ResourceReady checks whether an Achilles resource has been successfully processed. It returns true if the
// resource's "Ready" condition is true and observed generation matches the current generation.
// The condition reason and message are not compared.
func ResourceReady(res api.Conditioned) bool {
	readyCondition := res.GetCondition(api.TypeReady)
	return readyCondition.Type == api.TypeReady &&
		readyCondition.Status == corev1.ConditionTrue &&
		readyCondition.ObservedGeneration == res.GetGeneration()
}

// NewReadyCondition returns an api.Condition of type "Ready" whose value is the conjunction
// of all provided conditions. Conditions in unknown status will result in a failed Ready condition.
// ObservedGeneration is the generation of the object when the condition was last observed.
func NewReadyCondition(observedGeneration int64, conditions ...api.Condition) api.Condition {
	var nonSuccessfulConditions []api.Condition

	status := corev1.ConditionTrue
	reason := ReasonSuccess

	for _, condition := range conditions {
		if condition.Status != corev1.ConditionTrue {
			status = corev1.ConditionFalse
			reason = ReasonFailure
			nonSuccessfulConditions = append(nonSuccessfulConditions, condition)
		}
	}

	return api.Condition{
		Type:               api.TypeReady,
		LastTransitionTime: metav1.Now(),
		Status:             status,
		Reason:             api.ConditionReason(reason),
		Message:            readyMessage(nonSuccessfulConditions),
		ObservedGeneration: observedGeneration,
	}
}

func NewUnreadyCondition(observedGeneration int64) api.Condition {
	return api.Condition{
		Type:               api.TypeReady,
		LastTransitionTime: metav1.Now(),
		Status:             corev1.ConditionFalse,
		ObservedGeneration: observedGeneration,
	}
}

// construct condition message by listing the failed conditions if any exist
func readyMessage(nonSuccessfulConditions []api.Condition) string {
	if len(nonSuccessfulConditions) == 0 {
		return ReadySuccessMessage
	}

	var failedConditionTypes []string
	for _, c := range nonSuccessfulConditions {
		failedConditionTypes = append(failedConditionTypes, string(c.Type))
	}

	return readyFailureMessagePrefix + strings.Join(failedConditionTypes, ", ")
}

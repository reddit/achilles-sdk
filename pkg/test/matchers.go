package test

import (
	"fmt"
	"reflect"
	"slices"
	"sort"

	"github.com/onsi/gomega/format"
	"github.com/onsi/gomega/gstruct"
	"github.com/onsi/gomega/matchers"
	"github.com/onsi/gomega/types"

	"github.com/reddit/achilles-sdk-api/api"
)

// Custom gomega matchers

var (
	conditionedStatusFieldName = reflect.TypeOf(api.ConditionedStatus{}).Name()
)

// MatchConditionedStatus uses custom equality logic for the ConditionedStatus field, and
// reflect.DeepEqual for all other fields.
func MatchConditionedStatus(expected interface{}) types.GomegaMatcher {
	fields := gstruct.Fields{}

	val := reflect.Indirect(reflect.ValueOf(expected))
	typ := val.Type()
	for i := 0; i < val.NumField(); i++ {
		fieldName := typ.Field(i).Name

		if fieldName == conditionedStatusFieldName {
			fields[fieldName] = &conditionedStatusMatcher{expected: val.Field(i).Interface()}
			continue
		}

		fields[fieldName] = &matchers.EqualMatcher{Expected: val.Field(i).Interface()}
	}

	return gstruct.MatchAllFields(fields)
}

// ConditionsEqual compares two api.Condition structs for equality.
// It mimics the behavior of api.Condition.Equal, but does not compare the ObservedGeneration field.
func ConditionsEqual(c, other api.Condition) bool {
	return c.Type == other.Type &&
		c.Status == other.Status &&
		c.Reason == other.Reason &&
		c.Message == other.Message
}

type conditionedStatusMatcher struct {
	expected interface{}
}

func (m *conditionedStatusMatcher) Match(actual interface{}) (success bool, err error) {
	expectedConditionedStatus, ok := m.expected.(api.ConditionedStatus)
	if !ok {
		return false, fmt.Errorf("ConditionedStatusMatcher expects a api.ConditionedStatus. Got expected:\n%s", format.Object(m.expected, 1))
	}

	actualConditionedStatus, ok := actual.(api.ConditionedStatus)
	if !ok {
		return false, fmt.Errorf("ConditionedStatusMatcher expects a api.ConditionedStatus. Got actual:\n%s", format.Object(actual, 1))
	}

	//
	// Mimics the behavior of api.ConditionedStatus.Equal, but does not compare the ObservedGeneration field.
	//

	if len(actualConditionedStatus.Conditions) != len(expectedConditionedStatus.Conditions) {
		return false, nil
	}

	ac := make([]api.Condition, len(actualConditionedStatus.Conditions))
	copy(ac, actualConditionedStatus.Conditions)

	ec := make([]api.Condition, len(expectedConditionedStatus.Conditions))
	copy(ec, expectedConditionedStatus.Conditions)

	sort.Slice(ac, func(i, j int) bool { return ac[i].Type < ac[j].Type })
	sort.Slice(ec, func(i, j int) bool { return ec[i].Type < ec[j].Type })

	return slices.EqualFunc(ac, ec, ConditionsEqual), nil
}

func (m *conditionedStatusMatcher) FailureMessage(actual interface{}) (message string) {
	return fmt.Sprintf("Expected\n\t%#v\nto equal \n\t%#v", actual, m.expected)
}

func (m *conditionedStatusMatcher) NegatedFailureMessage(actual interface{}) (message string) {
	return fmt.Sprintf("Expected\n\t%#v\nto not equal \n\t%#v", actual, m.expected)
}

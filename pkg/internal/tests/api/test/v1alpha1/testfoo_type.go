package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/reddit/achilles-sdk-api/api"
)

type TestFooSpec struct {
	TestField string `json:"testField,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:resource:categories={test,infrared}
//+kubebuilder:subresource:status

// TestFoo is a simple test resource; notably it properly sets the subresource status, unlike TestBar.
type TestFoo struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              TestFooSpec   `json:"spec,omitempty"`
	Status            TestFooStatus `json:"status,omitempty"`
}

// TestFooStatus describes the status of a TestFoo.
type TestFooStatus struct {
	// A field updated by the controller to match the Spec's test field on reconciliation.
	// Mutation to this field can be used to verify if the reconcile loop has run in tests.
	TestField string `json:"testField,omitempty"`

	// Status conditions for the TestFoo.
	api.ConditionedStatus `json:",inline"`

	// ResourceRefs is a list of all resources managed by this object.
	ResourceRefs []api.TypedObjectRef `json:"resourceRefs,omitempty"`
}

func (c *TestFoo) SetManagedResources(refs []api.TypedObjectRef) {
	c.Status.ResourceRefs = refs
}

func (c *TestFoo) GetManagedResources() []api.TypedObjectRef {
	return c.Status.ResourceRefs
}

func (t *TestFoo) GetConditions() []api.Condition {
	return t.Status.Conditions
}

func (t *TestFoo) SetConditions(c ...api.Condition) {
	t.Status.SetConditions(c...)
}

func (t *TestFoo) GetCondition(ct api.ConditionType) api.Condition {
	return t.Status.GetCondition(ct)
}

//+kubebuilder:object:root=true

// TestFooList contains a list of TestFoos
type TestFooList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []*TestFoo `json:"items"`
}

func init() {
	SchemeBuilder.Register(&TestFoo{}, &TestFooList{})
}

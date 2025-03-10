package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/reddit/achilles-sdk-api/api"
)

type TestBarSpec struct {
	TestField string `json:"testField,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:resource:categories={test,infrared}

// TestBar is a simple test resource; notably, it does not subresource the status field, unlike TestFoo.
type TestBar struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              TestBarSpec   `json:"spec,omitempty"`
	Status            TestBarStatus `json:"status,omitempty"`
}

// TestBarStatus describes the status of a TestBar.
type TestBarStatus struct {
	// A field updated by the controller to match the Spec's test field on reconciliation.
	// Mutation to this field can be used to verify if the reconcile loop has run in tests.
	TestField string `json:"testField,omitempty"`

	// Status conditions for the TestBar.
	api.ConditionedStatus `json:",inline"`

	// ResourceRefs is a list of all resources managed by this object.
	ResourceRefs []api.TypedObjectRef `json:"resourceRefs,omitempty"`
}

func (c *TestBar) SetManagedResources(refs []api.TypedObjectRef) {
	c.Status.ResourceRefs = refs
}

func (c *TestBar) GetManagedResources() []api.TypedObjectRef {
	return c.Status.ResourceRefs
}

func (t *TestBar) GetConditions() []api.Condition {
	return t.Status.Conditions
}

func (t *TestBar) SetConditions(c ...api.Condition) {
	t.Status.SetConditions(c...)
}

func (t *TestBar) GetCondition(ct api.ConditionType) api.Condition {
	return t.Status.GetCondition(ct)
}

//+kubebuilder:object:root=true

// TestBarList contains a list of TestBars
type TestBarList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []*TestBar `json:"items"`
}

func init() {
	SchemeBuilder.Register(&TestBar{}, &TestBarList{})
}

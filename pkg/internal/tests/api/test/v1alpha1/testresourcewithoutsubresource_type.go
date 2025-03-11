package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/reddit/achilles-sdk-api/api"
)

type TestResourceWithoutSubresourceSpec struct {
	TestField string `json:"testField,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:resource:categories={test,infrared}

// TestResourceWithoutSubresource is a simple test resource; notably, it does not subresource the status field.
type TestResourceWithoutSubresource struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              TestResourceWithoutSubresourceSpec   `json:"spec,omitempty"`
	Status            TestResourceWithoutSubresourceStatus `json:"status,omitempty"`
}

// TestResourceWithoutSubresourceStatus describes the status of a TestResourceWithoutSubresource.
type TestResourceWithoutSubresourceStatus struct {
	// A field updated by the controller to match the Spec's test field on reconciliation.
	// Mutation to this field can be used to verify if the reconcile loop has run in tests.
	TestField string `json:"testField,omitempty"`

	// Status conditions for the TestResourceWithoutSubresource.
	api.ConditionedStatus `json:",inline"`

	// ResourceRefs is a list of all resources managed by this object.
	ResourceRefs []api.TypedObjectRef `json:"resourceRefs,omitempty"`
}

func (c *TestResourceWithoutSubresource) SetManagedResources(refs []api.TypedObjectRef) {
	c.Status.ResourceRefs = refs
}

func (c *TestResourceWithoutSubresource) GetManagedResources() []api.TypedObjectRef {
	return c.Status.ResourceRefs
}

func (t *TestResourceWithoutSubresource) GetConditions() []api.Condition {
	return t.Status.Conditions
}

func (t *TestResourceWithoutSubresource) SetConditions(c ...api.Condition) {
	t.Status.SetConditions(c...)
}

func (t *TestResourceWithoutSubresource) GetCondition(ct api.ConditionType) api.Condition {
	return t.Status.GetCondition(ct)
}

//+kubebuilder:object:root=true

// TestResourceWithoutSubresourceList contains a list of TestResourceWithoutSubresource
type TestResourceWithoutSubresourceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []*TestResourceWithoutSubresource `json:"items"`
}

func init() {
	SchemeBuilder.Register(&TestResourceWithoutSubresource{}, &TestResourceWithoutSubresourceList{})
}

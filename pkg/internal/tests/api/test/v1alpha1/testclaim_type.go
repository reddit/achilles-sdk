package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/reddit/achilles-sdk-api/api"
)

type TestClaimSpec struct {
	ClaimedRef    *api.TypedObjectRef `json:"claimedRef,omitempty"`
	TestField     string              `json:"testField,omitempty"`
	ConfigMapName *string             `json:"configMapName,omitempty"`
	DontDelete    bool                `json:"dontDelete,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:resource:categories={test,infrared}
//+kubebuilder:subresource:status

type TestClaim struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              TestClaimSpec   `json:"spec,omitempty"`
	Status            TestClaimStatus `json:"status,omitempty"`
}

// TestClaimStatus describes the status of a TestClaim.
type TestClaimStatus struct {
	// A field updated by the controller to match the Spec's test field on reconciliation.
	// Mutation to this field can be used to verify if the reconcile loop has run in tests.
	TestField string `json:"testField,omitempty"`

	// Status conditions for the TestClaim.
	api.ConditionedStatus `json:",inline"`

	// ResourceRefs is a list of all resources managed by this object.
	ResourceRefs []api.TypedObjectRef `json:"resourceRefs,omitempty"`

	ConfigMapName *string `json:"configMapName,omitempty"`
}

func (c *TestClaim) SetManagedResources(refs []api.TypedObjectRef) {
	c.Status.ResourceRefs = refs
}

func (c *TestClaim) GetManagedResources() []api.TypedObjectRef {
	return c.Status.ResourceRefs
}

func (t *TestClaim) GetConditions() []api.Condition {
	return t.Status.Conditions
}

func (t *TestClaim) SetConditions(c ...api.Condition) {
	t.Status.SetConditions(c...)
}

func (t *TestClaim) GetCondition(ct api.ConditionType) api.Condition {
	return t.Status.GetCondition(ct)
}

func (t *TestClaim) GetClaimedRef() *api.TypedObjectRef {
	return t.Spec.ClaimedRef
}

func (t *TestClaim) SetClaimedRef(ref *api.TypedObjectRef) {
	t.Spec.ClaimedRef = ref
}

//+kubebuilder:object:root=true

// TestClaimList contains a list of TestClaims
type TestClaimList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []*TestClaim `json:"items"`
}

func init() {
	SchemeBuilder.Register(&TestClaim{}, &TestClaimList{})
}

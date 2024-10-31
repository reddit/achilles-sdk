package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/reddit/achilles-sdk-api/api"
)

type TestClaimedSpec struct {
	ClaimRef   *api.TypedObjectRef `json:"claimRef,omitempty"`
	Success    bool                `json:"success,omitempty"`
	DontDelete bool                `json:"dontDelete,omitempty"`
}

type TestClaimedStatus struct {
	api.ConditionedStatus `json:",inline"`

	Resources []api.TypedObjectRef `json:"resources,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:resource:scope=Cluster,categories={test,infrared}
//+kubebuilder:subresource:status

type TestClaimed struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              TestClaimedSpec   `json:"spec,omitempty"`
	Status            TestClaimedStatus `json:"status,omitempty"`
}

func (t *TestClaimed) GetClaimRef() *api.TypedObjectRef {
	return t.Spec.ClaimRef
}

func (t *TestClaimed) SetClaimRef(ref *api.TypedObjectRef) {
	t.Spec.ClaimRef = ref
}

func (t *TestClaimed) SetManagedResources(refs []api.TypedObjectRef) {
	t.Status.Resources = refs
}

func (t *TestClaimed) GetManagedResources() []api.TypedObjectRef {
	return t.Status.Resources
}

func (t *TestClaimed) GetConditions() []api.Condition {
	return t.Status.Conditions
}

func (t *TestClaimed) SetConditions(c ...api.Condition) {
	t.Status.SetConditions(c...)
}

func (t *TestClaimed) GetCondition(ct api.ConditionType) api.Condition {
	return t.Status.GetCondition(ct)
}

//+kubebuilder:object:root=true

// TestClaimedList contains a list of TestClaimeds
type TestClaimedList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []*TestClaimed `json:"items"`
}

func init() {
	SchemeBuilder.Register(&TestClaimed{}, &TestClaimedList{})
}

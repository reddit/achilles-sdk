package test

import (
	"github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/reddit/achilles-sdk/pkg/meta"
)

// AssertRedditLabels asserts expected Reddit labels on the provided object.
func AssertRedditLabels(o client.Object, controllerName string) {
	AssertRedditLabelsWithGomega(o, controllerName, gomega.Default)
}

// AssertRedditLabelsWithGomega is the same as AssertRedditLabels but accepts a Gomega instance,
// useful for using inside Eventually/Consistently blocks.
func AssertRedditLabelsWithGomega(o client.Object, controllerName string, g gomega.Gomega) {
	for k, v := range meta.RedditLabels(controllerName) {
		g.ExpectWithOffset(1, o.GetLabels()).To(gomega.HaveKeyWithValue(k, v))
	}
}

// AssertOwnerRef asserts that the provided object has an owner reference that references the provided owner.
func AssertOwnerRef(o client.Object, owner client.Object, scheme *runtime.Scheme) {
	AssertOwnerRefWithGomega(o, owner, scheme, gomega.Default)
}

// AssertOwnerRefWithGomega is the same as AssertOwnerRef but accepts a Gomega instance,
// useful for using inside Eventually/Consistently blocks.
func AssertOwnerRefWithGomega(o client.Object, owner client.Object, scheme *runtime.Scheme, g gomega.Gomega) {
	gvk := meta.MustGVKForObject(owner, scheme)
	ref := metav1.OwnerReference{
		APIVersion:         gvk.GroupVersion().String(),
		Kind:               gvk.Kind,
		Name:               owner.GetName(),
		UID:                owner.GetUID(),
		BlockOwnerDeletion: ptr.To(true),
		Controller:         ptr.To(true),
	}

	g.ExpectWithOffset(1, o.GetOwnerReferences()).To(gomega.ContainElement(ref))
}

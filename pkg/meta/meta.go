package meta

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	zaputil "sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/reddit/achilles-sdk-api/api"
)

// MustGVKForObject returns schema.GroupVersionKind for the given object using the provided runtime.Scheme,
// will panic if not registered in scheme
func MustGVKForObject(obj client.Object, scheme *runtime.Scheme) schema.GroupVersionKind {
	gvk, err := apiutil.GVKForObject(obj, scheme)
	if err != nil {
		zaputil.NewRaw().Panic(fmt.Sprintf("GVK not registered with runtime scheme: %v", err))
	}
	return gvk
}

// MustTypedObjectRefFromObject returns *api.TypedObjectRef with GVK metadata provided from the
// provided runtime.Scheme, but panics if an error occurs.
func MustTypedObjectRefFromObject(obj client.Object, scheme *runtime.Scheme) *api.TypedObjectRef {
	typedObj, err := TypedObjectRefFromObject(obj, scheme)
	if err != nil {
		zaputil.NewRaw().Panic(fmt.Sprintf("GVK not registered with runtime scheme: %v", err))
	}
	return typedObj
}

// TypedObjectRefFromObject returns *api.TypedObjectRef with GVK metadata provided from the provided scheme
func TypedObjectRefFromObject(obj client.Object, scheme *runtime.Scheme) (*api.TypedObjectRef, error) {
	gvk, err := apiutil.GVKForObject(obj, scheme)
	if err != nil {
		return nil, err
	}
	return &api.TypedObjectRef{
		Group:     gvk.Group,
		Version:   gvk.Version,
		Kind:      gvk.Kind,
		Name:      obj.GetName(),
		Namespace: obj.GetNamespace(),
	}, nil
}

// NewObjectForGVK returns a new empty client.Object a given GroupVersionKind.
func NewObjectForGVK(scheme *runtime.Scheme, gvk schema.GroupVersionKind) (client.Object, error) {
	obj, err := scheme.New(gvk)
	if err != nil {
		return nil, fmt.Errorf("constructing new %s: %w", gvk, err)
	}

	clientObj, ok := obj.(client.Object)
	if !ok {
		return nil, fmt.Errorf("%T does not implement client.Object", obj)
	}

	return clientObj, nil
}

// WasDeleted returns true if the given object has been marked for deletion.Originally from
func WasDeleted(o metav1.Object) bool {
	return !o.GetDeletionTimestamp().IsZero()
}

// WasCreated returns true if the supplied object was created in the API server.
func WasCreated(o metav1.Object) bool {
	// This looks a little different from WasDeleted because DeletionTimestamp
	// returns a reference while CreationTimestamp returns a value.
	t := o.GetCreationTimestamp()
	return !t.IsZero()
}

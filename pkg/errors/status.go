package errors

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/reddit/achilles-sdk/pkg/meta"
)

// ObjectStatusNotReady occurs if object is not in a ready state as observed from its status
type ObjectStatusNotReady struct {
	obj    client.Object
	scheme *runtime.Scheme
}

func NewObjectStatusNotReady(obj client.Object, scheme *runtime.Scheme) *ObjectStatusNotReady {
	return &ObjectStatusNotReady{
		obj:    obj,
		scheme: scheme,
	}
}

func (e *ObjectStatusNotReady) Error() string {
	typedObj := meta.MustTypedObjectRefFromObject(e.obj, e.scheme)
	return fmt.Sprintf("%q %q: object status not ready", typedObj.GroupVersionKind(), typedObj.ObjectKey())
}

package types

import (
	"fmt"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/reddit/achilles-sdk-api/api"
	"github.com/reddit/achilles-sdk/pkg/io"
	"github.com/reddit/achilles-sdk/pkg/meta"
	"github.com/reddit/achilles-sdk/pkg/sets"
)

// OutputSet represents a set of objects that should be applied (with optional client apply options) to the server
// and that should be deleted from the server.
type OutputSet struct {
	scheme *runtime.Scheme

	// tracks objects applied by the caller
	applied *sets.ObjectSet
	// tracks objects explicitly deleted by the caller
	deleted *sets.ObjectSet

	applyOpts map[string][]io.ApplyOption
}

// OutputObject is a tuple of an object and an optional list of client apply options.
type OutputObject struct {
	Object    client.Object
	ApplyOpts []io.ApplyOption
}

// NewOutputSet returns a new OutputSet with a given scheme and objects.
func NewOutputSet(scheme *runtime.Scheme) *OutputSet {
	set := &OutputSet{
		applied:   sets.NewObjectSet(scheme),
		deleted:   sets.NewObjectSet(scheme),
		applyOpts: map[string][]io.ApplyOption{},
		scheme:    scheme,
	}
	return set
}

// Apply signals creation or update of an object to the server, with optional client apply options.
func (s *OutputSet) Apply(o client.Object, applyOpts ...io.ApplyOption) {
	s.applied.Insert(o)
	s.applyOpts[s.key(o)] = applyOpts
}

// ApplyAll is equivalent to calling Apply(obj) for all supplied objects.
func (s *OutputSet) ApplyAll(objs ...client.Object) {
	for _, o := range objs {
		s.applied.Insert(o)
	}
}

// GetApplied returns the set of objects to be applied.
func (s *OutputSet) GetApplied() *sets.ObjectSet {
	return s.applied
}

// GetDeleted returns the set of objects to be deleted.
func (s *OutputSet) GetDeleted() *sets.ObjectSet {
	return s.deleted
}

// Delete signals deletion of an object from the server.
func (s *OutputSet) Delete(o client.Object) {
	// delete object from applied set
	s.applied.Delete(o)
	// delete object from apply opts
	delete(s.applyOpts, s.key(o))
	// insert object into deleted set
	s.deleted.Insert(o)
}

// DeleteAll is equivalent to calling Delete(obj) for all supplied objects.
func (s *OutputSet) DeleteAll(objs ...client.Object) {
	for _, o := range objs {
		s.Delete(o)
	}
}

// DeleteByRef is the same as Delete, but takes an api.TypedObjectRef instead of an object.
func (s *OutputSet) DeleteByRef(typedObjRef api.TypedObjectRef) {
	apiVersion, kind := typedObjRef.GroupVersionKind().ToAPIVersionAndKind()
	objMeta := &v1.PartialObjectMetadata{
		TypeMeta: v1.TypeMeta{
			APIVersion: apiVersion,
			Kind:       kind,
		},
		ObjectMeta: v1.ObjectMeta{
			Name:      typedObjRef.Name,
			Namespace: typedObjRef.Namespace,
		},
	}
	s.Delete(objMeta)
}

// ListAppliedOutputs lists all objects from the output set along with their associated apply options.
func (s *OutputSet) ListAppliedOutputs() []OutputObject {
	var outputs []OutputObject
	for _, o := range s.applied.List() {
		k := s.key(o)
		outputs = append(outputs, OutputObject{
			Object:    o,
			ApplyOpts: s.applyOpts[k],
		})
	}

	return outputs
}

// ListApplied returns a slice of all objects to be applied against the server.
func (s *OutputSet) ListApplied() []client.Object {
	return s.applied.List()
}

// ListDeleted returns a slice of all objects to be deleted from the server.
func (s *OutputSet) ListDeleted() []client.Object {
	return s.deleted.List()
}

// return a key using the object's gvk, name/namespace
func (s *OutputSet) key(o client.Object) string {
	gvk := meta.MustGVKForObject(o, s.scheme)
	key := client.ObjectKeyFromObject(o)
	return keyFunc(gvk, key)
}

// key all objects on GVK + name/namespace
func keyFunc(gvk schema.GroupVersionKind, key client.ObjectKey) string {
	return fmt.Sprintf("%s:%s", gvk, key)
}

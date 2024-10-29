package sets

import (
	"fmt"
	"sort"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/reddit/achilles-sdk-api/api"
	"github.com/reddit/achilles-sdk/pkg/meta"
)

type setMap = map[string]client.Object

/*
ObjectSet provides a set data structure for client.Objects keyed on (group, version, kind, name, namespace).
Equality is defined in terms of equality of keys, not equality of object values.

The set operations union and intersection take their object values
from the receiver set (e.g. setA.Union(setB) will be the object values of those in setA).
*/
type ObjectSet struct {
	scheme *runtime.Scheme
	set    setMap
}

// NewObjectSet returns a new ObjectSet with a given scheme and objects.
func NewObjectSet(scheme *runtime.Scheme, objects ...client.Object) *ObjectSet {
	set := &ObjectSet{
		scheme: scheme,
		set:    make(setMap, len(objects)),
	}
	set.Insert(objects...)
	return set
}

// GetByRef gets an object from the set for a given TypedObjectRef. Returns nil if the object cannot be found.
func (s *ObjectSet) GetByRef(ref api.TypedObjectRef) client.Object {
	gvk := ref.GroupVersionKind()
	objectKey := ref.ObjectKey()
	key := keyFunc(gvk, objectKey)
	// will be nil on miss.
	return s.set[key]
}

// DeleteByRef deletes an object from the set for a given TypedObjectRef
func (s *ObjectSet) DeleteByRef(ref api.TypedObjectRef) {
	gvk := ref.GroupVersionKind()
	objectKey := ref.ObjectKey()
	key := keyFunc(gvk, objectKey)
	delete(s.set, key)
}

// Get returns an object from the set with the same type, name, and namespace as the provided object. Returns
// nil if the object cannot be found.
func Get[T client.Object](set *ObjectSet, obj T) T {
	o := set.GetByRef(*meta.MustTypedObjectRefFromObject(obj, set.scheme))
	// NOTE: needed to guard against typed nil errors (i.e. "panic interface conversion interface is nil")
	if o == nil {
		var empty T
		return empty
	}
	return o.(T)
}

// Insert adds objects to the set.
func (s *ObjectSet) Insert(objects ...client.Object) {
	for _, item := range objects {
		s.set[s.key(item)] = item
	}
}

// Delete removes all objects from the set.
func (s *ObjectSet) Delete(objects ...client.Object) {
	for _, item := range objects {
		delete(s.set, s.key(item))
	}
}

// Has returns true if and only if item is contained in the set.
func (s *ObjectSet) Has(item client.Object) bool {
	_, contained := s.set[s.key(item)]
	return contained
}

// HasAll returns true if and only if all objects are contained in the set.
func (s *ObjectSet) HasAll(objects ...client.Object) bool {
	for _, item := range objects {
		if !s.Has(item) {
			return false
		}
	}
	return true
}

// HasAny returns true if any objects are contained in the set.
func (s *ObjectSet) HasAny(objects ...client.Object) bool {
	for _, item := range objects {
		if s.Has(item) {
			return true
		}
	}
	return false
}

// Difference returns a set of objects that are not in s2
// For example:
// s1 = {a1, a2, a3}
// s2 = {a1, a2, a4, a5}
// s1.Difference(s2) = {a3}
// s2.Difference(s1) = {a4, a5}
func (s *ObjectSet) Difference(other *ObjectSet) *ObjectSet {
	result := NewObjectSet(s.scheme)
	for _, obj := range s.set {
		if !other.Has(obj) {
			result.Insert(obj)
		}
	}
	return result
}

// Union returns a new set which includes objects in either s1 or s2.
// If an object key exists in both sets, object value will be that of s1.
// For example:
// s1 = {a1, a2}
// s2 = {a3, a4}
// s1.Union(s2) = {a1, a2, a3, a4}
// s2.Union(s1) = {a1, a2, a3, a4}
func (s *ObjectSet) Union(other *ObjectSet) *ObjectSet {
	result := NewObjectSet(s.scheme)
	for _, obj := range other.set {
		result.Insert(obj)
	}
	for _, obj := range s.set {
		result.Insert(obj)
	}
	return result
}

// Intersection returns a new set which includes the item in BOTH s1 and s2.
// The elements of the returned set are sourced from the receiver.
// For example:
// s1 = {a1, a2}
// s2 = {a2, a3}
// s1.Intersection(s2) = {a2}
func (s *ObjectSet) Intersection(other *ObjectSet) *ObjectSet {
	var curr, next *ObjectSet
	result := NewObjectSet(s.scheme)
	if len(s.set) < len(other.set) {
		curr = s
		next = other
	} else {
		curr = other
		next = s
	}
	for _, obj := range curr.set {
		if next.Has(obj) {
			// grab element from s1
			result.Insert(s.set[s.key(obj)])
		}
	}
	return result
}

// IsSuperset returns true if and only if s1 is a superset of s2.
func (s *ObjectSet) IsSuperset(other *ObjectSet) bool {
	for _, obj := range other.set {
		if !s.Has(obj) {
			return false
		}
	}
	return true
}

// Equal returns true if and only if s1 is equal (as a set) to s2.
// Two sets are equal if their membership is identical.
// (In practice, this means same elements, order doesn't matter)
func (s *ObjectSet) Equal(other *ObjectSet) bool {
	return len(s.set) == len(other.set) && s.IsSuperset(other)
}

type pair struct {
	key string
	val client.Object
}
type sortablePairs []pair

func (s sortablePairs) Len() int           { return len(s) }
func (s sortablePairs) Less(i, j int) bool { return s[i].key < s[j].key }
func (s sortablePairs) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }

// List returns the contents of the set in stable order.
func (s *ObjectSet) List() []client.Object {
	pairs := make(sortablePairs, 0, len(s.set))
	for key, obj := range s.set {
		pairs = append(pairs, pair{key, obj})
	}
	sort.Sort(pairs)

	res := make([]client.Object, 0, len(pairs))
	for _, p := range pairs {
		res = append(res, p.val)
	}
	return res
}

// Len returns the size of the set.
func (s *ObjectSet) Len() int {
	return len(s.set)
}

// DeepCopy returns a new ObjectSet containing copies of all objects in this set.
func (s *ObjectSet) DeepCopy() *ObjectSet {
	objs := make([]client.Object, len(s.set))
	for i, o := range s.List() {
		objs[i] = o.DeepCopyObject().(client.Object)
	}
	return NewObjectSet(s.scheme, objs...)
}

// return a key using the object's gvk, name/namespace
func (s *ObjectSet) key(o client.Object) string {
	gvk := meta.MustGVKForObject(o, s.scheme)
	key := client.ObjectKeyFromObject(o)
	return keyFunc(gvk, key)
}

func keyFunc(gvk schema.GroupVersionKind, key client.ObjectKey) string {
	return fmt.Sprintf("%s:%s", gvk, key)
}

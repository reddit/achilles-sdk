package sets

import (
	"golang.org/x/exp/slices"
)

/*
ComparableSet provides a set data structure for `comparable` types.
*/
type ComparableSet[K comparable] struct {
	set map[K]struct{}
}

// NewComparableSet returns a new ComparableSet with elements of type K.
func NewComparableSet[K comparable](items ...K) *ComparableSet[K] {
	set := &ComparableSet[K]{
		set: make(map[K]struct{}, len(items)),
	}
	set.Insert(items...)
	return set
}

// Insert adds items to the set.
func (s *ComparableSet[K]) Insert(items ...K) {
	for _, item := range items {
		s.set[item] = struct{}{}
	}
}

// Delete removes all items from the set.
func (s *ComparableSet[K]) Delete(items ...K) {
	for _, item := range items {
		delete(s.set, item)
	}
}

// Has returns true if and only if item is contained in the set.
func (s *ComparableSet[K]) Has(item K) bool {
	_, contained := s.set[item]
	return contained
}

// HasAll returns true if and only if all items are contained in the set.
func (s *ComparableSet[K]) HasAll(items ...K) bool {
	for _, item := range items {
		if !s.Has(item) {
			return false
		}
	}
	return true
}

// HasAny returns true if any items are contained in the set.
func (s *ComparableSet[K]) HasAny(items ...K) bool {
	for _, item := range items {
		if s.Has(item) {
			return true
		}
	}
	return false
}

// Difference returns a set of items that are not in s2
// For example:
// s1 = {a1, a2, a3}
// s2 = {a1, a2, a4, a5}
// s1.Difference(s2) = {a3}
// s2.Difference(s1) = {a4, a5}
func (s *ComparableSet[K]) Difference(other *ComparableSet[K]) *ComparableSet[K] {
	result := NewComparableSet[K]()
	for item := range s.set {
		if !other.Has(item) {
			result.Insert(item)
		}
	}
	return result
}

// Union returns a new set which includes items in either s1 or s2.
// If an object key exists in both sets, object value will be that of s1.
// For example:
// s1 = {a1, a2}
// s2 = {a3, a4}
// s1.Union(s2) = {a1, a2, a3, a4}
// s2.Union(s1) = {a1, a2, a3, a4}
func (s *ComparableSet[K]) Union(other *ComparableSet[K]) *ComparableSet[K] {
	result := NewComparableSet[K]()
	for item := range other.set {
		result.Insert(item)
	}
	for item := range s.set {
		result.Insert(item)
	}
	return result
}

// Intersection returns a new set which includes the item in BOTH s1 and s2.
// The elements of the returned set are sourced from the receiver.
// For example:
// s1 = {a1, a2}
// s2 = {a2, a3}
// s1.Intersection(s2) = {a2}
func (s *ComparableSet[K]) Intersection(other *ComparableSet[K]) *ComparableSet[K] {
	var curr, next *ComparableSet[K]
	result := NewComparableSet[K]()
	if len(s.set) < len(other.set) {
		curr = s
		next = other
	} else {
		curr = other
		next = s
	}
	for item := range curr.set {
		if next.Has(item) {
			// grab element from s1
			result.Insert(item)
		}
	}
	return result
}

// IsSuperset returns true if and only if s1 is a superset of s2.
func (s *ComparableSet[K]) IsSuperset(other *ComparableSet[K]) bool {
	for item := range other.set {
		if !s.Has(item) {
			return false
		}
	}
	return true
}

// Equal returns true if and only if s1 is equal (as a set) to s2.
// Two sets are equal if their membership is identical.
// (In practice, this means same elements, order doesn't matter)
func (s *ComparableSet[K]) Equal(other *ComparableSet[K]) bool {
	return len(s.set) == len(other.set) && s.IsSuperset(other)
}

// List returns the contents of the set in sorted order determined by the specified less func.
func (s *ComparableSet[K]) List(less func(a, b K) int) []K {
	var ks []K
	for k := range s.set {
		ks = append(ks, k)
	}

	slices.SortFunc(ks, less)

	return ks
}

// UnorderedList returns the contents of the set as a slice whose order is undefined.
func (s *ComparableSet[K]) UnorderedList() []K {
	var ks []K
	for k := range s.set {
		ks = append(ks, k)
	}

	return ks
}

// Len returns the size of the set.
func (s *ComparableSet[K]) Len() int {
	return len(s.set)
}

// DeepCopy returns a new ComparableSet[K] containing copies of all objects in this set.
func (s *ComparableSet[K]) DeepCopy() *ComparableSet[K] {
	items := make([]K, len(s.set))
	for i, item := range s.UnorderedList() {
		items[i] = item
	}
	return NewComparableSet(items...)
}

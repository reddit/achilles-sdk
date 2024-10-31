package sets

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

type testStruct struct {
	Field1 string
	Field2 int
}

func lessTestStruct(a, b testStruct) int {
	if a.Field1 == b.Field1 {
		return a.Field2 - b.Field2
	}
	return strings.Compare(a.Field1, b.Field1)
}

var (
	aComparable = testStruct{
		Field1: "a",
		Field2: 1,
	}
	bComparable = testStruct{
		Field1: "a",
		Field2: 2,
	}
	cComparable = testStruct{
		Field1: "c",
		Field2: 1,
	}
	dComparable = testStruct{
		Field1: "d",
		Field2: 5,
	}
	eComparable = testStruct{
		Field1: "d",
		Field2: 6,
	}
	fComparable = testStruct{
		Field1: "f",
		Field2: 1,
	}
)

func TestNewComparableSet(t *testing.T) {
	s := NewComparableSet(aComparable, bComparable, cComparable)
	if s.Len() != 3 {
		t.Errorf("Expected len=3: %d", s.Len())
	}
	if !s.Has(aComparable) || !s.Has(bComparable) || !s.Has(cComparable) {
		t.Errorf("Unexpected contents: %#v", s)
	}
}

func TestComparableSet(t *testing.T) {
	s := NewComparableSet[testStruct]()
	s2 := NewComparableSet[testStruct]()
	if s.Len() != 0 {
		t.Errorf("Expected len=0: %d", s.Len())
	}
	s.Insert(aComparable, bComparable)
	if s.Len() != 2 {
		t.Errorf("Expected len=2: %d", s.Len())
	}
	s.Insert(cComparable)
	if s.Has(dComparable) {
		t.Errorf("Unexpected contents: %#v", s)
	}
	if !s.Has(aComparable) {
		t.Errorf("Missing contents: %#v", s)
	}
	s.Delete(aComparable)
	if s.Has(aComparable) {
		t.Errorf("Unexpected contents: %#v", s)
	}
	s.Insert(aComparable)
	if s.HasAll(aComparable, bComparable, dComparable) {
		t.Errorf("Unexpected contents: %#v", s)
	}
	if !s.HasAll(aComparable, bComparable) {
		t.Errorf("Missing contents: %#v", s)
	}
	s2.Insert(aComparable, bComparable, dComparable)
	if s.IsSuperset(s2) {
		t.Errorf("Unexpected contents: %#v", s)
	}
	s2.Delete(dComparable)
	if !s.IsSuperset(s2) {
		t.Errorf("Missing contents: %#v", s)
	}
}

func TestComparableSet_DeleteMultiples(t *testing.T) {
	s := NewComparableSet[testStruct]()
	s.Insert(aComparable, bComparable, cComparable)
	if s.Len() != 3 {
		t.Errorf("Expected len=3: %d", s.Len())
	}

	s.Delete(aComparable, cComparable)
	if s.Len() != 1 {
		t.Errorf("Expected len=1: %d", s.Len())
	}
	if s.Has(aComparable) {
		t.Errorf("Unexpected contents: %#v", s)
	}
	if s.Has(cComparable) {
		t.Errorf("Unexpected contents: %#v", s)
	}
	if !s.Has(bComparable) {
		t.Errorf("Missing contents: %#v", s)
	}
}

func TestComparableSet_List(t *testing.T) {
	s := NewComparableSet(dComparable, cComparable, bComparable, aComparable)
	// return in sorted order
	if diff := cmp.Diff(s.List(lessTestStruct), []testStruct{aComparable, bComparable, cComparable, dComparable}); diff != "" {
		t.Errorf("List gave unexpected results:\n%s", diff)
	}
}

func TestComparableSet_UnorderedList(t *testing.T) {
	s := NewComparableSet(dComparable, cComparable, bComparable, aComparable)
	// return in any order
	expected := []testStruct{aComparable, bComparable, cComparable, dComparable}
	if diff := cmp.Diff(s.List(lessTestStruct), expected, cmpopts.SortSlices(func(a, b testStruct) bool {
		return lessTestStruct(a, b) < 0
	})); diff != "" {
		t.Errorf("UnorderedList gave unexpected results:\n%s", diff)
	}
}

func TestComparableSet_Difference(t *testing.T) {
	s1 := NewComparableSet(aComparable, bComparable, cComparable)
	s2 := NewComparableSet(aComparable, bComparable, dComparable, eComparable)
	d1 := s1.Difference(s2)
	d2 := s2.Difference(s1)
	if d1.Len() != 1 {
		t.Errorf("Expected len=1: %d", d1.Len())
	}
	if !d1.Has(cComparable) {
		t.Errorf("Unexpected contents: %#v", d1.List(lessTestStruct))
	}
	if d2.Len() != 2 {
		t.Errorf("Expected len=2: %d", d2.Len())
	}
	if !d2.Has(dComparable) || !d2.Has(eComparable) {
		t.Errorf("Unexpected contents: %#v", d2.List(lessTestStruct))
	}
}

func TestComparableSet_HasAny(t *testing.T) {
	s := NewComparableSet(aComparable, bComparable, cComparable)

	if !s.HasAny(aComparable, dComparable) {
		t.Errorf("expected true, got false")
	}

	if s.HasAny(dComparable, eComparable) {
		t.Errorf("expected false, got true")
	}
}

func TestComparableSet_Equals(t *testing.T) {
	// Simple case (order doesn't matter)
	s1 := NewComparableSet(aComparable, bComparable)
	s2 := NewComparableSet(bComparable, aComparable)
	if !s1.Equal(s2) {
		t.Errorf("Expected to be equal: %v vs %v", aComparable, bComparable)
	}

	// It is a set; duplicates are ignored
	s2 = NewComparableSet(bComparable, bComparable, aComparable)
	if !s1.Equal(s2) {
		t.Errorf("Expected to be equal: %v vs %v", aComparable, bComparable)
	}

	// Edge cases around empty sets / empty strings
	s1 = NewComparableSet[testStruct]()
	s2 = NewComparableSet[testStruct]()
	if !s1.Equal(s2) {
		t.Errorf("Expected to be equal: %v vs %v", aComparable, bComparable)
	}

	s2 = NewComparableSet(aComparable, bComparable, cComparable)
	if s1.Equal(s2) {
		t.Errorf("Expected to be not-equal: %v vs %v", aComparable, bComparable)
	}

	// Check for equality after mutation
	s1 = NewComparableSet[testStruct]()
	s1.Insert(aComparable)
	if s1.Equal(s2) {
		t.Errorf("Expected to be not-equal: %v vs %v", aComparable, bComparable)
	}

	s1.Insert(bComparable)
	if s1.Equal(s2) {
		t.Errorf("Expected to be not-equal: %v vs %v", aComparable, bComparable)
	}
}

func TestComparableSet_Union(t *testing.T) {
	tests := []struct {
		name     string
		s1       *ComparableSet[testStruct]
		s2       *ComparableSet[testStruct]
		expected *ComparableSet[testStruct]
	}{
		{
			name:     "union",
			s1:       NewComparableSet(aComparable, bComparable, cComparable, dComparable),
			s2:       NewComparableSet(cComparable, dComparable, eComparable, fComparable),
			expected: NewComparableSet(aComparable, bComparable, cComparable, dComparable, eComparable, fComparable),
		},
		{
			name:     "disjoint",
			s1:       NewComparableSet(aComparable, bComparable, cComparable, dComparable),
			s2:       NewComparableSet[testStruct](),
			expected: NewComparableSet(aComparable, bComparable, cComparable, dComparable),
		},
		{
			name:     "identity",
			s1:       NewComparableSet(aComparable, bComparable, cComparable, dComparable),
			s2:       NewComparableSet(dComparable, cComparable, bComparable, aComparable),
			expected: NewComparableSet(aComparable, bComparable, cComparable, dComparable),
		},
		{
			name:     "empty",
			s1:       NewComparableSet[testStruct](),
			s2:       NewComparableSet[testStruct](),
			expected: NewComparableSet[testStruct](),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// union is commutative
			got1 := tc.s1.Union(tc.s2)
			got2 := tc.s2.Union(tc.s1)

			if !got1.Equal(got2) {
				t.Errorf("commutatitivy violated. diff=\n%s", cmp.Diff(got1.set, got2.set))
			}

			if diff := cmp.Diff(got1.set, tc.expected.set); diff != "" {
				t.Errorf("result differs from expected: (-got +want):\n%s", diff)
			}
		})
	}
}

func TestComparableSet_Intersection(t *testing.T) {
	cases := []struct {
		name     string
		s1       *ComparableSet[testStruct]
		s2       *ComparableSet[testStruct]
		expected *ComparableSet[testStruct]
	}{
		{
			name:     "intersects",
			s1:       NewComparableSet(aComparable, bComparable, cComparable, dComparable),
			s2:       NewComparableSet(cComparable, dComparable, eComparable, fComparable),
			expected: NewComparableSet(cComparable, dComparable),
		},
		{
			name:     "identity",
			s1:       NewComparableSet(aComparable, bComparable, cComparable, dComparable),
			s2:       NewComparableSet(aComparable, bComparable, cComparable, dComparable),
			expected: NewComparableSet(aComparable, bComparable, cComparable, dComparable),
		},
		{
			name:     "disjoint",
			s1:       NewComparableSet(aComparable, bComparable, cComparable, dComparable),
			s2:       NewComparableSet[testStruct](),
			expected: NewComparableSet[testStruct](),
		},
		{
			name:     "empty",
			s1:       NewComparableSet[testStruct](),
			s2:       NewComparableSet[testStruct](),
			expected: NewComparableSet[testStruct](),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// intersection is commutative
			got1 := tc.s1.Intersection(tc.s2)
			got2 := tc.s2.Intersection(tc.s1)

			if !got1.Equal(got2) {
				t.Errorf("commutatitivy violated. diff=\n%s", cmp.Diff(got1.set, got2.set))
			}

			if diff := cmp.Diff(got1.set, tc.expected.set); diff != "" {
				t.Errorf("result differs from expected: (-got +want):\n%s", diff)
			}
		})
	}
}

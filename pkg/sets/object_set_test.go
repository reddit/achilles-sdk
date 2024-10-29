package sets

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	internalscheme "github.com/reddit/achilles-sdk/pkg/internal/scheme"
	"github.com/reddit/achilles-sdk/pkg/meta"
)

var (
	scheme = internalscheme.MustNewScheme()
	a      = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "a",
			Namespace: "1",
		},
	}
	b = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "b",
			Namespace: "1",
		},
	}
	c = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "c",
			Namespace: "2",
		},
	}
	d = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "c",
			Namespace: "3",
		},
	}
	e = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "e",
			Namespace: "4",
		},
	}
	f = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "f",
			Namespace: "4",
		},
	}
	// empty string for name/namespace
	emptyKey = &corev1.Pod{}
)

func TestNewObjectSet(t *testing.T) {
	s := NewObjectSet(scheme, a, b, c)
	if s.Len() != 3 {
		t.Errorf("Expected len=3: %d", s.Len())
	}
	if !s.Has(a) || !s.Has(b) || !s.Has(c) {
		t.Errorf("Unexpected contents: %#v", s)
	}
}

func TestObjectSet(t *testing.T) {
	s := NewObjectSet(scheme)
	s2 := NewObjectSet(scheme)
	if s.Len() != 0 {
		t.Errorf("Expected len=0: %d", s.Len())
	}
	s.Insert(a, b)
	if s.Len() != 2 {
		t.Errorf("Expected len=2: %d", s.Len())
	}
	s.Insert(c)
	if s.Has(d) {
		t.Errorf("Unexpected contents: %#v", s)
	}
	if !s.Has(a) {
		t.Errorf("Missing contents: %#v", s)
	}
	s.Delete(a)
	if s.Has(a) {
		t.Errorf("Unexpected contents: %#v", s)
	}
	s.Insert(a)
	if s.HasAll(a, b, d) {
		t.Errorf("Unexpected contents: %#v", s)
	}
	if !s.HasAll(a, b) {
		t.Errorf("Missing contents: %#v", s)
	}
	s2.Insert(a, b, d)
	if s.IsSuperset(s2) {
		t.Errorf("Unexpected contents: %#v", s)
	}
	s2.Delete(d)
	if !s.IsSuperset(s2) {
		t.Errorf("Missing contents: %#v", s)
	}

	r := s2.GetByRef(*meta.MustTypedObjectRefFromObject(b, scheme))
	if got := cmp.Diff(r, b); got != "" {
		t.Errorf("Unexpected result for GetByKey: %v", r)
	}

	s.DeleteByRef(*meta.MustTypedObjectRefFromObject(b, scheme))
	if s.Has(b) {
		t.Errorf("Unexpected contents: %#v", s)
	}
}

func TestObjectSet_DeleteMultiples(t *testing.T) {
	s := NewObjectSet(scheme)
	s.Insert(a, b, c)
	if s.Len() != 3 {
		t.Errorf("Expected len=3: %d", s.Len())
	}

	s.Delete(a, c)
	if s.Len() != 1 {
		t.Errorf("Expected len=1: %d", s.Len())
	}
	if s.Has(a) {
		t.Errorf("Unexpected contents: %#v", s)
	}
	if s.Has(c) {
		t.Errorf("Unexpected contents: %#v", s)
	}
	if !s.Has(b) {
		t.Errorf("Missing contents: %#v", s)
	}
}

func TestObjectSet_List(t *testing.T) {
	s := NewObjectSet(scheme, d, c, b, a)
	// return in sorted order
	if diff := cmp.Diff(s.List(), []client.Object{a, b, c, d}); diff != "" {
		t.Errorf("List gave unexpected results:\n%s", diff)
	}
}

func TestObjectSet_Difference(t *testing.T) {
	s1 := NewObjectSet(scheme, a, b, c)
	s2 := NewObjectSet(scheme, a, b, d, e)
	d1 := s1.Difference(s2)
	d2 := s2.Difference(s1)
	if d1.Len() != 1 {
		t.Errorf("Expected len=1: %d", d1.Len())
	}
	if !d1.Has(c) {
		t.Errorf("Unexpected contents: %#v", d1.List())
	}
	if d2.Len() != 2 {
		t.Errorf("Expected len=2: %d", d2.Len())
	}
	if !d2.Has(d) || !d2.Has(e) {
		t.Errorf("Unexpected contents: %#v", d2.List())
	}
}

func TestObjectSet_HasAny(t *testing.T) {
	s := NewObjectSet(scheme, a, b, c)

	if !s.HasAny(a, d) {
		t.Errorf("expected true, got false")
	}

	if s.HasAny(d, e) {
		t.Errorf("expected false, got true")
	}
}

func TestObjectSet_Equals(t *testing.T) {
	// Simple case (order doesn't matter)
	s1 := NewObjectSet(scheme, a, b)
	s2 := NewObjectSet(scheme, b, a)
	if !s1.Equal(s2) {
		t.Errorf("Expected to be equal: %v vs %v", a, b)
	}

	// It is a set; duplicates are ignored
	s2 = NewObjectSet(scheme, b, b, a)
	if !s1.Equal(s2) {
		t.Errorf("Expected to be equal: %v vs %v", a, b)
	}

	// Edge cases around empty sets / empty strings
	s1 = NewObjectSet(scheme)
	s2 = NewObjectSet(scheme)
	if !s1.Equal(s2) {
		t.Errorf("Expected to be equal: %v vs %v", a, b)
	}

	s2 = NewObjectSet(scheme, a, b, c)
	if s1.Equal(s2) {
		t.Errorf("Expected to be not-equal: %v vs %v", a, b)
	}

	s2 = NewObjectSet(scheme, a, b, emptyKey)
	if s1.Equal(s2) {
		t.Errorf("Expected to be not-equal: %v vs %v", a, b)
	}

	// Check for equality after mutation
	s1 = NewObjectSet(scheme)
	s1.Insert(a)
	if s1.Equal(s2) {
		t.Errorf("Expected to be not-equal: %v vs %v", a, b)
	}

	s1.Insert(b)
	if s1.Equal(s2) {
		t.Errorf("Expected to be not-equal: %v vs %v", a, b)
	}

	s1.Insert(emptyKey)
	if !s1.Equal(s2) {
		t.Errorf("Expected to be equal: %v vs %v", a, b)
	}

	s1.Delete(emptyKey)
	if s1.Equal(s2) {
		t.Errorf("Expected to be not-equal: %v vs %v", a, b)
	}
}

func TestObjectSet_Union(t *testing.T) {
	cCopy := c.DeepCopy()
	cCopy.Spec.AutomountServiceAccountToken = ptr.To(true)
	dCopy := d.DeepCopy()
	dCopy.Spec.AutomountServiceAccountToken = ptr.To(true)
	tests := []struct {
		name     string
		s1       *ObjectSet
		s2       *ObjectSet
		expected *ObjectSet
	}{
		{
			name:     "union",
			s1:       NewObjectSet(scheme, a, b, c, d),
			s2:       NewObjectSet(scheme, c, d, e, f),
			expected: NewObjectSet(scheme, a, b, c, d, e, f),
		},
		{
			name:     "disjoint",
			s1:       NewObjectSet(scheme, a, b, c, d),
			s2:       NewObjectSet(scheme),
			expected: NewObjectSet(scheme, a, b, c, d),
		},
		{
			name:     "identity",
			s1:       NewObjectSet(scheme, a, b, c, d),
			s2:       NewObjectSet(scheme, d, c, b, a),
			expected: NewObjectSet(scheme, a, b, c, d),
		},
		{
			name:     "empty",
			s1:       NewObjectSet(scheme),
			s2:       NewObjectSet(scheme),
			expected: NewObjectSet(scheme),
		},
		{
			name:     "values",
			s1:       NewObjectSet(scheme, a, b, c, dCopy),
			s2:       NewObjectSet(scheme, cCopy, d, e, f),
			expected: NewObjectSet(scheme, a, b, c, dCopy, e, f), // result should contain values sourced from s1
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

func TestObjectSet_Intersection(t *testing.T) {
	cCopy := c.DeepCopy()
	cCopy.Spec.AutomountServiceAccountToken = ptr.To(true)
	dCopy := d.DeepCopy()
	dCopy.Spec.AutomountServiceAccountToken = ptr.To(true)
	cases := []struct {
		name     string
		s1       *ObjectSet
		s2       *ObjectSet
		expected *ObjectSet
	}{
		{
			name:     "intersects",
			s1:       NewObjectSet(scheme, a, b, c, d),
			s2:       NewObjectSet(scheme, c, d, e, f),
			expected: NewObjectSet(scheme, c, d),
		},
		{
			name:     "identity",
			s1:       NewObjectSet(scheme, a, b, c, d),
			s2:       NewObjectSet(scheme, a, b, c, d),
			expected: NewObjectSet(scheme, a, b, c, d),
		},
		{
			name:     "disjoint",
			s1:       NewObjectSet(scheme, a, b, c, d),
			s2:       NewObjectSet(scheme),
			expected: NewObjectSet(scheme),
		},
		{
			name:     "empty",
			s1:       NewObjectSet(scheme),
			s2:       NewObjectSet(scheme),
			expected: NewObjectSet(scheme),
		},
		{
			name:     "values",
			s1:       NewObjectSet(scheme, a, b, cCopy, d),
			s2:       NewObjectSet(scheme, c, dCopy, e, f),
			expected: NewObjectSet(scheme, cCopy, d), // result should contain values sourced from s1
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

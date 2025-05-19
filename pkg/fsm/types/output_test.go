package types

import (
	"testing"
	"unsafe"

	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/reddit/achilles-sdk/pkg/internal/scheme"
	"github.com/reddit/achilles-sdk/pkg/io"
	"github.com/reddit/achilles-sdk/pkg/sets"
)

func Test_OutputSet(t *testing.T) {
	scheme, err := scheme.NewScheme()
	if err != nil {
		t.Fatalf("building scheme: %s", err)
	}
	outputSet := NewOutputSet(scheme)

	o1 := cm("cm1", "ns")
	o2 := cm("cm2", "ns")
	o2applyOpts := []io.ApplyOption{io.AsUpdate(), io.WithOptimisticLock()}
	o3 := cm("cm3", "ns")
	o4 := cm("cm4", "ns")
	o5 := cm("cm4", "ns")

	// case 1: add object without apply option
	outputSet.Apply(o1)
	// case 2: add object with apply options
	outputSet.Apply(o2, o2applyOpts...)
	// case 3: delete object
	outputSet.Apply(o3, o2applyOpts...)
	outputSet.Delete(o3)

	actualO1ApplyOpts := outputSet.applyOpts[outputSet.key(o1)]
	actualO2ApplyOpts := outputSet.applyOpts[outputSet.key(o2)]

	if diff := cmp.Diff(len(outputSet.ListAppliedOutputs()), 2); diff != "" {
		t.Errorf("unexpected output length: (-got +want)\n%s", diff)
	}

	// assert state for o1
	if !applyOptsEqual(actualO1ApplyOpts, nil) {
		t.Errorf("unexpected apply options for o1")
	}

	// assert state for o2
	if !applyOptsEqual(actualO2ApplyOpts, o2applyOpts) {
		t.Errorf("unexpected apply options for o2")
	}

	// assert state for o3
	expectedDeleted := sets.NewObjectSet(scheme, o3)
	if !outputSet.deleted.Equal(expectedDeleted) {
		t.Errorf("unexpected deleted set for o3")
	}
	if len(outputSet.applyOpts[outputSet.key(o3)]) > 0 {
		t.Errorf("unexpected existence of apply opts for o3")
	}
	if outputSet.applied.Has(o3) {
		t.Errorf("unexpected existence of o3 in applied set")
	}

	// case 4: exercise ApplyAll and DeleteAll
	outputSet.ApplyAll(o4, o5)
	expectedApplied := sets.NewObjectSet(scheme, o1, o2, o4, o5)
	if !outputSet.applied.Equal(expectedApplied) {
		t.Errorf("unexpected applied set after applying o4 and o5")
	}
	outputSet.DeleteAll(o4, o5)
	expectedDeleted = sets.NewObjectSet(scheme, o3, o4, o5)
	if !outputSet.deleted.Equal(expectedDeleted) {
		t.Errorf("unexpected deleted set after deleting o4 and o5")
	}
}

func cm(name, namespace string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
}

func applyOptsEqual(a, b []io.ApplyOption) bool {
	if len(a) != len(b) {
		return false
	}
	// https://github.com/google/go-cmp/issues/162
	// this logic is needed for comparing function pointer equality
	for _, applyOptA := range a {
		pa := *(*unsafe.Pointer)(unsafe.Pointer(&applyOptA))

		var found bool
		for _, applyOptB := range b {
			pb := *(*unsafe.Pointer)(unsafe.Pointer(&applyOptB))
			if pa == pb {
				found = true
				break
			}
		}

		if !found {
			return false
		}
	}
	return true
}

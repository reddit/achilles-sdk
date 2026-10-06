package shard

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
)

func TestPropagateCopiesTheRootsValue(t *testing.T) {
	child := &corev1.ConfigMap{}
	Propagate(labeled("0"), child)

	assert.Equal(t, "0", child.GetLabels()[DefaultKey])
}

func TestPropagatePreservesExistingLabels(t *testing.T) {
	child := &corev1.ConfigMap{}
	child.SetLabels(map[string]string{"other": "value"})
	Propagate(labeled("0"), child)

	assert.Equal(t, map[string]string{"other": "value", DefaultKey: "0"}, child.GetLabels())
}

// An unsharded controller's objects carry no value, and must not have an empty one invented for
// them: an empty value is unreconcilable, whereas no label at all leaves the object untouched.
func TestPropagateFromAnUnlabeledRootIsANoOp(t *testing.T) {
	child := &corev1.ConfigMap{}
	Propagate(labeled(""), child)

	assert.Nil(t, child.GetLabels())
}

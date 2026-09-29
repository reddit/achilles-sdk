package shard

import (
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Propagate copies the shard label from a root object onto a child it manages, so a sharded
// controller's child informers can be filtered by the same selector as its roots.
//
// The value is taken from the root rather than from the instance's own selector, which keeps it
// correct for selectors matching several values and makes the child's shard agree with its owner's
// by construction. A root with no shard label leaves its children untouched, so this is a no-op
// when sharding is disabled and for the catch-all instance.
func Propagate(root, child client.Object) {
	value, ok := root.GetLabels()[DefaultKey]
	if !ok {
		return
	}

	childLabels := child.GetLabels()
	if childLabels == nil {
		childLabels = map[string]string{}
	}
	childLabels[DefaultKey] = value
	child.SetLabels(childLabels)
}

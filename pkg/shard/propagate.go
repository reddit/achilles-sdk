package shard

import (
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Propagate copies a root object's shard value onto something it produced.
//
// Only a value an instance was configured with is ever reconciled, so an object a sharded
// controller creates has to inherit one or it would be reconciled by nobody. Taking the value from
// the root rather than from the instance's configuration makes the two agree by construction.
//
// A root with no shard value leaves its output untouched, since an empty value is as
// unreconcilable as a missing one.
func Propagate(root, output client.Object) {
	value, ok := valueOf(root, DefaultKey)
	if !ok {
		return
	}

	labels := output.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labels[DefaultKey] = value
	output.SetLabels(labels)
}

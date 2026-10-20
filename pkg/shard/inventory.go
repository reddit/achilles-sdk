package shard

import (
	"sort"

	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// MissingValue stands in, when reporting, for objects carrying no shard value. Spelled so that it
// cannot be mistaken for a real value, which is always a valid label name.
const MissingValue = "<none>"

// Inventory is the set of shard values objects actually carry. What an instance serves is
// configured, so this only feeds the report of which values no instance serves.
type Inventory struct {
	// Values are the distinct non-empty shard label values in use.
	Values sets.Set[string]

	// Missing reports whether any object carries no shard value.
	Missing bool
}

// reported lists every value in use, deterministically, with MissingValue standing in for the
// objects that carry none.
func (i Inventory) reported() []string {
	values := i.Values.UnsortedList()
	sort.Strings(values)

	if i.Missing {
		values = append(values, MissingValue)
	}
	return values
}

// valueOf reports an object's shard value and whether it has a usable one.
//
// An empty label is treated as an absent one, since neither can be configured on an instance and
// so neither is reconcilable; distinguishing them would only add a partition nobody can claim.
func valueOf(obj client.Object, key string) (string, bool) {
	value := obj.GetLabels()[key]
	return value, value != ""
}

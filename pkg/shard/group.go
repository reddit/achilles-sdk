package shard

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
)

// Group is the identity that scopes a controller's shard Leases.
//
// It is derived from the types being partitioned rather than from a name, because "what is being
// partitioned" is exactly the right scope: every shard of one controller must contend for the same
// value Leases, while an unrelated controller in the same namespace must not. A name would have to
// be shared by all shards of a controller and distinct between controllers — a constraint nothing
// enforces, and one that silently breaks arbitration when violated, since each shard would then
// create its own private Lease per value and acquire it uncontested.
type Group struct {
	// Name prefixes the Lease objects, and is readable so that `kubectl get leases` is useful.
	Name string

	// Types canonically identifies the partitioned types. Name drops the API group to stay short,
	// so this is what distinguishes two same-named Kinds from different groups.
	Types string
}

// GroupFor derives the Group for a set of partitioned types.
func GroupFor(gvks []schema.GroupVersionKind) (Group, error) {
	if len(gvks) == 0 {
		return Group{}, fmt.Errorf("cannot derive a shard group from no types")
	}

	canonical := make([]string, 0, len(gvks))
	kinds := make([]string, 0, len(gvks))
	for _, gvk := range gvks {
		if gvk.Kind == "" {
			return Group{}, fmt.Errorf("cannot derive a shard group from an empty Kind")
		}
		canonical = append(canonical, gvk.GroupVersion().String()+","+gvk.Kind)
		kinds = append(kinds, strings.ToLower(gvk.Kind))
	}
	sort.Strings(canonical)
	sort.Strings(kinds)

	types := strings.Join(canonical, ";")

	name := strings.Join(kinds, "-")
	if len(name) > maxReadableIDLength || len(validation.IsDNS1123Label(name)) > 0 {
		sum := sha256.Sum256([]byte(types))
		name = "h" + hex.EncodeToString(sum[:])[:10]
	}

	return Group{Name: name, Types: types}, nil
}

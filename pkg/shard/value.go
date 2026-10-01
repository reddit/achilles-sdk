package shard

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// KindLabelKey distinguishes the two roles a shard Lease plays, so that listing one kind never
	// picks up the other.
	KindLabelKey = "shard.infrared.reddit.com/kind"

	// kindAdvertisement marks the Lease publishing the values a shard manages.
	kindAdvertisement = "advertisement"

	// kindValue marks the Lease conferring ownership of one concrete shard value.
	kindValue = "value"

	// ValueAnnotationKey records the shard value a Lease confers, which the Lease name only
	// approximates once a value is long enough or odd enough to need hashing.
	ValueAnnotationKey = "shard.infrared.reddit.com/value"

	// InstanceLabelKey names the controller instance (its Deployment), which is the unit leader
	// election elects within. It is what permits immediate takeover: a Lease bearing our own
	// instance was held by a predecessor leader of this very Deployment.
	InstanceLabelKey = "shard.infrared.reddit.com/instance"

	// TypesAnnotationKey records the partitioned types, so that two controllers whose Kinds happen
	// to collide do not silently block each other's values.
	TypesAnnotationKey = "shard.infrared.reddit.com/types"
)

// valueRef identifies one partition of the objects: either a concrete value of the shard label, or
// the objects carrying no shard label at all.
//
// The sentinel cannot be folded into value as the empty string, because "" is itself a legal label
// value and would then share a partition with unlabelled objects.
type valueRef struct {
	value     string
	unlabeled bool
}

// managedBy reports whether a shard manages this partition.
func (r valueRef) managedBy(s *Shard) bool {
	if r.unlabeled {
		return s.OwnsUnlabeled()
	}
	return s.Owns(r.value)
}

// String renders the partition the way an operator configures it, so that a reported value can be
// pasted straight into --shard-values.
func (r valueRef) String() string {
	if r.unlabeled {
		return UnlabeledValue
	}
	return r.value
}

// Inventory is the set of shard partitions objects actually occupy. Ownership is configured rather
// than discovered, so this is used to report the partitions no instance manages, not to decide
// what to claim.
type Inventory struct {
	// Values are the distinct shard label values in use.
	Values sets.Set[string]

	// Unlabeled reports whether any object carries no shard label.
	Unlabeled bool
}

// refs returns the partitions in a deterministic order, so that reported values are stable between
// syncs.
func (i Inventory) refs() []valueRef {
	values := i.Values.UnsortedList()
	sort.Strings(values)

	refs := make([]valueRef, 0, len(values)+1)
	for _, v := range values {
		refs = append(refs, valueRef{value: v})
	}
	if i.Unlabeled {
		refs = append(refs, valueRef{unlabeled: true})
	}
	return refs
}

// refs returns the partitions this shard manages, in a deterministic order so that concurrently
// syncing owners contend for leases in the same sequence.
func (s *Shard) refs() []valueRef {
	refs := make([]valueRef, 0, s.values.Len()+1)
	for _, v := range sets.List(s.values) {
		refs = append(refs, valueRef{value: v})
	}
	if s.unlabeled {
		refs = append(refs, valueRef{unlabeled: true})
	}
	return refs
}

// ValueOf reports which partition an object belongs to.
func valueOf(obj client.Object, key string) valueRef {
	value, ok := obj.GetLabels()[key]
	if !ok {
		return valueRef{unlabeled: true}
	}
	return valueRef{value: value}
}

// valueLeaseName names the Lease conferring ownership of one partition.
//
// Label values admit characters object names do not (uppercase, underscores), so a value that will
// not survive as a name is hashed. The readable form is preferred because these names are what an
// operator reads out of `kubectl get leases` when working out who owns what.
func valueLeaseName(group, value string, unlabeled bool) string {
	if unlabeled {
		return group + "-unlabeled"
	}

	id := value
	if id == "" || len(id) > maxReadableIDLength || len(validation.IsDNS1123Label(id)) > 0 {
		sum := sha256.Sum256([]byte(value))
		id = "h" + hex.EncodeToString(sum[:])[:10]
	}
	return fmt.Sprintf("%s-value-%s", group, id)
}

// instanceLeaseName names the Lease advertising one controller instance's selector. Keyed on the
// instance rather than the shard so that two Deployments sharing an instance name — which means
// sharing a leader election lock, and so one of them never running — is detectable.
func instanceLeaseName(group, instance string) string {
	name := fmt.Sprintf("%s-instance-%s", group, instance)
	if isDNSSubdomain(name) {
		return name
	}

	sum := sha256.Sum256([]byte(instance))
	return fmt.Sprintf("%s-instance-h%s", group, hex.EncodeToString(sum[:])[:10])
}

func isDNSSubdomain(name string) bool {
	return len(validation.IsDNS1123Subdomain(name)) == 0
}

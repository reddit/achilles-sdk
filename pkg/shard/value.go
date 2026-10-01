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

// MissingValue is the value reported for objects that carry no usable shard value, i.e. no shard
// label or an empty one.
//
// Such an object can never be reconciled, since only a concrete value can be declared on an
// instance, so it is always reported as ignored. Spelled with angle brackets so it cannot be
// confused with a real value, which is alphanumeric with dashes, dots, and underscores.
const MissingValue = "<none>"

// Inventory is the set of shard values objects actually carry. Ownership is configured rather than
// discovered, so this is used to report the values no instance manages, not to decide what to
// claim.
type Inventory struct {
	// Values are the distinct non-empty shard label values in use.
	Values sets.Set[string]

	// Unlabeled reports whether any object carries no usable shard value.
	Unlabeled bool
}

// reported returns every value in use for metric purposes, in a deterministic order, with
// MissingValue standing in for the objects that carry none.
func (i Inventory) reported() []string {
	values := i.Values.UnsortedList()
	sort.Strings(values)

	if i.Unlabeled {
		values = append(values, MissingValue)
	}
	return values
}

// valueOf reports an object's shard value, and whether it has a usable one at all.
//
// An empty label value is treated the same as an absent one: neither can be declared on an
// instance, so neither is reconcilable, and distinguishing them would only add an unclaimable
// partition.
func valueOf(obj client.Object, key string) (string, bool) {
	value := obj.GetLabels()[key]
	return value, value != ""
}

// valueLeaseName names the Lease conferring ownership of one shard value.
//
// Label values admit characters object names do not (uppercase, underscores), so a value that will
// not survive as a name is hashed. The readable form is preferred because these names are what an
// operator reads out of `kubectl get leases` when working out who owns what.
func valueLeaseName(group, value string) string {
	id := value
	if id == "" || len(id) > maxReadableIDLength || len(validation.IsDNS1123Label(id)) > 0 {
		sum := sha256.Sum256([]byte(value))
		id = "h" + hex.EncodeToString(sum[:])[:10]
	}
	return fmt.Sprintf("%s-value-%s", group, id)
}

// instanceLeaseName names the Lease advertising one controller instance's shard values. Keyed on
// the instance rather than the shard so that two Deployments sharing an instance name — which means
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

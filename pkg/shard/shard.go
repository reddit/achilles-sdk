// Package shard implements label-based sharding for achilles-sdk controllers: running several
// instances of one controller in a cluster, each reconciling a mutually exclusive slice of the same
// CRs.
//
// An instance is configured with the concrete shard values it manages. Nothing is filtered: every
// instance still watches and caches every object, and ignores the ones whose value it does not
// manage. Exclusivity is settled at run time by Owner, which holds a Lease per managed value.
//
// A value no instance is configured with is reconciled by nobody, deliberately: assignment is an
// operator decision, so a value appearing at run time is reported rather than guessed at. See
// achilles_shard_values_ignored.
//
// The label itself is applied by the platform, not by the SDK; the SDK only reads it, and
// propagates it from a root object onto the children that object manages.
package shard

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation"
)

// DefaultKey is the label key that assigns an object to a shard.
const DefaultKey = "shard.infrared.reddit.com/key"

// UnlabeledValue is the reserved value naming the partition of objects carrying no shard label.
//
// Spelled with a leading "@" so it cannot collide with a real value: Kubernetes label values are
// alphanumeric with dashes, dots, and underscores, so "@unlabeled" is not one.
const UnlabeledValue = "@unlabeled"

// catchAllID renders UnlabeledValue into the DNS-safe names derived from a shard's values.
const catchAllID = "catchall"

// maxReadableIDLength keeps a spelled-out ID well inside the 63-character DNS label limit, leaving
// room for the prefixes callers add.
const maxReadableIDLength = 40

// Shard is the slice of objects owned by one controller instance: the set of concrete shard values
// it manages, plus whether it manages the unlabeled partition.
type Shard struct {
	key       string
	values    sets.Set[string]
	unlabeled bool
}

// Parse builds a Shard from the concrete shard values an instance manages, e.g. {"0", "1"}, or
// {UnlabeledValue} for the instance managing everything that carries no shard label.
//
// An empty list returns a nil Shard, meaning sharding is disabled. Values are deduplicated, and
// each must be a legal label value so that a managed value can always be matched against a real
// object's label.
func Parse(key string, values []string) (*Shard, error) {
	s := &Shard{key: key, values: sets.New[string]()}

	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if value == UnlabeledValue {
			s.unlabeled = true
			continue
		}
		if errs := validation.IsValidLabelValue(value); len(errs) > 0 {
			return nil, fmt.Errorf("shard value %q is not a valid label value: %s", value, strings.Join(errs, "; "))
		}
		s.values.Insert(value)
	}

	if s.values.Len() == 0 && !s.unlabeled {
		return nil, nil
	}
	return s, nil
}

// Key returns the label key the shard is defined over.
func (s *Shard) Key() string { return s.key }

// Values returns the managed values in canonical order, with UnlabeledValue included when the
// shard manages the unlabeled partition.
func (s *Shard) Values() []string {
	out := sets.List(s.values)
	if s.unlabeled {
		out = append(out, UnlabeledValue)
	}
	return out
}

// String renders the managed values as the operator would write them.
func (s *Shard) String() string { return strings.Join(s.Values(), ",") }

// Owns reports whether the shard manages a concrete value of the shard label.
func (s *Shard) Owns(value string) bool { return s.values.Has(value) }

// OwnsUnlabeled reports whether the shard manages the objects carrying no shard label.
func (s *Shard) OwnsUnlabeled() bool { return s.unlabeled }

// ID is a stable, DNS-1123-safe identifier for the shard, used to label the Leases that record
// which shard holds what.
//
// The ID spells the values out when they have a short DNS-safe rendering and falls back to a hash
// only when they do not, since these names are what an operator sees when debugging.
func (s *Shard) ID() string {
	if readable, ok := s.readableID(); ok {
		return readable
	}

	sum := sha256.Sum256([]byte(s.canonical()))
	return "h" + hex.EncodeToString(sum[:])[:10]
}

func (s *Shard) readableID() (string, bool) {
	// A literal value spelled like the sentinel's rendering would make the ID ambiguous, whether
	// or not this shard also manages the unlabeled partition.
	if s.values.Has(catchAllID) {
		return "", false
	}

	parts := sets.List(s.values)
	if s.unlabeled {
		parts = append(parts, catchAllID)
	}

	candidate := strings.Join(parts, "-")
	if candidate == "" || len(candidate) > maxReadableIDLength || len(validation.IsDNS1123Label(candidate)) > 0 {
		return "", false
	}
	return candidate, true
}

// canonical renders the shard so that equivalent configurations hash identically regardless of the
// order the values were given in.
func (s *Shard) canonical() string {
	return fmt.Sprintf("unlabeled=%t;values=%s", s.unlabeled, strings.Join(sets.List(s.values), ","))
}

// Equal reports whether both shards manage exactly the same values. Used to detect two instances
// sharing a leader election lock while disagreeing about their work, which is fatal.
func (s *Shard) Equal(other *Shard) bool {
	if s == nil || other == nil {
		return s == other
	}
	return s.unlabeled == other.unlabeled && s.values.Equal(other.values)
}

// Conflict describes what both shards manage, or "" when they are disjoint.
//
// With concrete values this is an exact set intersection, so an overlap is a plain configuration
// mistake rather than something inferred from selector algebra.
func (s *Shard) Conflict(other *Shard) string {
	if s == nil || other == nil {
		return ""
	}

	var parts []string
	if s.unlabeled && other.unlabeled {
		parts = append(parts, "objects carrying no shard label")
	}
	if shared := s.values.Intersection(other.values); shared.Len() > 0 {
		values := sets.List(shared)
		sort.Strings(values)
		parts = append(parts, "the shard value(s) "+strings.Join(values, ", "))
	}
	return strings.Join(parts, " and ")
}

// Overlaps reports whether both shards manage the same object.
func (s *Shard) Overlaps(other *Shard) bool { return s.Conflict(other) != "" }

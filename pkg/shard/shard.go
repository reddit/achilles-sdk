// Package shard divides a controller's objects between several instances of that controller, so
// that one controller's work can be spread over more than one Deployment.
//
// An object belongs to the shard named by its shard label. Each instance is configured with the
// concrete values it serves, and reconciles an object only while it holds that value's Lease, so
// two instances mistakenly configured with the same value still reconcile its objects once.
//
// Values are assigned, never inferred. A value no instance is configured with — including the
// absent value, that is, an object carrying no shard label — is reconciled by nobody and reported
// through achilles_shard_values_ignored, because which instance should serve it is an operator's
// decision rather than something to guess at.
package shard

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation"
)

// DefaultKey is the label whose value names the shard an object belongs to.
const DefaultKey = "shard.infrared.reddit.com/key"

// Shard is the set of shard values a single instance serves.
type Shard struct {
	key    string
	values sets.Set[string]
}

// Parse builds the Shard for a set of configured values, returning nil when none are configured,
// which disables sharding.
//
// Each value has to be usable as an object name, since it names a Lease. Rejecting such a value
// rather than mangling it keeps the Lease name derivable from the value alone, which is all an
// instance knows about a value another instance serves.
func Parse(key string, values []string) (*Shard, error) {
	parsed := sets.New[string]()

	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if errs := validation.IsDNS1123Label(value); len(errs) > 0 {
			return nil, fmt.Errorf("shard value %q cannot name a Lease: %s", value, strings.Join(errs, "; "))
		}
		parsed.Insert(value)
	}

	if parsed.Len() == 0 {
		return nil, nil
	}
	return &Shard{key: key, values: parsed}, nil
}

// Key is the label naming an object's shard.
func (s *Shard) Key() string { return s.key }

// Values are the shard values this instance serves, in a deterministic order.
func (s *Shard) Values() []string { return sets.List(s.values) }

// Serves reports whether this instance is configured to serve the given value.
func (s *Shard) Serves(value string) bool { return s.values.Has(value) }

func (s *Shard) String() string { return strings.Join(s.Values(), ",") }

// Package shard implements label-based sharding for achilles-sdk controllers: running several
// instances of one controller in a cluster, each owning a mutually exclusive slice of the same CRs.
//
// An instance's slice is described by a label selector constrained to a single key, which keeps
// overlap between two instances exactly decidable (see Shard.Overlaps). The label itself is applied
// by the platform, not by the SDK; the SDK only reads it, and propagates it from a root object onto
// the children that object manages.
package shard

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/util/validation"
)

// DefaultKey is the label key that assigns an object to a shard.
const DefaultKey = "shard.infrared.reddit.com/key"

// catchAllID is the ID of the shard that owns every object lacking the shard label.
const catchAllID = "catchall"

// Shard is the slice of objects owned by one controller instance.
type Shard struct {
	key      string
	raw      string
	selector labels.Selector
	values   valueSet
}

// Parse builds a Shard from a Kubernetes label selector, e.g. "shard.infrared.reddit.com/key=a" or
// "!shard.infrared.reddit.com/key" for the instance that owns everything unlabeled.
//
// An empty raw selector returns a nil Shard, meaning sharding is disabled. Every requirement must
// reference key, both so that the shard value stamped onto children is unambiguous and so that two
// shards can be proven disjoint rather than conservatively assumed to overlap.
func Parse(key, raw string) (*Shard, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}

	selector, err := labels.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parsing shard selector %q: %w", raw, err)
	}

	reqs, _ := selector.Requirements()
	for _, r := range reqs {
		if r.Key() != key {
			return nil, fmt.Errorf("shard selector %q may only reference the key %q, got %q", raw, key, r.Key())
		}
	}

	values := everything()
	for _, r := range reqs {
		values = values.intersect(fromRequirement(r))
	}
	if values.empty() {
		return nil, fmt.Errorf("shard selector %q matches no objects", raw)
	}

	return &Shard{key: key, raw: raw, selector: selector, values: values}, nil
}

// Key returns the label key the shard is defined over.
func (s *Shard) Key() string { return s.key }

// Selector returns the selector to filter informers and watches with.
func (s *Shard) Selector() labels.Selector { return s.selector }

// String returns the selector as the operator wrote it.
func (s *Shard) String() string { return s.raw }

// Requirements returns the selector's requirements, for composing into an existing selector.
func (s *Shard) Requirements() labels.Requirements {
	reqs, _ := s.selector.Requirements()
	return reqs
}

// ID is a stable, DNS-1123-safe identifier for the shard, used to name the resources that must be
// unique per shard (the leader election lock and the shard's advertisement Lease). Selectors that
// name a single value use it verbatim so those resources stay recognizable; anything more complex
// is hashed, since it has no short readable form.
func (s *Shard) ID() string {
	if s.values.onlyAbsent() {
		return catchAllID
	}
	if v, ok := s.values.single(); ok && len(validation.IsDNS1123Label(v)) == 0 {
		return v
	}

	sum := sha256.Sum256([]byte(s.values.canonical()))
	return "h" + hex.EncodeToString(sum[:])[:10]
}

// Overlaps reports whether both shards can match the same object, which means they would both
// reconcile it. Because every requirement is constrained to one key, this is an exact set
// intersection rather than a conservative approximation.
func (s *Shard) Overlaps(other *Shard) bool {
	if s == nil || other == nil {
		return false
	}
	return !s.values.intersect(other.values).empty()
}

// valueSet is the set of label values a selector matches, over the domain {absent} plus the
// unbounded space of label values.
//
// finite distinguishes the two representations values can take: when true it holds exactly the
// matched values, when false it holds the excluded ones and everything else matches. The second
// form is needed because "notin" and "!=" match an unbounded set.
type valueSet struct {
	absent bool
	finite bool
	values map[string]struct{}
}

func everything() valueSet {
	return valueSet{absent: true, finite: false, values: map[string]struct{}{}}
}

// fromRequirement converts a single selector requirement into the value set it matches. The absent
// cases mirror labels.Requirement.Matches, where NotIn and NotEquals are satisfied by objects that
// do not carry the key at all.
func fromRequirement(r labels.Requirement) valueSet {
	vals := map[string]struct{}{}
	for _, v := range r.Values().List() {
		vals[v] = struct{}{}
	}

	switch r.Operator() {
	case selection.Equals, selection.DoubleEquals, selection.In:
		return valueSet{absent: false, finite: true, values: vals}
	case selection.NotEquals, selection.NotIn:
		return valueSet{absent: true, finite: false, values: vals}
	case selection.Exists:
		return valueSet{absent: false, finite: false, values: map[string]struct{}{}}
	case selection.DoesNotExist:
		return valueSet{absent: true, finite: true, values: map[string]struct{}{}}
	default:
		// Gt and Lt are meaningless for a shard key; match nothing rather than guess.
		return valueSet{absent: false, finite: true, values: map[string]struct{}{}}
	}
}

func (v valueSet) intersect(o valueSet) valueSet {
	out := valueSet{absent: v.absent && o.absent, values: map[string]struct{}{}}

	switch {
	case v.finite && o.finite: // both enumerate matches
		out.finite = true
		for k := range v.values {
			if _, ok := o.values[k]; ok {
				out.values[k] = struct{}{}
			}
		}
	case v.finite != o.finite: // one enumerates matches, the other exclusions
		in, ex := v, o
		if o.finite {
			in, ex = o, v
		}
		out.finite = true
		for k := range in.values {
			if _, excluded := ex.values[k]; !excluded {
				out.values[k] = struct{}{}
			}
		}
	default: // both enumerate exclusions, so the union is excluded
		out.finite = false
		for k := range v.values {
			out.values[k] = struct{}{}
		}
		for k := range o.values {
			out.values[k] = struct{}{}
		}
	}

	return out
}

// empty reports whether the set matches nothing. A non-finite set is never empty, since it matches
// every value outside a finite exclusion list.
func (v valueSet) empty() bool {
	return !v.absent && v.finite && len(v.values) == 0
}

func (v valueSet) onlyAbsent() bool {
	return v.absent && v.finite && len(v.values) == 0
}

func (v valueSet) single() (string, bool) {
	if v.absent || !v.finite || len(v.values) != 1 {
		return "", false
	}
	for k := range v.values {
		return k, true
	}
	return "", false
}

// canonical renders the set so that equivalent selectors hash identically regardless of how they
// were written.
func (v valueSet) canonical() string {
	sorted := make([]string, 0, len(v.values))
	for k := range v.values {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)

	return fmt.Sprintf("absent=%t;finite=%t;values=%s", v.absent, v.finite, strings.Join(sorted, ","))
}

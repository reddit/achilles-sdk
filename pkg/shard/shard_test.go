package shard

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/labels"
)

func TestParseRejectsForeignKeys(t *testing.T) {
	for _, raw := range []string{
		"other.example.com/key=a",
		"shard.infrared.reddit.com/key=a,env=prod",
		"env=prod",
	} {
		_, err := Parse(DefaultKey, raw)
		assert.ErrorContains(t, err, DefaultKey, "selector %q referencing a foreign key must be rejected", raw)
	}
}

func TestParseRejectsMalformed(t *testing.T) {
	_, err := Parse(DefaultKey, "!!!")
	assert.Error(t, err)
}

func TestParseEmptyYieldsNoShard(t *testing.T) {
	s, err := Parse(DefaultKey, "")
	require.NoError(t, err)
	assert.Nil(t, s, "an empty selector means sharding is disabled")
}

func TestParseRejectsUnsatisfiableSelector(t *testing.T) {
	// Matches nothing at all, so the controller would silently reconcile no objects.
	_, err := Parse(DefaultKey, "shard.infrared.reddit.com/key=a,shard.infrared.reddit.com/key=b")
	assert.ErrorContains(t, err, "matches no objects")
}

func TestID(t *testing.T) {
	tests := map[string]struct {
		raw  string
		want string
	}{
		"single value is used verbatim": {"shard.infrared.reddit.com/key=shard1", "shard1"},
		"single value via in":           {"shard.infrared.reddit.com/key in (shard1)", "shard1"},
		"catch-all":                     {"!shard.infrared.reddit.com/key", "catchall"},
		"value set is spelled out":      {"shard.infrared.reddit.com/key in (a,b)", "a-b"},
		"tier split":                    {"shard.infrared.reddit.com/key in (0,1)", "0-1"},
		"negation is spelled out":       {"shard.infrared.reddit.com/key notin (0,1)", "not-0-1"},
		"exists":                        {"shard.infrared.reddit.com/key", "any"},
		"non-dns-safe value is hashed":  {"shard.infrared.reddit.com/key=Shard_1", "h"},
		"long value set is hashed":      {"shard.infrared.reddit.com/key in (aaaaaaaaaa,bbbbbbbbbb,cccccccccc,dddddddddd,eeeeeeeeee)", "h"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			s, err := Parse(DefaultKey, tc.raw)
			require.NoError(t, err)
			got := s.ID()

			if tc.want == "h" {
				assert.Regexp(t, `^h[0-9a-f]{10}$`, got)
			} else {
				assert.Equal(t, tc.want, got)
			}
		})
	}
}

func TestIDIsStableAndOrderIndependent(t *testing.T) {
	a, err := Parse(DefaultKey, "shard.infrared.reddit.com/key in (a,b)")
	require.NoError(t, err)
	b, err := Parse(DefaultKey, "shard.infrared.reddit.com/key in (b,a)")
	require.NoError(t, err)

	assert.Equal(t, a.ID(), b.ID(), "equivalent selectors must produce the same ID regardless of value order")
}

func TestIDIsDNSSafe(t *testing.T) {
	for _, raw := range []string{
		"shard.infrared.reddit.com/key=shard1",
		"!shard.infrared.reddit.com/key",
		"shard.infrared.reddit.com/key in (a,b)",
		"shard.infrared.reddit.com/key notin (0,1)",
		"shard.infrared.reddit.com/key",
		"shard.infrared.reddit.com/key=Shard_1",
	} {
		s, err := Parse(DefaultKey, raw)
		require.NoError(t, err)
		assert.Regexp(t, `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, s.ID(), "ID for %q must be usable in a resource name", raw)
	}
}

func TestOverlaps(t *testing.T) {
	tests := map[string]struct {
		a, b string
		want bool
	}{
		"identical single values":        {"shard.infrared.reddit.com/key=a", "shard.infrared.reddit.com/key=a", true},
		"disjoint single values":         {"shard.infrared.reddit.com/key=a", "shard.infrared.reddit.com/key=b", false},
		"overlapping value sets":         {"shard.infrared.reddit.com/key in (a,b)", "shard.infrared.reddit.com/key in (b,c)", true},
		"disjoint value sets":            {"shard.infrared.reddit.com/key in (a,b)", "shard.infrared.reddit.com/key in (c,d)", false},
		"catch-all vs labeled shard":     {"!shard.infrared.reddit.com/key", "shard.infrared.reddit.com/key=a", false},
		"catch-all vs catch-all":         {"!shard.infrared.reddit.com/key", "!shard.infrared.reddit.com/key", true},
		"catch-all vs exists":            {"!shard.infrared.reddit.com/key", "shard.infrared.reddit.com/key", false},
		"exists vs single value":         {"shard.infrared.reddit.com/key", "shard.infrared.reddit.com/key=a", true},
		"notin excludes that value":      {"shard.infrared.reddit.com/key notin (a)", "shard.infrared.reddit.com/key=a", false},
		"notin admits other values":      {"shard.infrared.reddit.com/key notin (a)", "shard.infrared.reddit.com/key=b", true},
		"notin matches unlabeled too":    {"shard.infrared.reddit.com/key notin (a)", "!shard.infrared.reddit.com/key", true},
		"two negations always overlap":   {"shard.infrared.reddit.com/key notin (a)", "shard.infrared.reddit.com/key notin (b)", true},
		"compound narrowing is disjoint": {"shard.infrared.reddit.com/key in (a,b),shard.infrared.reddit.com/key notin (b)", "shard.infrared.reddit.com/key=b", false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			a, err := Parse(DefaultKey, tc.a)
			require.NoError(t, err)
			b, err := Parse(DefaultKey, tc.b)
			require.NoError(t, err)

			assert.Equal(t, tc.want, a.Overlaps(b))
			assert.Equal(t, tc.want, b.Overlaps(a), "overlap must be symmetric")
		})
	}
}

func TestConflict(t *testing.T) {
	tests := map[string]struct {
		a, b string
		want string
	}{
		// Looks like a clean split of the values in use, but both shards own every unlabelled
		// object and every value neither excludes. Only one shard may be negated.
		"two negated shards": {
			"shard.infrared.reddit.com/key notin (0,1)",
			"shard.infrared.reddit.com/key notin (2,3)",
			"objects carrying no shard label and every shard value except 0, 1, 2, 3",
		},
		"shared value": {
			"shard.infrared.reddit.com/key in (a,b)",
			"shard.infrared.reddit.com/key in (b,c)",
			"the shard value(s) b",
		},
		"negated shard absorbs a named one": {
			"shard.infrared.reddit.com/key notin (0,1)",
			"shard.infrared.reddit.com/key=2",
			"the shard value(s) 2",
		},
		"catch-all against a named shard": {
			"!shard.infrared.reddit.com/key",
			"shard.infrared.reddit.com/key=a",
			"",
		},
		"the intended pairing is disjoint": {
			"shard.infrared.reddit.com/key in (0,1)",
			"shard.infrared.reddit.com/key notin (0,1)",
			"",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			a, err := Parse(DefaultKey, tc.a)
			require.NoError(t, err)
			b, err := Parse(DefaultKey, tc.b)
			require.NoError(t, err)

			assert.Equal(t, tc.want, a.Conflict(b))
			assert.Equal(t, tc.want, b.Conflict(a), "the description must not depend on argument order")
		})
	}
}

func TestSelectorMatchesExpectedObjects(t *testing.T) {
	tests := map[string]struct {
		raw    string
		labels map[string]string
		want   bool
	}{
		"equality matches":             {"shard.infrared.reddit.com/key=a", map[string]string{DefaultKey: "a"}, true},
		"equality rejects other value": {"shard.infrared.reddit.com/key=a", map[string]string{DefaultKey: "b"}, false},
		"equality rejects unlabeled":   {"shard.infrared.reddit.com/key=a", nil, false},
		"catch-all matches unlabeled":  {"!shard.infrared.reddit.com/key", nil, true},
		"catch-all rejects labeled":    {"!shard.infrared.reddit.com/key", map[string]string{DefaultKey: "a"}, false},
		// The semantics that make a single-selector catch-all possible at all.
		"notin matches unlabeled": {"shard.infrared.reddit.com/key notin (a)", nil, true},
		// A negated shard absorbs shard values that did not exist when it was deployed, so a new
		// value never lands with nobody until it is deliberately carved out.
		"notin absorbs an unforeseen value": {"shard.infrared.reddit.com/key notin (0,1)", map[string]string{DefaultKey: "2"}, true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			s, err := Parse(DefaultKey, tc.raw)
			require.NoError(t, err)
			assert.Equal(t, tc.want, s.Selector().Matches(labels.Set(tc.labels)))
		})
	}
}

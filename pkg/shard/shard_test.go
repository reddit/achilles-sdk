package shard

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustParse(t *testing.T, values ...string) *Shard {
	t.Helper()
	s, err := Parse(DefaultKey, values)
	require.NoError(t, err)
	require.NotNil(t, s)
	return s
}

func TestParseEmptyYieldsNoShard(t *testing.T) {
	for name, values := range map[string][]string{
		"nil":          nil,
		"empty slice":  {},
		"blank string": {""},
		"whitespace":   {"  "},
	} {
		t.Run(name, func(t *testing.T) {
			s, err := Parse(DefaultKey, values)
			require.NoError(t, err)
			assert.Nil(t, s, "no values means sharding is disabled")
		})
	}
}

// A value that cannot appear in a label could never match an object, so it is a typo rather than an
// empty shard.
func TestParseRejectsValuesNoObjectCouldCarry(t *testing.T) {
	for _, value := range []string{"a/b", "-leading", strings.Repeat("x", 64), "has space"} {
		_, err := Parse(DefaultKey, []string{value})
		assert.ErrorContains(t, err, "not a valid label value", "value %q must be rejected", value)
	}
}

func TestParseNormalizesValues(t *testing.T) {
	s := mustParse(t, " 1 ", "0", "1", "")

	assert.Equal(t, []string{"0", "1"}, s.Values(), "values are trimmed, deduplicated, and ordered")
	assert.Equal(t, "0,1", s.String())
}

func TestOwns(t *testing.T) {
	s := mustParse(t, "0", "1")

	assert.True(t, s.Owns("0"))
	assert.False(t, s.Owns("2"), "a value this instance was not given is not its work")
	assert.False(t, s.Owns(""), "the absent value cannot be declared, so it is never owned")
}

func TestID(t *testing.T) {
	tests := map[string]struct {
		values []string
		want   string
	}{
		"single value is used verbatim": {[]string{"shard1"}, "shard1"},
		"value set is spelled out":      {[]string{"a", "b"}, "a-b"},
		"tier split":                    {[]string{"0", "1"}, "0-1"},
		"non-dns-safe value is hashed":  {[]string{"Shard_1"}, "h"},
		"long value set is hashed": {
			[]string{"aaaaaaaaaa", "bbbbbbbbbb", "cccccccccc", "dddddddddd", "eeeeeeeeee"}, "h",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := mustParse(t, tc.values...).ID()

			if tc.want == "h" {
				assert.Regexp(t, `^h[0-9a-f]{10}$`, got)
			} else {
				assert.Equal(t, tc.want, got)
			}
		})
	}
}

func TestIDIsOrderIndependent(t *testing.T) {
	assert.Equal(t, mustParse(t, "a", "b").ID(), mustParse(t, "b", "a").ID(),
		"the same values in a different order are the same shard")
}

func TestIDIsDNSSafe(t *testing.T) {
	for _, values := range [][]string{
		{"shard1"},
		{"a", "b"},
		{"Shard_1"},
		{strings.Repeat("x", 63)},
	} {
		assert.Regexp(t, `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, mustParse(t, values...).ID(),
			"ID for %q must be usable in a resource name", values)
	}
}

func TestEqual(t *testing.T) {
	assert.True(t, mustParse(t, "0", "1").Equal(mustParse(t, "1", "0")))
	assert.False(t, mustParse(t, "0", "1").Equal(mustParse(t, "0")))
	assert.False(t, mustParse(t, "0").Equal(nil))
}

func TestOverlaps(t *testing.T) {
	tests := map[string]struct {
		a, b []string
		want bool
	}{
		"identical":               {[]string{"a"}, []string{"a"}, true},
		"disjoint":                {[]string{"a"}, []string{"b"}, false},
		"partial":                 {[]string{"a", "b"}, []string{"b", "c"}, true},
		"disjoint sets":           {[]string{"a", "b"}, []string{"c", "d"}, false},
		"the intended tier split": {[]string{"0", "1"}, []string{"2", "3"}, false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			a, b := mustParse(t, tc.a...), mustParse(t, tc.b...)

			assert.Equal(t, tc.want, a.Overlaps(b))
			assert.Equal(t, tc.want, b.Overlaps(a), "overlap must be symmetric")
		})
	}
}

func TestConflict(t *testing.T) {
	tests := map[string]struct {
		a, b []string
		want string
	}{
		"shared value": {
			[]string{"a", "b"}, []string{"b", "c"}, "the shard value(s) b",
		},
		"several shared values": {
			[]string{"0", "1", "2"}, []string{"1", "2", "3"}, "the shard value(s) 1, 2",
		},
		"disjoint": {
			[]string{"0", "1"}, []string{"2", "3"}, "",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			a, b := mustParse(t, tc.a...), mustParse(t, tc.b...)

			assert.Equal(t, tc.want, a.Conflict(b))
			assert.Equal(t, tc.want, b.Conflict(a), "the description must not depend on argument order")
		})
	}
}

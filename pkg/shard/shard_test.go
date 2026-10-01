package shard

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseWithoutValuesDisablesSharding(t *testing.T) {
	for name, values := range map[string][]string{
		"nil":          nil,
		"empty slice":  {},
		"empty string": {""},
		"whitespace":   {"  "},
	} {
		t.Run(name, func(t *testing.T) {
			s, err := Parse(DefaultKey, values)
			require.NoError(t, err)
			assert.Nil(t, s)
		})
	}
}

// A shard value names a Lease, so a value that cannot be an object name has to be rejected rather
// than silently mangled.
func TestParseRejectsValuesThatCannotNameALease(t *testing.T) {
	for name, value := range map[string]string{
		"uppercase":     "Critical",
		"underscore":    "tier_0",
		"leading dash":  "-0",
		"too long":      "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"missing value": MissingValue,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(DefaultKey, []string{value})
			require.Error(t, err)
			assert.Contains(t, err.Error(), value)
		})
	}
}

func TestParseNormalizesValues(t *testing.T) {
	s, err := Parse(DefaultKey, []string{" b ", "a", "b", ""})
	require.NoError(t, err)

	assert.Equal(t, []string{"a", "b"}, s.Values())
	assert.Equal(t, "a,b", s.String())
	assert.Equal(t, DefaultKey, s.Key())
}

func TestServes(t *testing.T) {
	s, err := Parse(DefaultKey, []string{"a", "b"})
	require.NoError(t, err)

	assert.True(t, s.Serves("a"))
	assert.True(t, s.Serves("b"))
	assert.False(t, s.Serves("c"))
	assert.False(t, s.Serves(""))
	assert.False(t, s.Serves(MissingValue))
}

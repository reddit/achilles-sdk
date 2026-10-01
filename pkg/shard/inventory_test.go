package shard

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/util/sets"
)

func TestInventoryReportsEveryValueDeterministically(t *testing.T) {
	assert.Equal(t,
		[]string{"a", "b"},
		Inventory{Values: sets.New("b", "a")}.reported(),
	)
	assert.Equal(t,
		[]string{"a", MissingValue},
		Inventory{Values: sets.New("a"), Missing: true}.reported(),
	)
}

// An empty label is indistinguishable from an absent one for our purposes: neither can be
// configured on an instance, so neither is reconcilable.
func TestValueOfTreatsAnEmptyLabelAsAbsent(t *testing.T) {
	value, ok := valueOf(labeled("0"), DefaultKey)
	assert.Equal(t, "0", value)
	assert.True(t, ok)

	_, ok = valueOf(labeled(""), DefaultKey)
	assert.False(t, ok)

	_, ok = valueOf(labeled("0"), "other.key/shard")
	assert.False(t, ok)
}

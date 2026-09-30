package shard

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSkipIsNoopWhenShardingIsDisabled(t *testing.T) {
	skip, retry := Skip(context.Background(), obj("0", true))
	assert.False(t, skip)
	assert.False(t, retry)
}

func TestSkipDistinguishesAnotherShardsWorkFromAnUnacquiredValue(t *testing.T) {
	c := testClient()
	o := mustOwner(t, c, "pod-a", "shard.infrared.reddit.com/key in (0)", inventory(false, "0", "1"))
	ctx := NewContext(context.Background(), o)

	// Before the first sync nothing is held, so a selected value is worth revisiting.
	skip, retry := Skip(ctx, obj("0", true))
	assert.True(t, skip)
	assert.True(t, retry, "the value is selected, so ownership is still pending")

	// Another shard's slice will never become ours, so there is nothing to wait for.
	skip, retry = Skip(ctx, obj("1", true))
	assert.True(t, skip)
	assert.False(t, retry)

	require.NoError(t, o.Sync(ctx))

	skip, retry = Skip(ctx, obj("0", true))
	assert.False(t, skip)
	assert.False(t, retry)
}

func TestOwnerFromContextIsNilWhenAbsent(t *testing.T) {
	assert.Nil(t, OwnerFromContext(context.Background()))
}

package shard

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckReportsOwnedWhenShardingIsDisabled(t *testing.T) {
	assert.Equal(t, Owned, Check(context.Background(), obj("0", true)))
}

func TestCheckDistinguishesAnotherShardsWorkFromAnUnacquiredValue(t *testing.T) {
	c := testClient()
	o := mustOwner(t, c, "inst-a", "pod-a", []string{"0"}, inventory(false, "0", "1"))
	ctx := NewContext(context.Background(), o)

	// Before the first sync nothing is held, so a managed value is worth revisiting.
	assert.Equal(t, Pending, Check(ctx, obj("0", true)))

	// A value this instance was not configured with will not become ours without a configuration
	// change, so there is nothing to wait for.
	assert.Equal(t, NotManaged, Check(ctx, obj("1", true)))

	require.NoError(t, o.Sync(ctx))

	assert.Equal(t, Owned, Check(ctx, obj("0", true)))
}

func TestOwnerFromContextIsNilWhenAbsent(t *testing.T) {
	assert.Nil(t, OwnerFromContext(context.Background()))
}

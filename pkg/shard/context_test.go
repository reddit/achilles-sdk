package shard

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// An unsharded controller must reconcile everything it sees.
func TestCheckWithoutAnOwnerOwnsEverything(t *testing.T) {
	assert.Equal(t, Owned, Check(context.Background(), labeled("")))
	assert.Equal(t, Owned, Check(context.Background(), labeled("0")))
}

func TestCheckDefersToTheOwner(t *testing.T) {
	o := testOwner(t, fake.NewClientBuilder().Build(), "inst-a", "pod-a", []string{"0"}, nil)
	require.NoError(t, o.Sync(context.Background()))
	ctx := NewContext(context.Background(), o)

	assert.Equal(t, Owned, Check(ctx, labeled("0")))
	assert.Equal(t, NotManaged, Check(ctx, labeled("1")))
}

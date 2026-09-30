package bootstrap

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/reddit/achilles-sdk/pkg/shard"
)

func mustParse(t *testing.T, raw string) *shard.Shard {
	t.Helper()
	s, err := shard.Parse(shard.DefaultKey, raw)
	require.NoError(t, err)
	return s
}

func shardedOptions() *Options {
	return &Options{
		ShardSelector:    "shard.infrared.reddit.com/key=a",
		ShardedTypes:     []client.Object{&corev1.ConfigMap{}},
		LeaderElectionID: "ctl",
		LeaderElection:   true,
	}
}

func TestResolveShardDisabledByDefault(t *testing.T) {
	s, err := resolveShard(&Options{})
	require.NoError(t, err)
	assert.Nil(t, s)
}

func TestResolveShardRequiresShardedTypes(t *testing.T) {
	opts := shardedOptions()
	opts.ShardedTypes = nil

	_, err := resolveShard(opts)
	assert.ErrorContains(t, err, "sharded-types")
}

func TestResolveShardRequiresLeaderElectionID(t *testing.T) {
	opts := shardedOptions()
	opts.LeaderElectionID = ""

	_, err := resolveShard(opts)
	assert.ErrorContains(t, err, "leader-election-id")
}

// Two replicas of one shard both claiming its values would defeat the point of claiming them, and
// leader election is what prevents it.
func TestResolveShardRequiresLeaderElection(t *testing.T) {
	opts := shardedOptions()
	opts.LeaderElection = false

	_, err := resolveShard(opts)
	assert.ErrorContains(t, err, "leader-election must be enabled")
}

func TestResolveShardRejectsForeignKey(t *testing.T) {
	opts := shardedOptions()
	opts.ShardSelector = "env=prod"

	_, err := resolveShard(opts)
	assert.ErrorContains(t, err, shard.DefaultKey)
}

func TestResolveShardSucceeds(t *testing.T) {
	s, err := resolveShard(shardedOptions())
	require.NoError(t, err)
	require.NotNil(t, s)
	assert.Equal(t, "a", s.ID())
}

func TestShardedLeaderElectionID(t *testing.T) {
	assert.Equal(t, "ctl", shardedLeaderElectionID("ctl", nil))
	assert.Equal(t, "ctl-a", shardedLeaderElectionID("ctl", mustParse(t, "shard.infrared.reddit.com/key=a")))
	assert.Equal(t, "ctl-catchall", shardedLeaderElectionID("ctl", mustParse(t, "!shard.infrared.reddit.com/key")))
}

// Each shard must hold a distinct lock, or exactly one replica across all shards would be active.
func TestShardedLeaderElectionIDsAreDistinct(t *testing.T) {
	a := shardedLeaderElectionID("ctl", mustParse(t, "shard.infrared.reddit.com/key=a"))
	b := shardedLeaderElectionID("ctl", mustParse(t, "shard.infrared.reddit.com/key=b"))
	assert.NotEqual(t, a, b)
}

func TestShardNamespacePrefersExplicitOption(t *testing.T) {
	ns, err := shardNamespace(&Options{LeaderElectionNamespace: "explicit"})
	require.NoError(t, err)
	assert.Equal(t, "explicit", ns)
}

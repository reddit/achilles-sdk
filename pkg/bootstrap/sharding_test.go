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
		ShardSelector:  "shard.infrared.reddit.com/key=a",
		ShardedTypes:   []client.Object{&corev1.ConfigMap{}},
		InstanceName:   "ctl-shard-a",
		LeaderElection: true,
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

// The lock is named after the Deployment, so a separately configured lock name would be a second
// source of truth able to disagree with it.
func TestResolveShardRejectsLeaderElectionID(t *testing.T) {
	opts := shardedOptions()
	opts.LeaderElectionID = "ctl"

	_, err := resolveShard(opts)
	assert.ErrorContains(t, err, "leader-election-id must not be set")
}

func TestResolveShardRequiresInstanceName(t *testing.T) {
	opts := shardedOptions()
	opts.InstanceName = ""

	_, err := resolveShard(opts)
	assert.ErrorContains(t, err, "instance-name")
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

func TestLeaderElectionIDUsesTheConfiguredLockWhenUnsharded(t *testing.T) {
	assert.Equal(t, "ctl", leaderElectionID(&Options{LeaderElectionID: "ctl"}, nil))
}

// Each shard is its own Deployment, so its lock is that Deployment's name. Kubernetes already
// guarantees those are distinct in a namespace, which no derivation from the selector can.
func TestLeaderElectionIDUsesTheInstanceNameWhenSharded(t *testing.T) {
	opts := shardedOptions()
	assert.Equal(t, "ctl-shard-a", leaderElectionID(opts, mustParse(t, opts.ShardSelector)))
}

// Editing a selector must not move the lock, or the outgoing and incoming pods of one shard would
// briefly hold different locks and both act as leader.
func TestLeaderElectionIDIsIndependentOfTheSelector(t *testing.T) {
	opts := shardedOptions()
	before := leaderElectionID(opts, mustParse(t, "shard.infrared.reddit.com/key=a"))
	after := leaderElectionID(opts, mustParse(t, "shard.infrared.reddit.com/key in (a,b)"))
	assert.Equal(t, before, after)
}

func TestShardNamespacePrefersExplicitOption(t *testing.T) {
	ns, err := shardNamespace(&Options{LeaderElectionNamespace: "explicit"})
	require.NoError(t, err)
	assert.Equal(t, "explicit", ns)
}

package bootstrap

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/reddit/achilles-sdk/pkg/shard"
)

func shardScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	return scheme
}

func mustParse(t *testing.T, raw string) *shard.Shard {
	t.Helper()
	s, err := shard.Parse(shard.DefaultKey, raw)
	require.NoError(t, err)
	return s
}

func TestResolveShardDisabledByDefault(t *testing.T) {
	s, err := resolveShard(&Options{})
	require.NoError(t, err)
	assert.Nil(t, s)
}

func TestResolveShardRequiresShardedTypes(t *testing.T) {
	_, err := resolveShard(&Options{
		WatchLabelSelector: "shard.infrared.reddit.com/key=a",
		LeaderElectionID:   "ctl",
	})
	assert.ErrorContains(t, err, "sharded-types")
}

func TestResolveShardRequiresLeaderElectionID(t *testing.T) {
	_, err := resolveShard(&Options{
		WatchLabelSelector: "shard.infrared.reddit.com/key=a",
		ShardedTypes:       []client.Object{&corev1.ConfigMap{}},
	})
	assert.ErrorContains(t, err, "leader-election-id")
}

func TestResolveShardRejectsForeignKey(t *testing.T) {
	_, err := resolveShard(&Options{
		WatchLabelSelector: "env=prod",
		ShardedTypes:       []client.Object{&corev1.ConfigMap{}},
		LeaderElectionID:   "ctl",
	})
	assert.ErrorContains(t, err, shard.DefaultKey)
}

func TestResolveShardSucceeds(t *testing.T) {
	s, err := resolveShard(&Options{
		WatchLabelSelector: "shard.infrared.reddit.com/key=a",
		ShardedTypes:       []client.Object{&corev1.ConfigMap{}},
		LeaderElectionID:   "ctl",
	})
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

func TestApplyShardToCacheNoopWhenDisabled(t *testing.T) {
	in := cache.Options{}
	out, err := applyShardToCache(in, shardScheme(t), nil, []client.Object{&corev1.ConfigMap{}})
	require.NoError(t, err)
	assert.Nil(t, out.ByObject)
}

func TestApplyShardToCacheFiltersShardedTypes(t *testing.T) {
	out, err := applyShardToCache(
		cache.Options{},
		shardScheme(t),
		mustParse(t, "shard.infrared.reddit.com/key=a"),
		[]client.Object{&corev1.ConfigMap{}},
	)
	require.NoError(t, err)
	require.Len(t, out.ByObject, 1)

	for _, opts := range out.ByObject {
		require.NotNil(t, opts.Label)
		assert.True(t, opts.Label.Matches(labels.Set{shard.DefaultKey: "a"}))
		assert.False(t, opts.Label.Matches(labels.Set{shard.DefaultKey: "b"}))
		assert.False(t, opts.Label.Matches(labels.Set{}), "unlabeled objects belong to the catch-all shard")
	}
}

func TestApplyShardToCacheLeavesOtherTypesUnfiltered(t *testing.T) {
	out, err := applyShardToCache(
		cache.Options{ByObject: map[client.Object]cache.ByObject{
			&appsv1.Deployment{}: {},
		}},
		shardScheme(t),
		mustParse(t, "shard.infrared.reddit.com/key=a"),
		[]client.Object{&corev1.ConfigMap{}},
	)
	require.NoError(t, err)
	require.Len(t, out.ByObject, 2)

	for obj, opts := range out.ByObject {
		if _, ok := obj.(*appsv1.Deployment); ok {
			assert.Nil(t, opts.Label, "types the caller did not declare as sharded stay unfiltered")
		}
	}
}

// A caller may already scope a type; the shard must narrow that rather than replace it.
func TestApplyShardToCacheComposesWithExistingSelector(t *testing.T) {
	existing := labels.SelectorFromSet(labels.Set{"tier": "gold"})
	out, err := applyShardToCache(
		cache.Options{ByObject: map[client.Object]cache.ByObject{
			&corev1.ConfigMap{}: {Label: existing},
		}},
		shardScheme(t),
		mustParse(t, "shard.infrared.reddit.com/key=a"),
		[]client.Object{&corev1.ConfigMap{}},
	)
	require.NoError(t, err)
	require.Len(t, out.ByObject, 1, "the sharded type must reuse the caller's existing entry, not duplicate its GVK")

	for _, opts := range out.ByObject {
		assert.True(t, opts.Label.Matches(labels.Set{"tier": "gold", shard.DefaultKey: "a"}))
		assert.False(t, opts.Label.Matches(labels.Set{"tier": "gold"}), "shard requirement must still apply")
		assert.False(t, opts.Label.Matches(labels.Set{shard.DefaultKey: "a"}), "caller's requirement must still apply")
	}
}

func TestShardNamespacePrefersExplicitOption(t *testing.T) {
	ns, err := shardNamespace(&Options{LeaderElectionNamespace: "explicit"})
	require.NoError(t, err)
	assert.Equal(t, "explicit", ns)
}

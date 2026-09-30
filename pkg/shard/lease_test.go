package shard

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	coordinationv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const testGroup = "test-controller"

func testClient(objs ...client.Object) client.Client {
	scheme := runtime.NewScheme()
	if err := coordinationv1.AddToScheme(scheme); err != nil {
		panic(err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

func peerLease(name, shardID, selector string, renewedAgo time.Duration) *coordinationv1.Lease {
	renew := metav1.NewMicroTime(time.Now().Add(-renewedAgo))
	holder := "peer-pod"
	duration := int32(15)

	return &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "ns",
			Labels: map[string]string{
				GroupLabelKey: testGroup,
				IDLabelKey:    shardID,
				KindLabelKey:  kindAdvertisement,
			},
			Annotations: map[string]string{SelectorAnnotationKey: selector},
		},
		Spec: coordinationv1.LeaseSpec{
			HolderIdentity:       &holder,
			RenewTime:            &renew,
			LeaseDurationSeconds: &duration,
		},
	}
}

func advertiser(c client.Client, selector string) (*Advertiser, error) {
	s, err := Parse(DefaultKey, selector)
	if err != nil {
		return nil, err
	}
	return NewAdvertiser(c, Config{
		Shard:     s,
		Group:     testGroup,
		Namespace: "ns",
		Identity:  "self-pod",
	}), nil
}

func TestAdvertiseSucceedsWithNoPeers(t *testing.T) {
	c := testClient()
	a, err := advertiser(c, "shard.infrared.reddit.com/key=a")
	require.NoError(t, err)

	require.NoError(t, a.Advertise(context.Background()))

	var lease coordinationv1.Lease
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: "ns", Name: a.LeaseName()}, &lease))
	assert.Equal(t, "shard.infrared.reddit.com/key=a", lease.Annotations[SelectorAnnotationKey])
	assert.Equal(t, "self-pod", *lease.Spec.HolderIdentity)
}

func TestAdvertiseFailsOnOverlappingPeer(t *testing.T) {
	c := testClient(peerLease("peer", "h123", "shard.infrared.reddit.com/key in (a,b)", time.Second))
	a, err := advertiser(c, "shard.infrared.reddit.com/key=a")
	require.NoError(t, err)

	overlap, err := a.Overlap(context.Background())
	require.NoError(t, err)
	assert.Contains(t, overlap, "overlaps")
}

func TestAdvertiseIgnoresDisjointPeer(t *testing.T) {
	c := testClient(peerLease("peer", "b", "shard.infrared.reddit.com/key=b", time.Second))
	a, err := advertiser(c, "shard.infrared.reddit.com/key=a")
	require.NoError(t, err)

	overlap, err := a.Overlap(context.Background())
	require.NoError(t, err)
	assert.Empty(t, overlap)
}

// A rolling update runs two pods of the same shard at once, which must not be treated as a conflict.
func TestAdvertiseAllowsSameShardPeer(t *testing.T) {
	c := testClient(peerLease("test-controller-shard-a", "a", "shard.infrared.reddit.com/key=a", time.Second))
	a, err := advertiser(c, "shard.infrared.reddit.com/key=a")
	require.NoError(t, err)

	require.NoError(t, a.Advertise(context.Background()))

	var lease coordinationv1.Lease
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: "ns", Name: a.LeaseName()}, &lease))
	assert.Equal(t, "self-pod", *lease.Spec.HolderIdentity, "the incoming pod takes over the shard's lease")
}

// Readable IDs can collide: "key in (not,0,1)" and "key notin (0,1)" both render as "not-0-1".
// Treating that as the same shard would silently disable the check, so the selector decides.
func TestAdvertiseFailsOnSameIDWithDifferentSelector(t *testing.T) {
	c := testClient(peerLease("test-controller-shard-not-0-1", "not-0-1",
		"shard.infrared.reddit.com/key in (not,0,1)", time.Second))
	a, err := advertiser(c, "shard.infrared.reddit.com/key notin (0,1)")
	require.NoError(t, err)
	require.Equal(t, "not-0-1", a.cfg.Shard.ID(), "precondition: the IDs collide")

	overlap, err := a.Overlap(context.Background())
	require.NoError(t, err)
	assert.Contains(t, overlap, "different selector")
}

// Introducing a new shard value means carving it out of whichever live shard currently absorbs it,
// and a negated shard absorbs every value it does not exclude. Both the narrowed shard and the new
// one therefore overlap the shard still running, so neither can be rolled out underneath it.
func TestAdvertiseFailsWhenCarvingOutOfALiveShard(t *testing.T) {
	for name, selector := range map[string]string{
		"the new shard itself":        "shard.infrared.reddit.com/key=2",
		"narrowing the negated shard": "shard.infrared.reddit.com/key notin (0,1,2)",
		"widening the negated shard":  "shard.infrared.reddit.com/key notin (0)",
	} {
		t.Run(name, func(t *testing.T) {
			live := peerLease("test-controller-shard-not-0-1", "not-0-1",
				"shard.infrared.reddit.com/key notin (0,1)", time.Second)
			a, err := advertiser(testClient(live), selector)
			require.NoError(t, err)

			overlap, err := a.Overlap(context.Background())
			require.NoError(t, err)
			assert.Contains(t, overlap, "overlaps")
		})
	}
}

func TestAdvertiseIgnoresStalePeer(t *testing.T) {
	c := testClient(peerLease("peer", "h123", "shard.infrared.reddit.com/key in (a,b)", time.Hour))
	a, err := advertiser(c, "shard.infrared.reddit.com/key=a")
	require.NoError(t, err)

	overlap, err := a.Overlap(context.Background())
	require.NoError(t, err)
	assert.Empty(t, overlap, "a peer that stopped renewing is gone")
}

func TestAdvertiseIgnoresOtherGroups(t *testing.T) {
	other := peerLease("peer", "h123", "shard.infrared.reddit.com/key in (a,b)", time.Second)
	other.Labels[GroupLabelKey] = "a-different-controller"
	c := testClient(other)

	a, err := advertiser(c, "shard.infrared.reddit.com/key=a")
	require.NoError(t, err)

	overlap, err := a.Overlap(context.Background())
	require.NoError(t, err)
	assert.Empty(t, overlap, "a different controller's shards are unrelated")
}

func TestRenewUpdatesRenewTime(t *testing.T) {
	c := testClient()
	a, err := advertiser(c, "shard.infrared.reddit.com/key=a")
	require.NoError(t, err)
	require.NoError(t, a.Advertise(context.Background()))

	var before coordinationv1.Lease
	key := client.ObjectKey{Namespace: "ns", Name: a.LeaseName()}
	require.NoError(t, c.Get(context.Background(), key, &before))

	time.Sleep(10 * time.Millisecond)
	require.NoError(t, a.renew(context.Background()))

	var after coordinationv1.Lease
	require.NoError(t, c.Get(context.Background(), key, &after))
	assert.True(t, after.Spec.RenewTime.After(before.Spec.RenewTime.Time), "renewal must advance renewTime")
}

func TestLeaseNameIsScopedToGroupAndShard(t *testing.T) {
	a, err := advertiser(testClient(), "shard.infrared.reddit.com/key=a")
	require.NoError(t, err)
	assert.Equal(t, "test-controller-shard-a", a.LeaseName())

	b, err := advertiser(testClient(), "!shard.infrared.reddit.com/key")
	require.NoError(t, err)
	assert.Equal(t, "test-controller-shard-catchall", b.LeaseName())
}

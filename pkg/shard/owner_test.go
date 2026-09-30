package shard

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func inventory(unlabeled bool, values ...string) ListFunc {
	return func(context.Context) (Inventory, error) {
		return Inventory{Values: sets.New(values...), Unlabeled: unlabeled}, nil
	}
}

func owner(c client.Client, identity, selector string, list ListFunc) (*Owner, error) {
	s, err := Parse(DefaultKey, selector)
	if err != nil {
		return nil, err
	}
	return NewOwner(c, OwnerConfig{
		Shard:     s,
		Group:     testGroup,
		Namespace: "ns",
		Identity:  identity,
		List:      list,
	}), nil
}

func mustOwner(t *testing.T, c client.Client, identity, selector string, list ListFunc) *Owner {
	t.Helper()
	o, err := owner(c, identity, selector, list)
	require.NoError(t, err)
	return o
}

// holders maps each value to the identity currently holding its lease, "" when unheld.
func holders(t *testing.T, c client.Client) map[string]string {
	t.Helper()

	var leases coordinationv1.LeaseList
	require.NoError(t, c.List(context.Background(), &leases,
		client.InNamespace("ns"),
		client.MatchingLabels{GroupLabelKey: testGroup, KindLabelKey: kindValue},
	))

	out := map[string]string{}
	for i := range leases.Items {
		l := &leases.Items[i]
		out[l.Annotations[ValueAnnotationKey]] = heldBy(l)
	}
	return out
}

// valueLease builds an ownership Lease as some other process would have left it.
func valueLease(value, shardID, identity string, renewedAgo time.Duration) *coordinationv1.Lease {
	renew := metav1.NewMicroTime(time.Now().Add(-renewedAgo))
	duration := int32(15)

	return &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      valueLeaseName(testGroup, value, false),
			Namespace: "ns",
			Labels: map[string]string{
				GroupLabelKey: testGroup,
				KindLabelKey:  kindValue,
				IDLabelKey:    shardID,
			},
			Annotations: map[string]string{ValueAnnotationKey: value},
		},
		Spec: coordinationv1.LeaseSpec{
			HolderIdentity:       &identity,
			RenewTime:            &renew,
			LeaseDurationSeconds: &duration,
		},
	}
}

func obj(value string, labelled bool) client.Object {
	o := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "o", Namespace: "ns"}}
	if labelled {
		o.SetLabels(map[string]string{DefaultKey: value})
	}
	return o
}

// The scenario the selector-only safeguard could not handle: two shards that exclude disjoint
// values both select a value neither anticipated, so both would reconcile it. Exactly one may end
// up holding its lease.
func TestNewValueIsClaimedByExactlyOneOverlappingShard(t *testing.T) {
	c := testClient()
	list := inventory(false, "0", "1", "2", "3")

	a := mustOwner(t, c, "pod-a", "shard.infrared.reddit.com/key notin (0,1)", list)
	b := mustOwner(t, c, "pod-b", "shard.infrared.reddit.com/key notin (2,3)", list)

	require.NoError(t, a.Sync(context.Background()))
	require.NoError(t, b.Sync(context.Background()))

	require.Equal(t, "pod-a", holders(t, c)["2"], "precondition: a owns the values b excludes")
	require.Equal(t, "pod-b", holders(t, c)["0"], "precondition: b owns the values a excludes")

	// A fourth value appears. Both selectors match it.
	list = inventory(false, "0", "1", "2", "3", "4")
	a = mustOwner(t, c, "pod-a", "shard.infrared.reddit.com/key notin (0,1)", list)
	b = mustOwner(t, c, "pod-b", "shard.infrared.reddit.com/key notin (2,3)", list)

	require.NoError(t, a.Sync(context.Background()))
	require.NoError(t, b.Sync(context.Background()))

	assert.Equal(t, "pod-a", holders(t, c)["4"], "the first to acquire keeps it")
	assert.Equal(t, Owned, a.Ownership(obj("4", true)))
	assert.Equal(t, Pending, b.Ownership(obj("4", true)),
		"b still selects value 4 but must not reconcile it while a holds the lease")
}

func TestSyncAcquiresOnlySelectedValues(t *testing.T) {
	c := testClient()
	o := mustOwner(t, c, "pod-a", "shard.infrared.reddit.com/key in (0,1)", inventory(false, "0", "1", "2", "3"))

	require.NoError(t, o.Sync(context.Background()))

	assert.Equal(t, map[string]string{"0": "pod-a", "1": "pod-a", "2": "", "3": ""}, holders(t, c),
		"a lease exists for every value, but only the selected ones are held")
}

func TestSyncConvergesLeaseSetOntoValuesInUse(t *testing.T) {
	c := testClient()
	o := mustOwner(t, c, "pod-a", "shard.infrared.reddit.com/key in (0,1)", inventory(false, "0", "1"))
	require.NoError(t, o.Sync(context.Background()))
	require.Contains(t, holders(t, c), "1")

	// Value 1 falls out of use.
	o = mustOwner(t, c, "pod-a", "shard.infrared.reddit.com/key in (0,1)", inventory(false, "0"))
	require.NoError(t, o.Sync(context.Background()))

	assert.Equal(t, map[string]string{"0": "pod-a"}, holders(t, c),
		"a lease for a value no object carries is garbage")
}

func TestSyncClaimsTheUnlabeledSentinel(t *testing.T) {
	c := testClient()
	catchAll := mustOwner(t, c, "pod-catchall", "!shard.infrared.reddit.com/key", inventory(true, "0"))
	require.NoError(t, catchAll.Sync(context.Background()))

	assert.Equal(t, Owned, catchAll.Ownership(obj("", false)))
	assert.Equal(t, NotSelected, catchAll.Ownership(obj("0", true)))
}

func TestSyncDoesNotStealALiveLeaseFromAnotherShard(t *testing.T) {
	c := testClient()
	list := inventory(false, "0", "1")

	held := mustOwner(t, c, "pod-a", "shard.infrared.reddit.com/key in (0)", list)
	require.NoError(t, held.Sync(context.Background()))

	// A different shard, misconfigured to also select value 0.
	thief := mustOwner(t, c, "pod-b", "shard.infrared.reddit.com/key in (0,1)", list)
	require.NoError(t, thief.Sync(context.Background()))

	assert.Equal(t, "pod-a", holders(t, c)["0"], "the incumbent keeps a live lease")
	assert.Equal(t, "pod-b", holders(t, c)["1"], "the uncontested value is still served")
	assert.Equal(t, Pending, thief.Ownership(obj("0", true)))
}

// Two instances carrying the same selector are replicas of one shard, not rivals, so the incoming
// one takes the outgoing one's values rather than waiting out their expiry.
func TestSyncTreatsAnIdenticalSelectorAsTheSameShard(t *testing.T) {
	c := testClient()
	list := inventory(false, "0")

	outgoing := mustOwner(t, c, "pod-a", "shard.infrared.reddit.com/key in (0)", list)
	require.NoError(t, outgoing.Sync(context.Background()))

	incoming := mustOwner(t, c, "pod-b", "shard.infrared.reddit.com/key in (0)", list)
	require.NoError(t, incoming.Sync(context.Background()))

	assert.Equal(t, "pod-b", holders(t, c)["0"])
}

func TestSyncTakesOverAnExpiredLease(t *testing.T) {
	c := testClient()
	stale := valueLease("0", "0", "dead-pod", time.Hour)
	require.NoError(t, c.Create(context.Background(), stale))

	o := mustOwner(t, c, "pod-b", "shard.infrared.reddit.com/key in (0)", inventory(false, "0"))
	require.NoError(t, o.Sync(context.Background()))

	assert.Equal(t, "pod-b", holders(t, c)["0"], "a holder that stopped renewing has gone")
}

// On leader failover the outgoing leader's value leases are still live, but only one leader of a
// shard exists at a time, so the incoming leader may take them over without waiting out expiry.
func TestSyncTakesOverItsOwnShardsLeaseImmediately(t *testing.T) {
	c := testClient()
	live := valueLease("0", "0", "previous-leader", time.Second)
	require.NoError(t, c.Create(context.Background(), live))

	o := mustOwner(t, c, "new-leader", "shard.infrared.reddit.com/key in (0)", inventory(false, "0"))
	require.NoError(t, o.Sync(context.Background()))

	assert.Equal(t, "new-leader", holders(t, c)["0"])
}

func TestSyncReleasesValuesThatFallOutOfTheSelector(t *testing.T) {
	c := testClient()
	wide := mustOwner(t, c, "pod-a", "shard.infrared.reddit.com/key in (0,1)", inventory(false, "0", "1"))
	require.NoError(t, wide.Sync(context.Background()))
	require.Equal(t, "pod-a", holders(t, c)["1"])

	narrow := mustOwner(t, c, "pod-a", "shard.infrared.reddit.com/key in (0)", inventory(false, "0", "1"))
	require.NoError(t, narrow.Sync(context.Background()))

	assert.Equal(t, "", holders(t, c)["1"], "a released value must be free for another shard at once")
	assert.Equal(t, NotSelected, narrow.Ownership(obj("1", true)))
}

func TestOwnershipBeforeFirstSyncIsPendingNotOwned(t *testing.T) {
	o := mustOwner(t, testClient(), "pod-a", "shard.infrared.reddit.com/key in (0)", inventory(false, "0"))

	assert.Equal(t, Pending, o.Ownership(obj("0", true)),
		"reconciling before any lease is held would defeat the point of holding one")
	assert.Equal(t, NotSelected, o.Ownership(obj("9", true)))
}

func TestSyncIgnoresOtherGroupsLeases(t *testing.T) {
	c := testClient()
	foreign := valueLease("0", "0", "other-controller-pod", time.Second)
	foreign.Labels[GroupLabelKey] = "a-different-controller"
	require.NoError(t, c.Create(context.Background(), foreign))

	o := mustOwner(t, c, "pod-a", "shard.infrared.reddit.com/key in (0)", inventory(false, "0"))
	require.NoError(t, o.Sync(context.Background()))

	assert.Equal(t, "pod-a", holders(t, c)["0"], "another controller's leases are unrelated")
}

func TestValueLeaseNamesAreReadableWhenTheyCanBe(t *testing.T) {
	assert.Equal(t, testGroup+"-value-0", valueLeaseName(testGroup, "0", false))
	assert.Equal(t, testGroup+"-value-tier-one", valueLeaseName(testGroup, "tier-one", false))
	assert.Equal(t, testGroup+"-unlabeled", valueLeaseName(testGroup, "", true))
}

// Label values admit characters object names do not, so anything unusable must still yield a name.
func TestValueLeaseNamesAreAlwaysValidObjectNames(t *testing.T) {
	for _, value := range []string{"0", "tier-one", "Tier_One", "", "a.b.c", strings.Repeat("x", 63)} {
		got := valueLeaseName(testGroup, value, false)
		assert.True(t, isDNSSubdomain(got), "value %q yielded invalid name %q", value, got)
	}
}

func TestSentinelCannotCollideWithALiteralValue(t *testing.T) {
	assert.NotEqual(t, valueLeaseName(testGroup, "unlabeled", false), valueLeaseName(testGroup, "", true))
}

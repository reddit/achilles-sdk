package shard

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
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

// mustOwner builds an Owner for one pod (identity) of one Deployment (instance).
func mustOwner(t *testing.T, c client.Client, instance, identity string, values []string, list ListFunc) *Owner {
	t.Helper()

	s, err := Parse(DefaultKey, values)
	require.NoError(t, err)
	require.NotNil(t, s)

	return NewOwner(c, OwnerConfig{
		Shard:     s,
		Group:     testGroupID,
		Instance:  instance,
		Namespace: "ns",
		Identity:  identity,
		List:      list,
	})
}

// holders maps each value with a lease to the identity holding it, "" when unheld. A value absent
// from the map has no lease at all, which is how an unmanaged value looks.
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
func valueLease(value, instance, identity string, renewedAgo time.Duration) *coordinationv1.Lease {
	renew := metav1.NewMicroTime(time.Now().Add(-renewedAgo))
	duration := int32(15)

	return &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      valueLeaseName(testGroup, value),
			Namespace: "ns",
			Labels: map[string]string{
				GroupLabelKey:    testGroup,
				KindLabelKey:     kindValue,
				InstanceLabelKey: instance,
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

func ignoredMetric(t *testing.T, o *Owner, value string) float64 {
	t.Helper()
	return testutil.ToFloat64(valuesIgnored.WithLabelValues(
		o.cfg.Group.Name, o.cfg.Shard.ID(), value))
}

// The defining behaviour of configured sharding: a value nobody was configured with is reconciled
// by nobody, and says so, rather than being assigned to whichever instance happens to claim it.
func TestNewValueIsIgnoredUntilAnInstanceIsConfiguredWithIt(t *testing.T) {
	c := testClient()
	list := inventory(false, "0", "1", "2", "3")

	a := mustOwner(t, c, "inst-a", "pod-a", []string{"0", "1"}, list)
	b := mustOwner(t, c, "inst-b", "pod-b", []string{"2", "3"}, list)
	require.NoError(t, a.Sync(context.Background()))
	require.NoError(t, b.Sync(context.Background()))
	require.Equal(t, "pod-a", holders(t, c)["0"], "precondition: the configured values are served")

	// A fourth value appears that neither instance was configured with.
	list = inventory(false, "0", "1", "2", "3", "4")
	a = mustOwner(t, c, "inst-a", "pod-a", []string{"0", "1"}, list)
	b = mustOwner(t, c, "inst-b", "pod-b", []string{"2", "3"}, list)
	require.NoError(t, a.Sync(context.Background()))
	require.NoError(t, b.Sync(context.Background()))

	assert.NotContains(t, holders(t, c), "4", "an unmanaged value gets no lease")
	assert.Equal(t, NotManaged, a.Ownership(obj("4", true)))
	assert.Equal(t, NotManaged, b.Ownership(obj("4", true)))

	// Every instance reporting 1 is what makes "ignored by all" answerable without counting pods.
	assert.Equal(t, 1.0, ignoredMetric(t, a, "4"))
	assert.Equal(t, 1.0, ignoredMetric(t, b, "4"))
	assert.Equal(t, 0.0, ignoredMetric(t, a, "0"), "a value this instance manages is not ignored")

	// Configuring an instance with it is what assigns it.
	a = mustOwner(t, c, "inst-a", "pod-a", []string{"0", "1", "4"}, list)
	require.NoError(t, a.Sync(context.Background()))

	assert.Equal(t, "pod-a", holders(t, c)["4"])
	assert.Equal(t, Owned, a.Ownership(obj("4", true)))
	assert.Equal(t, 0.0, ignoredMetric(t, a, "4"))
}

func TestSyncAcquiresExactlyTheConfiguredValues(t *testing.T) {
	c := testClient()
	o := mustOwner(t, c, "inst-a", "pod-a", []string{"0", "1"}, inventory(false, "0", "1", "2", "3"))

	require.NoError(t, o.Sync(context.Background()))

	assert.Equal(t, map[string]string{"0": "pod-a", "1": "pod-a"}, holders(t, c),
		"leases exist for the configured values and for nothing else")
}

// Ownership is settled from configuration, so it does not wait for an object to turn up. This is
// what removes the window in which a value's first object could be reconciled by two instances.
func TestSyncHoldsAConfiguredValueNoObjectCarriesYet(t *testing.T) {
	c := testClient()
	o := mustOwner(t, c, "inst-a", "pod-a", []string{"0", "9"}, inventory(false, "0"))

	require.NoError(t, o.Sync(context.Background()))

	assert.Equal(t, "pod-a", holders(t, c)["9"])
	assert.Equal(t, Owned, o.Ownership(obj("9", true)))
}

// An object carrying no shard value cannot be declared on any instance, so it is reconcilable by
// nobody and must be reported rather than silently dropped.
func TestObjectsWithNoShardValueAreNeverOwnedAndAlwaysReported(t *testing.T) {
	c := testClient()
	o := mustOwner(t, c, "inst-a", "pod-a", []string{"0"}, inventory(true, "0"))
	require.NoError(t, o.Sync(context.Background()))

	assert.Equal(t, NotManaged, o.Ownership(obj("", false)), "no label")
	assert.Equal(t, NotManaged, o.Ownership(obj("", true)), "an empty label value is the same case")
	assert.Equal(t, 1.0, ignoredMetric(t, o, MissingValue))
	assert.NotContains(t, holders(t, c), MissingValue, "and gets no lease")
}

// Two Deployments misconfigured with one value is the case the lease exists for: both manage it, so
// both try, and the loser must defer rather than duplicate the work.
func TestSyncDoesNotStealALiveLeaseFromAnotherInstance(t *testing.T) {
	c := testClient()
	list := inventory(false, "0", "1")

	held := mustOwner(t, c, "inst-a", "pod-a", []string{"0"}, list)
	require.NoError(t, held.Sync(context.Background()))

	thief := mustOwner(t, c, "inst-b", "pod-b", []string{"0", "1"}, list)
	require.NoError(t, thief.Sync(context.Background()))

	assert.Equal(t, "pod-a", holders(t, c)["0"], "the incumbent keeps a live lease")
	assert.Equal(t, "pod-b", holders(t, c)["1"], "the uncontested value is still served")
	assert.Equal(t, Pending, thief.Ownership(obj("0", true)))
}

func TestSyncTakesOverAnExpiredLease(t *testing.T) {
	c := testClient()
	require.NoError(t, c.Create(context.Background(), valueLease("0", "inst-dead", "dead-pod", time.Hour)))

	o := mustOwner(t, c, "inst-b", "pod-b", []string{"0"}, inventory(false, "0"))
	require.NoError(t, o.Sync(context.Background()))

	assert.Equal(t, "pod-b", holders(t, c)["0"], "a holder that stopped renewing has gone")
}

// On leader failover the outgoing leader's value leases are still live, but leader election runs per
// instance, so the incoming leader of that same instance may take them over without waiting out
// expiry.
func TestSyncTakesOverItsOwnInstancesLeaseImmediately(t *testing.T) {
	c := testClient()
	require.NoError(t, c.Create(context.Background(), valueLease("0", "inst-a", "previous-leader", time.Second)))

	o := mustOwner(t, c, "inst-a", "new-leader", []string{"0"}, inventory(false, "0"))
	require.NoError(t, o.Sync(context.Background()))

	assert.Equal(t, "new-leader", holders(t, c)["0"])
}

// Keying takeover on the instance rather than the shard is what makes editing --shard-values a
// clean rolling update: the incoming pod's shard differs, but it is the same Deployment.
func TestSyncTakesOverItsOwnInstancesLeaseAfterAValuesEdit(t *testing.T) {
	c := testClient()
	list := inventory(false, "0", "1")

	before := mustOwner(t, c, "inst-a", "pod-a", []string{"0"}, list)
	require.NoError(t, before.Sync(context.Background()))
	require.NotEqual(t, before.Shard().ID(), "0-1", "precondition: the edit changes the shard ID")

	after := mustOwner(t, c, "inst-a", "pod-b", []string{"0", "1"}, list)
	require.NoError(t, after.Sync(context.Background()))

	assert.Equal(t, map[string]string{"0": "pod-b", "1": "pod-b"}, holders(t, c))
}

func TestSyncReleasesValuesRemovedFromTheConfiguration(t *testing.T) {
	c := testClient()
	wide := mustOwner(t, c, "inst-a", "pod-a", []string{"0", "1"}, inventory(false, "0", "1"))
	require.NoError(t, wide.Sync(context.Background()))
	require.Equal(t, "pod-a", holders(t, c)["1"])

	narrow := mustOwner(t, c, "inst-a", "pod-a", []string{"0"}, inventory(false, "0", "1"))
	require.NoError(t, narrow.Sync(context.Background()))

	assert.Equal(t, "", holders(t, c)["1"], "a released value must be free for another instance at once")
	assert.Equal(t, NotManaged, narrow.Ownership(obj("1", true)))
	assert.Equal(t, 1.0, ignoredMetric(t, narrow, "1"), "and is now ignored by this instance")
}

// A lease for a value that is neither configured nor carried by any object is garbage; whichever
// instance is configured with it recreates it.
func TestSyncDeletesLeasesForValuesNeitherManagedNorInUse(t *testing.T) {
	c := testClient()
	wide := mustOwner(t, c, "inst-a", "pod-a", []string{"0", "1"}, inventory(false, "0", "1"))
	require.NoError(t, wide.Sync(context.Background()))

	// Value 1 is dropped from the configuration and falls out of use. Two syncs: the first
	// releases it, the second finds it unheld and unused.
	narrow := mustOwner(t, c, "inst-a", "pod-a", []string{"0"}, inventory(false, "0"))
	require.NoError(t, narrow.Sync(context.Background()))
	require.NoError(t, narrow.Sync(context.Background()))

	assert.Equal(t, map[string]string{"0": "pod-a"}, holders(t, c))
}

func TestOwnershipBeforeFirstSyncIsPendingNotOwned(t *testing.T) {
	o := mustOwner(t, testClient(), "inst-a", "pod-a", []string{"0"}, inventory(false, "0"))

	assert.Equal(t, Pending, o.Ownership(obj("0", true)),
		"reconciling before any lease is held would defeat the point of holding one")
	assert.Equal(t, NotManaged, o.Ownership(obj("9", true)))
}

// Another controller partitioning different types shares the namespace but not the group, so its
// value 0 and ours are different partitions. Both the Lease name and the group label differ,
// because the name is prefixed with the group.
func TestSyncIgnoresOtherGroupsLeases(t *testing.T) {
	c := testClient()
	foreign := valueLease("0", "inst-other", "other-controller-pod", time.Second)
	foreign.Name = valueLeaseName("othertype", "0")
	foreign.Labels[GroupLabelKey] = "othertype"
	require.NoError(t, c.Create(context.Background(), foreign))

	o := mustOwner(t, c, "inst-a", "pod-a", []string{"0"}, inventory(false, "0"))
	require.NoError(t, o.Sync(context.Background()))

	assert.Equal(t, "pod-a", holders(t, c)["0"], "another controller's leases are unrelated")

	var untouched coordinationv1.Lease
	require.NoError(t, c.Get(context.Background(),
		client.ObjectKey{Namespace: "ns", Name: foreign.Name}, &untouched))
	assert.Equal(t, "other-controller-pod", heldBy(&untouched), "and must not be pruned either")
}

// Only the leader of an instance may hold its values; a standby replica holding work it will not do
// would strand that work.
func TestOwnerIsLeaderGated(t *testing.T) {
	o := mustOwner(t, testClient(), "inst-a", "pod-a", []string{"0"}, inventory(false, "0"))
	assert.True(t, o.NeedLeaderElection())
}

func TestValueLeaseNamesAreReadableWhenTheyCanBe(t *testing.T) {
	assert.Equal(t, testGroup+"-value-0", valueLeaseName(testGroup, "0"))
	assert.Equal(t, testGroup+"-value-tier-one", valueLeaseName(testGroup, "tier-one"))
}

// Label values admit characters object names do not, so anything unusable must still yield a name.
func TestValueLeaseNamesAreAlwaysValidObjectNames(t *testing.T) {
	for _, value := range []string{"0", "tier-one", "Tier_One", "", "a.b.c", strings.Repeat("x", 63)} {
		got := valueLeaseName(testGroup, value)
		assert.True(t, isDNSSubdomain(got), "value %q yielded invalid name %q", value, got)
	}
}

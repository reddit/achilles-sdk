package shard

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

const testNamespace = "ns"

func testOwner(t *testing.T, c client.Client, instance, identity string, values []string, list ListFunc) *Owner {
	t.Helper()

	s, err := Parse(DefaultKey, values)
	require.NoError(t, err)
	require.NotNil(t, s)

	if list == nil {
		list = inventoryOf(false)
	}
	return NewOwner(c, OwnerConfig{
		Shard:     s,
		Instance:  instance,
		Namespace: testNamespace,
		Identity:  identity,
		List:      list,
	})
}

func inventoryOf(missing bool, values ...string) ListFunc {
	return func(context.Context) (Inventory, error) {
		return Inventory{Values: sets.New(values...), Missing: missing}, nil
	}
}

func labeled(value string) client.Object {
	obj := &corev1.ConfigMap{}
	if value != "" {
		obj.SetLabels(map[string]string{DefaultKey: value})
	}
	return obj
}

// heldLease is a Lease already claimed by some instance, renewed the given time ago.
func heldLease(value, instance, identity string, renewedAgo time.Duration) *coordinationv1.Lease {
	renewed := metav1.NewMicroTime(time.Now().Add(-renewedAgo))
	duration := int32(DefaultLeaseDuration.Seconds())
	return &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      leaseName(value),
			Labels:    map[string]string{InstanceLabelKey: instance},
		},
		Spec: coordinationv1.LeaseSpec{
			HolderIdentity:       &identity,
			RenewTime:            &renewed,
			LeaseDurationSeconds: &duration,
		},
	}
}

func getLease(t *testing.T, c client.Client, value string) *coordinationv1.Lease {
	t.Helper()
	var lease coordinationv1.Lease
	key := client.ObjectKey{Namespace: testNamespace, Name: leaseName(value)}
	require.NoError(t, c.Get(context.Background(), key, &lease))
	return &lease
}

// ageObservation backdates when this process saw a value's Lease, which is what expiry is measured
// from. Reaching into the Owner avoids a test that has to sleep out a lease duration.
func ageObservation(t *testing.T, o *Owner, value string, by time.Duration) {
	t.Helper()

	obs, ok := o.observed[value]
	require.True(t, ok, "value %q has not been observed yet", value)
	obs.at = obs.at.Add(-by)
	o.observed[value] = obs
}

// ageClaim backdates when this process last confirmed its own claim on a value.
func ageClaim(t *testing.T, o *Owner, value string, by time.Duration) {
	t.Helper()

	confirmed, ok := o.confirmed[value]
	require.True(t, ok, "value %q has not been claimed yet", value)
	o.confirmed[value] = confirmed.Add(-by)
}

// renewLease renews a Lease the way its holder would, so that watchers see the claim as live.
func renewLease(t *testing.T, c client.Client, value string) {
	t.Helper()

	lease := getLease(t, c, value)
	now := metav1.NewMicroTime(time.Now())
	lease.Spec.RenewTime = &now
	require.NoError(t, c.Update(context.Background(), lease))
}

func ignoredMetric(t *testing.T, instance, value string) float64 {
	t.Helper()
	return testutil.ToFloat64(valuesIgnored.WithLabelValues(instance, value))
}

// Values are served because they were configured, not because an object was seen carrying one, so a
// value is claimable before its first object exists.
func TestSyncClaimsEveryConfiguredValue(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	o := testOwner(t, c, "inst-a", "pod-a", []string{"0", "1"}, nil)

	require.NoError(t, o.Sync(context.Background()))

	for _, value := range []string{"0", "1"} {
		lease := getLease(t, c, value)
		assert.Equal(t, "pod-a", heldBy(lease))
		assert.Equal(t, "inst-a", lease.Labels[InstanceLabelKey])
		assert.Equal(t, Owned, o.Ownership(labeled(value)))
	}
}

// The Lease, not the configuration, is what permits a reconcile, so nothing is owned until the
// first sync has established what this instance actually holds.
func TestNothingIsOwnedBeforeTheFirstSync(t *testing.T) {
	o := testOwner(t, fake.NewClientBuilder().Build(), "inst-a", "pod-a", []string{"0"}, nil)

	assert.Equal(t, Pending, o.Ownership(labeled("0")))
}

func TestValuesThisInstanceDoesNotServeAreNeverOwned(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	o := testOwner(t, c, "inst-a", "pod-a", []string{"0"}, nil)
	require.NoError(t, o.Sync(context.Background()))

	assert.Equal(t, NotManaged, o.Ownership(labeled("1")))
	assert.Equal(t, NotManaged, o.Ownership(labeled("")))
}

// Two instances configured with one value is a misconfiguration, but it must not produce two
// reconciles: the loser defers instead.
func TestValueHeldByAnotherInstanceIsPending(t *testing.T) {
	c := fake.NewClientBuilder().
		WithObjects(heldLease("0", "inst-b", "pod-b", time.Second)).
		Build()
	o := testOwner(t, c, "inst-a", "pod-a", []string{"0"}, nil)

	require.NoError(t, o.Sync(context.Background()))

	assert.Equal(t, Pending, o.Ownership(labeled("0")))
	assert.Equal(t, "pod-b", heldBy(getLease(t, c, "0")))
	assert.Equal(t, 1.0, testutil.ToFloat64(valuesContested.WithLabelValues("inst-a")))
}

// Expiry is measured from when this process saw the Lease, never from the holder's own timestamp,
// so a holder with a skewed clock cannot have its live claim mistaken for an abandoned one. The
// cost is that a first sighting tells us nothing, however stale the timestamp looks.
func TestAnotherInstancesLeaseIsNotClaimedOnFirstSighting(t *testing.T) {
	c := fake.NewClientBuilder().
		WithObjects(heldLease("0", "inst-b", "pod-b", time.Hour)).
		Build()
	o := testOwner(t, c, "inst-a", "pod-a", []string{"0"}, nil)

	require.NoError(t, o.Sync(context.Background()))

	assert.Equal(t, Pending, o.Ownership(labeled("0")))
	assert.Equal(t, "pod-b", heldBy(getLease(t, c, "0")))
}

func TestAnotherInstancesLeaseIsClaimedOnceItAgesOut(t *testing.T) {
	c := fake.NewClientBuilder().
		WithObjects(heldLease("0", "inst-b", "pod-b", time.Hour)).
		Build()
	o := testOwner(t, c, "inst-a", "pod-a", []string{"0"}, nil)
	require.NoError(t, o.Sync(context.Background()))
	require.Equal(t, Pending, o.Ownership(labeled("0")))

	ageObservation(t, o, "0", 2*DefaultLeaseDuration)
	require.NoError(t, o.Sync(context.Background()))

	assert.Equal(t, Owned, o.Ownership(labeled("0")))
	assert.Equal(t, "pod-a", heldBy(getLease(t, c, "0")))
}

// A holder that keeps renewing resets the clock on every sync, so it never ages out however long
// this instance watches it.
func TestARenewingHolderNeverAgesOut(t *testing.T) {
	c := fake.NewClientBuilder().
		WithObjects(heldLease("0", "inst-b", "pod-b", time.Second)).
		Build()
	o := testOwner(t, c, "inst-a", "pod-a", []string{"0"}, nil)
	require.NoError(t, o.Sync(context.Background()))

	ageObservation(t, o, "0", 2*DefaultLeaseDuration)
	renewLease(t, c, "0")
	require.NoError(t, o.Sync(context.Background()))

	assert.Equal(t, Pending, o.Ownership(labeled("0")))
	assert.Equal(t, "pod-b", heldBy(getLease(t, c, "0")))
}

// A Lease nobody has ever renewed is claimable at once: there is no claim to have aged out.
func TestNeverRenewedLeaseIsClaimedImmediately(t *testing.T) {
	stale := heldLease("0", "inst-b", "pod-b", time.Second)
	stale.Spec.RenewTime = nil
	c := fake.NewClientBuilder().WithObjects(stale).Build()
	o := testOwner(t, c, "inst-a", "pod-a", []string{"0"}, nil)

	require.NoError(t, o.Sync(context.Background()))

	assert.Equal(t, Owned, o.Ownership(labeled("0")))
}

// A failed claim must not stop the others being attempted, or one unreachable value would mask the
// state of every value after it.
func TestSyncClaimsEveryValueDespiteFailures(t *testing.T) {
	var attempted []string
	c := fake.NewClientBuilder().
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				attempted = append(attempted, key.Name)
				if key.Name == leaseName("0") {
					return apierrors.NewServiceUnavailable("nope")
				}
				return c.Get(ctx, key, obj, opts...)
			},
		}).
		Build()
	o := testOwner(t, c, "inst-a", "pod-a", []string{"0", "1"}, nil)

	err := o.Sync(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "0", "the failure must name the value it belongs to")
	assert.Contains(t, attempted, leaseName("1"), "a later value must still be attempted")
	assert.Equal(t, Owned, o.Ownership(labeled("1")))
	assert.Equal(t, Pending, o.Ownership(labeled("0")))
}

// A transient failure is not evidence that the claim is gone, so work continues while the last
// confirmed claim could still be live.
func TestTransientFailureKeepsARecentlyConfirmedClaim(t *testing.T) {
	fail := false
	c := fake.NewClientBuilder().
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if fail {
					return apierrors.NewServiceUnavailable("nope")
				}
				return c.Get(ctx, key, obj, opts...)
			},
		}).
		Build()
	o := testOwner(t, c, "inst-a", "pod-a", []string{"0"}, nil)
	require.NoError(t, o.Sync(context.Background()))
	require.Equal(t, Owned, o.Ownership(labeled("0")))

	fail = true
	require.Error(t, o.Sync(context.Background()))

	assert.Equal(t, Owned, o.Ownership(labeled("0")))
}

// Once the claim is old enough that another instance could have taken it, continuing to act on it
// would be exactly the double reconcile the Lease exists to prevent.
func TestPersistentFailureGivesUpTheClaimAtExpiry(t *testing.T) {
	fail := false
	c := fake.NewClientBuilder().
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if fail {
					return apierrors.NewServiceUnavailable("nope")
				}
				return c.Get(ctx, key, obj, opts...)
			},
		}).
		Build()
	o := testOwner(t, c, "inst-a", "pod-a", []string{"0"}, nil)
	require.NoError(t, o.Sync(context.Background()))

	fail = true
	ageClaim(t, o, "0", 2*DefaultLeaseDuration)
	require.Error(t, o.Sync(context.Background()))

	assert.Equal(t, Pending, o.Ownership(labeled("0")))
}

// Leader election admits one leader per instance, so an unexpired Lease bearing our own instance
// was left by a predecessor of ours and waiting out its expiry would only delay failover.
func TestLeaseLeftByOurOwnPredecessorIsClaimedImmediately(t *testing.T) {
	c := fake.NewClientBuilder().
		WithObjects(heldLease("0", "inst-a", "pod-old", time.Second)).
		Build()
	o := testOwner(t, c, "inst-a", "pod-new", []string{"0"}, nil)

	require.NoError(t, o.Sync(context.Background()))

	assert.Equal(t, Owned, o.Ownership(labeled("0")))
	assert.Equal(t, "pod-new", heldBy(getLease(t, c, "0")))
}

func TestSyncRenewsLeasesItAlreadyHolds(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	o := testOwner(t, c, "inst-a", "pod-a", []string{"0"}, nil)

	require.NoError(t, o.Sync(context.Background()))
	first := getLease(t, c, "0").Spec.RenewTime

	time.Sleep(10 * time.Millisecond)
	require.NoError(t, o.Sync(context.Background()))

	assert.True(t, getLease(t, c, "0").Spec.RenewTime.After(first.Time))
	assert.Equal(t, 1.0, testutil.ToFloat64(valuesHeld.WithLabelValues("inst-a")))
}

// Sync replaces what this instance holds rather than adding to it, so a value taken by another
// instance stops being owned. Were it additive, the loser would go on reconciling the value's
// objects alongside its new owner — the one thing the Lease exists to prevent.
func TestValueTakenByAnotherInstanceStopsBeingOwned(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	o := testOwner(t, c, "inst-a", "pod-a", []string{"0", "1"}, nil)
	require.NoError(t, o.Sync(context.Background()))
	require.Equal(t, Owned, o.Ownership(labeled("0")))

	// What a process stalled past its lease expiry wakes up to.
	taken := getLease(t, c, "0")
	identity := "pod-b"
	taken.Spec.HolderIdentity = &identity
	taken.Labels[InstanceLabelKey] = "inst-b"
	require.NoError(t, c.Update(context.Background(), taken))

	require.NoError(t, o.Sync(context.Background()))

	assert.Equal(t, Pending, o.Ownership(labeled("0")))
	assert.Equal(t, Owned, o.Ownership(labeled("1")), "values still held must survive the loss of another")
}

// Contention has to clear once the value is regained, not merely be recorded when it starts.
func TestContentionClearsWhenAValueIsRegained(t *testing.T) {
	c := fake.NewClientBuilder().
		WithObjects(heldLease("0", "regain-b", "pod-b", time.Second)).
		Build()
	o := testOwner(t, c, "regain-a", "pod-a", []string{"0"}, nil)

	require.NoError(t, o.Sync(context.Background()))
	require.Equal(t, Pending, o.Ownership(labeled("0")))
	require.Equal(t, 1.0, testutil.ToFloat64(valuesContested.WithLabelValues("regain-a")))
	require.Equal(t, 0.0, testutil.ToFloat64(valuesHeld.WithLabelValues("regain-a")))

	ageObservation(t, o, "0", 2*DefaultLeaseDuration)
	require.NoError(t, o.Sync(context.Background()))

	require.Equal(t, Owned, o.Ownership(labeled("0")))
	assert.Equal(t, 0.0, testutil.ToFloat64(valuesContested.WithLabelValues("regain-a")), "contention must clear, not just be recorded")
	assert.Equal(t, 1.0, testutil.ToFloat64(valuesHeld.WithLabelValues("regain-a")))
}

// Every instance sees every object, so each reports every value in use. A value no instance serves
// is then the one every instance reports as ignored.
func TestEveryValueInUseIsReportedAsServedOrIgnored(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	o := testOwner(t, c, "report-a", "pod-a", []string{"0"}, inventoryOf(false, "0", "1"))

	require.NoError(t, o.Sync(context.Background()))

	assert.Equal(t, 0.0, ignoredMetric(t, "report-a", "0"))
	assert.Equal(t, 1.0, ignoredMetric(t, "report-a", "1"))
}

// No instance can be configured with the absent value, so objects without one are reconciled by
// nobody and have to be visible as such.
func TestObjectsWithNoValueAreReportedAsIgnored(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	o := testOwner(t, c, "report-b", "pod-a", []string{"0"}, inventoryOf(true, "0"))

	require.NoError(t, o.Sync(context.Background()))

	assert.Equal(t, 1.0, ignoredMetric(t, "report-b", MissingValue))
}

// A value that falls out of use must stop being reported, or it lingers at its last reading
// forever and reads as permanently ignored.
func TestValuesNoLongerInUseStopBeingReported(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	o := testOwner(t, c, "report-c", "pod-a", []string{"0"}, inventoryOf(false, "0", "1"))
	require.NoError(t, o.Sync(context.Background()))
	require.Equal(t, 1.0, ignoredMetric(t, "report-c", "1"))

	o.cfg.List = inventoryOf(false, "0")
	require.NoError(t, o.Sync(context.Background()))

	assert.Equal(t, 0.0, ignoredMetric(t, "report-c", "1"))
}

// Giving up a value on the way out lets an instance newly configured with it claim it at once,
// rather than waiting out the lease.
func TestReleaseGivesUpHeldValues(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	o := testOwner(t, c, "inst-a", "pod-a", []string{"0"}, nil)
	require.NoError(t, o.Sync(context.Background()))

	o.release()

	lease := getLease(t, c, "0")
	assert.Empty(t, heldBy(lease))
	assert.NotContains(t, lease.Labels, InstanceLabelKey)
	assert.Equal(t, Pending, o.Ownership(labeled("0")))
}

// Clearing the holder of a value another process has since taken would take it from them rather
// than give it up.
func TestReleaseLeavesAnotherProcessesValueAlone(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	o := testOwner(t, c, "inst-a", "pod-a", []string{"0"}, nil)
	require.NoError(t, o.Sync(context.Background()))

	taken := getLease(t, c, "0")
	identity := "pod-b"
	taken.Spec.HolderIdentity = &identity
	taken.Labels[InstanceLabelKey] = "inst-b"
	require.NoError(t, c.Update(context.Background(), taken))

	o.release()

	lease := getLease(t, c, "0")
	assert.Equal(t, "pod-b", heldBy(lease))
	assert.Equal(t, "inst-b", lease.Labels[InstanceLabelKey])
}

func TestReleaseWithNothingHeldIsHarmless(t *testing.T) {
	o := testOwner(t, fake.NewClientBuilder().Build(), "inst-a", "pod-a", []string{"0"}, nil)

	assert.NotPanics(t, o.release)
}

// The context Start was given is already cancelled by the time values are released, so the release
// cannot depend on it.
func TestStartReleasesOnShutdown(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	o := testOwner(t, c, "inst-a", "pod-a", []string{"0"}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() { stopped <- o.Start(ctx) }()

	require.Eventually(t, func() bool {
		return o.Ownership(labeled("0")) == Owned
	}, time.Second, 10*time.Millisecond)

	cancel()
	require.NoError(t, <-stopped)

	assert.Empty(t, heldBy(getLease(t, c, "0")))
}

// A sync loop that stops running refreshes nothing, so claimIsFresh never gets the chance to give
// a value up. Ownership has to lapse on its own rather than persist on a stale view.
func TestOwnershipLapsesWhenTheSyncLoopStops(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	o := testOwner(t, c, "inst-a", "pod-a", []string{"0"}, nil)
	require.NoError(t, o.Sync(context.Background()))
	require.Equal(t, Owned, o.Ownership(labeled("0")))

	o.syncedAt.Store(time.Now().Add(-2 * DefaultLeaseDuration).UnixNano())

	assert.Equal(t, Pending, o.Ownership(labeled("0")))
}

// A standby replica must not hold values it will not act on.
func TestOwnerIsLeaderGated(t *testing.T) {
	o := testOwner(t, fake.NewClientBuilder().Build(), "inst-a", "pod-a", []string{"0"}, nil)
	assert.True(t, o.NeedLeaderElection())
}

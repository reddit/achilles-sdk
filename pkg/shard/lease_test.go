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

const testGroup = "testkind"

var testGroupID = Group{Name: testGroup, Types: "test.infrared.reddit.com/v1alpha1,TestKind"}

func testClient(objs ...client.Object) client.Client {
	scheme := runtime.NewScheme()
	if err := coordinationv1.AddToScheme(scheme); err != nil {
		panic(err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

// peerLease builds the advertisement another controller instance would have published.
func peerLease(instance, shardID, values string, renewedAgo time.Duration) *coordinationv1.Lease {
	renew := metav1.NewMicroTime(time.Now().Add(-renewedAgo))
	holder := "peer-pod"
	duration := int32(15)

	return &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      instanceLeaseName(testGroup, instance),
			Namespace: "ns",
			Labels: map[string]string{
				GroupLabelKey:    testGroup,
				InstanceLabelKey: instance,
				IDLabelKey:       shardID,
				KindLabelKey:     kindAdvertisement,
			},
			Annotations: map[string]string{
				ValuesAnnotationKey: values,
				TypesAnnotationKey:  testGroupID.Types,
			},
		},
		Spec: coordinationv1.LeaseSpec{
			HolderIdentity:       &holder,
			RenewTime:            &renew,
			LeaseDurationSeconds: &duration,
		},
	}
}

func advertiser(c client.Client, instance string, values []string) (*Advertiser, error) {
	s, err := Parse(DefaultKey, values)
	if err != nil {
		return nil, err
	}
	return NewAdvertiser(c, Config{
		Shard:     s,
		Group:     testGroupID,
		Instance:  instance,
		Namespace: "ns",
		Identity:  "self-pod",
	}), nil
}

func mustAdvertiser(t *testing.T, c client.Client, instance string, values []string) *Advertiser {
	t.Helper()
	a, err := advertiser(c, instance, values)
	require.NoError(t, err)
	return a
}

func TestAdvertiseSucceedsWithNoPeers(t *testing.T) {
	c := testClient()
	a := mustAdvertiser(t, c, "inst-a", []string{"a"})

	require.NoError(t, a.Advertise(context.Background()))

	var lease coordinationv1.Lease
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: "ns", Name: a.LeaseName()}, &lease))
	assert.Equal(t, "a", lease.Annotations[ValuesAnnotationKey])
	assert.Equal(t, "inst-a", lease.Labels[InstanceLabelKey])
	assert.Equal(t, "self-pod", *lease.Spec.HolderIdentity)
}

// Overlap is reported but not refused: Owner arbitrates over each concrete value, so the objects
// are still reconciled exactly once.
func TestVerifyReportsAnOverlappingPeerWithoutFailing(t *testing.T) {
	c := testClient(peerLease("inst-b", "a-b", "a,b", time.Second))
	a := mustAdvertiser(t, c, "inst-a", []string{"a"})

	overlap, err := a.Verify(context.Background())
	require.NoError(t, err)
	assert.Contains(t, overlap, "overlap")
}

func TestVerifyIgnoresDisjointPeer(t *testing.T) {
	c := testClient(peerLease("inst-b", "b", "b", time.Second))
	a := mustAdvertiser(t, c, "inst-a", []string{"a"})

	overlap, err := a.Verify(context.Background())
	require.NoError(t, err)
	assert.Empty(t, overlap)
}

// A rolling update runs two pods of the same instance at once, which must not look like a conflict
// so long as they agree about their work.
func TestAdvertiseAllowsAnotherReplicaOfTheSameInstance(t *testing.T) {
	c := testClient(peerLease("inst-a", "a", "a", time.Second))
	a := mustAdvertiser(t, c, "inst-a", []string{"a"})

	require.NoError(t, a.Advertise(context.Background()))

	var lease coordinationv1.Lease
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: "ns", Name: a.LeaseName()}, &lease))
	assert.Equal(t, "self-pod", *lease.Spec.HolderIdentity, "the incoming pod takes over the instance's lease")
}

// Values given in a different order are the same configuration, so a restart with a reordered flag
// must not look like a disagreement.
func TestVerifyTreatsReorderedValuesAsAgreement(t *testing.T) {
	c := testClient(peerLease("inst-a", "0-1", "1,0", time.Second))
	a := mustAdvertiser(t, c, "inst-a", []string{"0", "1"})

	_, err := a.Verify(context.Background())
	assert.NoError(t, err)
}

// The one misconfiguration that is not self-correcting: two Deployments given one name share a
// leader election lock, so only one of their value sets would ever be served.
func TestVerifyFailsWhenPodsSharingOurNameDisagreeAboutTheirValues(t *testing.T) {
	c := testClient(peerLease("inst-a", "b", "b", time.Second))
	a := mustAdvertiser(t, c, "inst-a", []string{"a"})

	_, err := a.Verify(context.Background())
	require.Error(t, err)
	assert.ErrorContains(t, err, "leader election lock")

	assert.Error(t, a.Advertise(context.Background()), "a shared lock must stop the process starting")
}

// The readable group name drops the API group, so two controllers sharding same-named Kinds would
// otherwise contend for each other's values.
func TestVerifyFailsWhenAnotherControllerSharesOurGroupName(t *testing.T) {
	peer := peerLease("inst-b", "b", "b", time.Second)
	peer.Annotations[TypesAnnotationKey] = "other.example.com/v1,TestKind"
	c := testClient(peer)

	a := mustAdvertiser(t, c, "inst-a", []string{"a"})

	_, err := a.Verify(context.Background())
	assert.ErrorContains(t, err, "different types")
}

func TestVerifyIgnoresStalePeer(t *testing.T) {
	c := testClient(peerLease("inst-b", "a-b", "a,b", time.Hour))
	a := mustAdvertiser(t, c, "inst-a", []string{"a"})

	overlap, err := a.Verify(context.Background())
	require.NoError(t, err)
	assert.Empty(t, overlap, "a peer that stopped renewing is gone")
}

func TestVerifyIgnoresOtherGroups(t *testing.T) {
	other := peerLease("inst-b", "a-b", "a,b", time.Second)
	other.Labels[GroupLabelKey] = "a-different-controller"
	c := testClient(other)

	a := mustAdvertiser(t, c, "inst-a", []string{"a"})

	overlap, err := a.Verify(context.Background())
	require.NoError(t, err)
	assert.Empty(t, overlap, "a different controller's shards are unrelated")
}

func TestRenewUpdatesRenewTime(t *testing.T) {
	c := testClient()
	a := mustAdvertiser(t, c, "inst-a", []string{"a"})
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

func TestLeaseNameIsScopedToGroupAndInstance(t *testing.T) {
	a := mustAdvertiser(t, testClient(), "app-controller-critical", []string{"0", "1"})
	assert.Equal(t, testGroup+"-instance-app-controller-critical", a.LeaseName())

	b := mustAdvertiser(t, testClient(), "app-controller-standard", []string{UnlabeledValue})
	assert.Equal(t, testGroup+"-instance-app-controller-standard", b.LeaseName())
}

// Every replica advertises, not just the leader, so the published map of who manages what reflects
// what is actually running.
func TestAdvertiserIsNotLeaderGated(t *testing.T) {
	a := mustAdvertiser(t, testClient(), "inst-a", []string{"a"})
	assert.False(t, a.NeedLeaderElection())
}

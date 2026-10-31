package internal

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/reddit/achilles-sdk-api/api"
	"github.com/reddit/achilles-sdk/pkg/fsm/metrics"
	"github.com/reddit/achilles-sdk/pkg/fsm/types"
	testv1alpha1 "github.com/reddit/achilles-sdk/pkg/internal/tests/api/test/v1alpha1"
	"github.com/reddit/achilles-sdk/pkg/io"
	"github.com/reddit/achilles-sdk/pkg/meta"
)

type testClaimOptions = types.ReconcilerOptions[testv1alpha1.TestClaim, *testv1alpha1.TestClaim]

// A reconcile that read an outdated copy, as from a lagging cache, would write that copy's status back over
// newer status. With OptimisticLockStatus, the write is rejected and the object requeued instead.
func TestReconcileWithOptimisticLockStatusKeepsNewerStatus(t *testing.T) {
	server, stale := claimUpdatedSinceRead(t)
	r := statusLockReconciler(t, laggingCache(server, stale), testClaimOptions{OptimisticLockStatus: true})

	res, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(stale)})

	require.NoError(t, err)
	assert.Equal(t, reconcile.Result{Requeue: true}, res, "requeued with backoff")
	assert.Equal(t, "newer", storedClaim(t, server).Status.TestField)
}

// Locking is opt-in: without it, status is written whatever changed since the read, as before.
func TestReconcileWithoutOptimisticLockStatusDoesNotCheckTheRead(t *testing.T) {
	server, stale := claimUpdatedSinceRead(t)
	r := statusLockReconciler(t, laggingCache(server, stale), testClaimOptions{})

	res, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(stale)})

	require.NoError(t, err)
	assert.Equal(t, reconcile.Result{}, res)
	assert.Contains(t, conditionTypes(t, server, stale), reconciledCondition)
}

// Applying a new child writes status.resourceRefs mid-reconcile. That write mustn't make the reconcile's own
// status write look outdated.
func TestReconcileWithOptimisticLockStatusWritesStatusAfterAddingAChild(t *testing.T) {
	obj := newTestClaim()
	server := fake.NewClientBuilder().WithScheme(scheme).WithObjects(obj).WithStatusSubresource(obj).Build()
	child := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "child", Namespace: "default"}}
	r := statusLockReconciler(t, &io.ClientApplicator{Client: server, Applicator: io.NewAPIPatchingApplicator(server)},
		testClaimOptions{OptimisticLockStatus: true}, child)

	res, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(obj)})

	require.NoError(t, err)
	assert.Equal(t, reconcile.Result{}, res)
	stored := storedClaim(t, server)
	assert.Len(t, stored.Status.ResourceRefs, 1)
	assert.Contains(t, conditionTypes(t, server, obj), reconciledCondition)
}

// claimUpdatedSinceRead returns a server holding a TestClaim whose status changed after stale was read.
func claimUpdatedSinceRead(t *testing.T) (client.Client, *testv1alpha1.TestClaim) {
	t.Helper()
	obj := newTestClaim()
	obj.Status.TestField = "older"
	server := fake.NewClientBuilder().WithScheme(scheme).WithObjects(obj).WithStatusSubresource(obj).Build()
	stale := storedClaim(t, server)
	newer := stale.DeepCopy()
	newer.Status.TestField = "newer"
	require.NoError(t, server.Status().Update(context.Background(), newer))
	return server, stale
}

// laggingCache returns a client whose reconcile reads return stale while its writes go to server.
func laggingCache(server client.Client, stale *testv1alpha1.TestClaim) *io.ClientApplicator {
	return &io.ClientApplicator{
		Client:     &staleReadClient{Client: server, stale: stale},
		Applicator: io.NewAPIPatchingApplicator(server),
	}
}

func storedClaim(t *testing.T, c client.Client) *testv1alpha1.TestClaim {
	t.Helper()
	stored := &testv1alpha1.TestClaim{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(newTestClaim()), stored))
	return stored
}

// statusLockReconciler returns a reconciler whose one state sets a condition, so every reconcile writes status,
// and applies children.
func statusLockReconciler(
	t *testing.T,
	c *io.ClientApplicator,
	opts testClaimOptions,
	children ...client.Object,
) *fsmReconciler[testv1alpha1.TestClaim, *testv1alpha1.TestClaim] {
	t.Helper()
	m := metrics.MustMakeMetrics(scheme, prometheus.NewRegistry())
	m.InitializeForGVK(meta.MustGVKForObject(newTestClaim(), scheme))
	state := &types.State[*testv1alpha1.TestClaim]{
		Name:      "reconciled",
		Condition: api.Condition{Type: reconciledCondition},
		Transition: func(_ context.Context, _ *testv1alpha1.TestClaim, out *types.OutputSet) (*types.State[*testv1alpha1.TestClaim], types.Result) {
			for _, child := range children {
				out.Apply(child.DeepCopyObject().(client.Object))
			}
			return nil, types.DoneResult()
		},
	}
	return NewFSMReconciler[testv1alpha1.TestClaim, *testv1alpha1.TestClaim](
		"test", zaptest.NewLogger(t).Sugar(), c, scheme, state, nil, []schema.GroupVersionKind{corev1.SchemeGroupVersion.WithKind("ConfigMap")}, m, opts,
	)
}

package internal

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/reddit/achilles-sdk-api/api"
	"github.com/reddit/achilles-sdk/pkg/fsm/metrics"
	"github.com/reddit/achilles-sdk/pkg/fsm/types"
	testv1alpha1 "github.com/reddit/achilles-sdk/pkg/internal/tests/api/test/v1alpha1"
	"github.com/reddit/achilles-sdk/pkg/io"
	"github.com/reddit/achilles-sdk/pkg/meta"
	"github.com/reddit/achilles-sdk/pkg/shard"
)

// reconciledCondition is set by the state machine below, so its presence means the object was
// reconciled rather than merely created.
const reconciledCondition = api.ConditionType("Reconciled")

// shardedOwner returns an Owner for an instance serving "a", which claims the value on first Sync.
func shardedOwner(t *testing.T) *shard.Owner {
	t.Helper()

	s, err := shard.Parse(shard.DefaultKey, []string{"a"})
	require.NoError(t, err)

	return shard.NewOwner(fake.NewClientBuilder().Build(), shard.OwnerConfig{
		Shard:     s,
		Instance:  "inst-a",
		Namespace: "ns",
		Identity:  "pod-a",
		List: func(context.Context) (shard.Inventory, error) {
			return shard.Inventory{}, nil
		},
	})
}

// shardedContext returns a context whose instance serves "a", optionally having claimed it.
func shardedContext(t *testing.T, claim bool) context.Context {
	t.Helper()

	owner := shardedOwner(t)
	if claim {
		require.NoError(t, owner.Sync(context.Background()))
	}
	return shard.NewContext(context.Background(), owner)
}

func shardedReconciler(t *testing.T, obj *testv1alpha1.TestClaim) (*fsmReconciler[testv1alpha1.TestClaim, *testv1alpha1.TestClaim], client.Client) {
	t.Helper()

	apiClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(obj).WithStatusSubresource(obj).Build()
	c := &io.ClientApplicator{
		Client:     apiClient,
		Applicator: io.NewAPIPatchingApplicator(apiClient),
	}

	m := metrics.MustMakeMetrics(scheme, prometheus.NewRegistry())
	m.InitializeForGVK(meta.MustGVKForObject(obj, scheme))

	state := &types.State[*testv1alpha1.TestClaim]{
		Name:      "reconciled",
		Condition: api.Condition{Type: reconciledCondition},
		Transition: func(context.Context, *testv1alpha1.TestClaim, *types.OutputSet) (*types.State[*testv1alpha1.TestClaim], types.Result) {
			return nil, types.DoneResult()
		},
	}

	r := NewFSMReconciler[testv1alpha1.TestClaim, *testv1alpha1.TestClaim](
		"test", zaptest.NewLogger(t).Sugar(), c, scheme, state, nil, nil, m,
		types.ReconcilerOptions[testv1alpha1.TestClaim, *testv1alpha1.TestClaim]{},
	)
	return r, apiClient
}

func claimWithValue(value string) *testv1alpha1.TestClaim {
	obj := newTestClaim()
	if value != "" {
		obj.SetLabels(map[string]string{shard.DefaultKey: value})
	}
	return obj
}

func conditionTypes(t *testing.T, c client.Client, obj *testv1alpha1.TestClaim) []api.ConditionType {
	t.Helper()

	stored := &testv1alpha1.TestClaim{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(obj), stored))

	var types []api.ConditionType
	for _, condition := range stored.GetConditions() {
		types = append(types, condition.Type)
	}
	return types
}

// Every instance is notified of every object, so an instance that does not serve an object's value
// has to leave it completely alone rather than merely decline to act on it.
func TestReconcileLeavesObjectsOfOtherShardsUntouched(t *testing.T) {
	for name, value := range map[string]string{
		"value served by another instance": "b",
		"no value at all":                  "",
	} {
		t.Run(name, func(t *testing.T) {
			obj := claimWithValue(value)
			r, c := shardedReconciler(t, obj)

			res, err := r.Reconcile(shardedContext(t, true), reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(obj),
			})

			require.NoError(t, err)
			assert.True(t, res.IsZero())
			assert.NotContains(t, conditionTypes(t, c, obj), reconciledCondition)
		})
	}
}

func TestReconcileProceedsForAClaimedValue(t *testing.T) {
	obj := claimWithValue("a")
	r, c := shardedReconciler(t, obj)

	_, err := r.Reconcile(shardedContext(t, true), reconcile.Request{
		NamespacedName: client.ObjectKeyFromObject(obj),
	})

	require.NoError(t, err)
	assert.Contains(t, conditionTypes(t, c, obj), reconciledCondition)
}

// Until the value is claimed, acting on it would be exactly what the Lease exists to prevent.
func TestReconcileDefersUntilTheValueIsClaimed(t *testing.T) {
	obj := claimWithValue("a")
	r, c := shardedReconciler(t, obj)

	res, err := r.Reconcile(shardedContext(t, false), reconcile.Request{
		NamespacedName: client.ObjectKeyFromObject(obj),
	})

	require.NoError(t, err)
	assert.Equal(t, shard.PendingRequeueInterval, res.RequeueAfter)
	assert.NotContains(t, conditionTypes(t, c, obj), reconciledCondition)
}

// The deferred reconcile has to converge: claiming the value is enough to make the requeued object
// reconcile, with nothing else prompting it.
func TestReconcileConvergesOnceTheValueIsClaimed(t *testing.T) {
	obj := claimWithValue("a")
	r, c := shardedReconciler(t, obj)

	owner := shardedOwner(t)
	ctx := shard.NewContext(context.Background(), owner)
	req := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(obj)}

	res, err := r.Reconcile(ctx, req)
	require.NoError(t, err)
	require.Equal(t, shard.PendingRequeueInterval, res.RequeueAfter)
	require.NotContains(t, conditionTypes(t, c, obj), reconciledCondition)

	require.NoError(t, owner.Sync(context.Background()))

	res, err = r.Reconcile(ctx, req)

	require.NoError(t, err)
	assert.True(t, res.IsZero())
	assert.Contains(t, conditionTypes(t, c, obj), reconciledCondition)
}

// An unsharded controller must behave exactly as it did before sharding existed.
func TestReconcileWithoutShardingReconcilesEverything(t *testing.T) {
	obj := claimWithValue("")
	r, c := shardedReconciler(t, obj)

	_, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: client.ObjectKeyFromObject(obj),
	})

	require.NoError(t, err)
	assert.Contains(t, conditionTypes(t, c, obj), reconciledCondition)
}

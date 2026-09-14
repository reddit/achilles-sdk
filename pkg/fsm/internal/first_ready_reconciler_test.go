package internal

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/reddit/achilles-sdk-api/api"
	"github.com/reddit/achilles-sdk/pkg/fsm/metrics"
	fsmtypes "github.com/reddit/achilles-sdk/pkg/fsm/types"
	"github.com/reddit/achilles-sdk/pkg/internal/tests/api/test/v1alpha1"
	"github.com/reddit/achilles-sdk/pkg/meta"
	"github.com/reddit/achilles-sdk/pkg/status"
)

func firstReadyFSM(t *testing.T, c client.Client, m *metrics.Metrics, result *fsmtypes.Result) *fsmReconciler[v1alpha1.TestClaimed, *v1alpha1.TestClaimed] {
	t.Helper()
	m.InitializeForGVK(meta.MustGVKForObject(&v1alpha1.TestClaimed{}, scheme))
	return NewFSMReconciler("first-ready", zaptest.NewLogger(t).Sugar(), testApplicator(c), scheme,
		&fsmtypes.State[*v1alpha1.TestClaimed]{
			Name: "ready", Condition: api.Condition{Type: "Provisioned"},
			Transition: func(context.Context, *v1alpha1.TestClaimed, *fsmtypes.OutputSet) (*fsmtypes.State[*v1alpha1.TestClaimed], fsmtypes.Result) {
				return nil, *result
			},
		}, nil, nil, m, fsmtypes.ReconcilerOptions[v1alpha1.TestClaimed, *v1alpha1.TestClaimed]{})
}

func TestFSMFirstReadyLifecycle(t *testing.T) {
	ctx := context.Background()
	obj := firstReadyObject()
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(obj).WithStatusSubresource(obj).Build()
	reg := prometheus.NewRegistry()
	m := metrics.MustMakeMetrics(scheme, reg)
	result := fsmtypes.RequeueResult("not ready", time.Second)
	r := firstReadyFSM(t, c, m, &result)
	req := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(obj)}
	// Capture the pre-existing Ready condition before this reconcile replaces it with False.
	_, err := r.Reconcile(ctx, req)
	require.NoError(t, err)
	require.NoError(t, c.Get(ctx, req.NamespacedName, obj))
	require.Equal(t, firstReadyTime.Format(time.RFC3339Nano), obj.Annotations[meta.FirstReadyAtKey])
	requireFirstReadyMetric(t, reg, obj, &firstReadyTime)

	result = fsmtypes.DoneResult()
	obj.Generation++
	require.NoError(t, c.Update(ctx, obj))
	_, err = r.Reconcile(ctx, req)
	require.NoError(t, err)
	requireFirstReadyMetric(t, reg, obj, &firstReadyTime)

	require.NoError(t, c.Delete(ctx, obj))
	_, err = r.Reconcile(ctx, req)
	require.NoError(t, err)
	requireFirstReadyMetric(t, reg, obj, nil)

	// Exercise the NotFound path that creates a replacement before returning.
	m.RecordFirstReady(obj, firstReadyTime)
	r.reconcilerOptions.CreateIfNotFound = true
	_, err = r.Reconcile(ctx, req)
	require.NoError(t, err)
	requireFirstReadyMetric(t, reg, obj, nil)

	_, err = r.Reconcile(ctx, req)
	require.NoError(t, err)
	require.NoError(t, c.Get(ctx, req.NamespacedName, obj))
	newTime, err := time.Parse(time.RFC3339Nano, obj.Annotations[meta.FirstReadyAtKey])
	require.NoError(t, err)
	require.True(t, newTime.After(firstReadyTime))
	requireFirstReadyMetric(t, reg, obj, &newTime)
}

func TestFSMFirstReadyWriteFailures(t *testing.T) {
	for _, failStatus := range []bool{true, false} {
		t.Run(map[bool]string{true: "status", false: "annotation"}[failStatus], func(t *testing.T) {
			ctx := context.Background()
			obj := firstReadyObject()
			obj.SetConditions(api.Creating())
			writeErr := errors.New("write failed")
			fail := true
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(obj).WithStatusSubresource(obj).
				WithInterceptorFuncs(interceptor.Funcs{
					SubResourcePatch: func(ctx context.Context, c client.Client, sub string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
						if fail && failStatus {
							// Allow output bookkeeping, then fail the write that actually publishes Ready=True.
							data, err := patch.Data(obj)
							require.NoError(t, err)
							desired := &v1alpha1.TestClaimed{}
							require.NoError(t, json.Unmarshal(data, desired))
							if status.ResourceReady(desired) {
								return writeErr
							}
						}
						return c.SubResource(sub).Patch(ctx, obj, patch, opts...)
					},
					Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
						if fail && !failStatus {
							return writeErr
						}
						return c.Patch(ctx, obj, patch, opts...)
					},
				}).Build()
			reg := prometheus.NewRegistry()
			m := metrics.MustMakeMetrics(scheme, reg)
			result := fsmtypes.DoneResult()
			r := firstReadyFSM(t, c, m, &result)
			req := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(obj)}
			_, err := r.Reconcile(ctx, req)
			require.ErrorIs(t, err, writeErr)
			require.NoError(t, c.Get(ctx, req.NamespacedName, obj))
			require.NotContains(t, obj.Annotations, meta.FirstReadyAtKey)
			requireFirstReadyMetric(t, reg, obj, nil)
			fail = false
			_, err = r.Reconcile(ctx, req)
			require.NoError(t, err)
			require.NoError(t, c.Get(ctx, req.NamespacedName, obj))
			readyAt, err := time.Parse(time.RFC3339Nano, obj.Annotations[meta.FirstReadyAtKey])
			require.NoError(t, err)
			requireFirstReadyMetric(t, reg, obj, &readyAt)
		})
	}
}

func TestClaimFirstReadyLifecycle(t *testing.T) {
	ctx := context.Background()
	claim := apply(newTestClaim(), withFinalizer, withGeneratedClaimRef)
	claimed := apply(&v1alpha1.TestClaimed{}, withGeneratedName, withClaimRef, withSuccessfulConditions[*v1alpha1.TestClaimed])
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(claim, claimed).WithStatusSubresource(claim, claimed).Build()
	reg := prometheus.NewRegistry()
	m := metrics.MustMakeMetrics(scheme, reg)
	r := NewClaimReconciler(claimed, claim, testApplicator(c), scheme, zaptest.NewLogger(t).Sugar(), nil, m)
	req := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(claim)}
	_, err := r.Reconcile(ctx, req)
	require.NoError(t, err)
	require.NoError(t, c.Get(ctx, req.NamespacedName, claim))
	readyAt, err := time.Parse(time.RFC3339Nano, claim.Annotations[meta.FirstReadyAtKey])
	require.NoError(t, err)
	require.True(t, readyAt.Equal(claim.GetCondition(api.TypeReady).LastTransitionTime.Time))
	requireFirstReadyMetric(t, reg, claim, &readyAt)

	reg = prometheus.NewRegistry()
	r.metrics = metrics.MustMakeMetrics(scheme, reg)
	claim.Labels = map[string]string{meta.SuspendKey: "true"}
	require.NoError(t, c.Update(ctx, claim))
	_, err = r.Reconcile(ctx, req)
	require.NoError(t, err)
	requireFirstReadyMetric(t, reg, claim, &readyAt)

	// Remove the test finalizer directly so this test can exercise NotFound cleanup.
	require.NoError(t, c.Get(ctx, req.NamespacedName, claim))
	claim.Finalizers = nil
	require.NoError(t, c.Update(ctx, claim))
	require.NoError(t, c.Delete(ctx, claim))
	_, err = r.Reconcile(ctx, req)
	require.NoError(t, err)
	requireFirstReadyMetric(t, reg, claim, nil)
}

func TestFirstReadyPreservesReconcileError(t *testing.T) {
	ctx := context.Background()
	obj := firstReadyObject()
	obj.SetConditions(api.Creating())
	stateErr := errors.New("state failed")
	readErr := errors.New("metrics read failed")
	failRead := false
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(obj).WithStatusSubresource(obj).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if failRead {
					return readErr
				}
				return c.Get(ctx, key, obj, opts...)
			},
			SubResourcePatch: func(ctx context.Context, c client.Client, sub string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				err := c.SubResource(sub).Patch(ctx, obj, patch, opts...)
				// Fail the refresh after the failed state has published its conditions.
				failRead = true
				return err
			},
		}).Build()
	reg := prometheus.NewRegistry()
	result := fsmtypes.ErrorResult(stateErr)
	r := firstReadyFSM(t, c, metrics.MustMakeMetrics(scheme, reg), &result)
	_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(obj)})
	require.ErrorIs(t, err, stateErr)
	require.ErrorIs(t, err, readErr)
	requireFirstReadyMetric(t, reg, obj, nil)
}

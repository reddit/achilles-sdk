package internal

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/reddit/achilles-sdk-api/api"
	"github.com/reddit/achilles-sdk/pkg/fsm/metrics"
	fsmtypes "github.com/reddit/achilles-sdk/pkg/fsm/types"
	"github.com/reddit/achilles-sdk/pkg/internal/tests/api/test/v1alpha1"
	"github.com/reddit/achilles-sdk/pkg/meta"
)

const firstReadyMetricName = "achilles_resource_first_ready_timestamp_seconds"

var firstReadyTime = time.Date(2026, 9, 1, 12, 30, 0, 0, time.UTC)

func firstReadyObject() *v1alpha1.TestClaimed {
	obj := &v1alpha1.TestClaimed{ObjectMeta: metav1.ObjectMeta{
		Name: "first-ready", Generation: 3,
		Annotations: map[string]string{"example.com/other": "preserved"},
	}}
	obj.SetConditions(api.Condition{
		Type: api.TypeReady, Status: corev1.ConditionTrue, ObservedGeneration: 3,
		LastTransitionTime: metav1.NewTime(firstReadyTime),
	})
	return obj
}

func requireFirstReadyMetric(t *testing.T, reg *prometheus.Registry, obj client.Object, want *time.Time) {
	t.Helper()
	families, err := reg.Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() != firstReadyMetricName {
			continue
		}
		for _, metric := range family.Metric {
			labels := map[string]string{}
			for _, label := range metric.Label {
				labels[label.GetName()] = label.GetValue()
			}
			gvk := meta.MustGVKForObject(obj, scheme)
			if labels["name"] != obj.GetName() || labels["namespace"] != obj.GetNamespace() || labels["kind"] != gvk.Kind {
				continue
			}
			require.NotNil(t, want, "unexpected first-ready metric")
			require.Equal(t, map[string]string{
				"group": gvk.Group, "version": gvk.Version, "kind": gvk.Kind,
				"name": obj.GetName(), "namespace": obj.GetNamespace(),
			}, labels)
			require.Equal(t, float64(want.Unix())+float64(want.Nanosecond())/1e9, metric.GetGauge().GetValue())
			return
		}
	}
	require.Nil(t, want, "first-ready metric not found")
}

func TestObserveFirstReady(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*v1alpha1.TestClaimed)
		want   bool
	}{
		{name: "ready", want: true},
		{name: "nil annotations", want: true, change: func(o *v1alpha1.TestClaimed) { o.Annotations = nil }},
		{name: "no conditions", change: func(o *v1alpha1.TestClaimed) { o.Status.Conditions = nil }},
		{name: "false", change: func(o *v1alpha1.TestClaimed) { o.SetConditions(api.Unavailable()) }},
		{name: "unknown", change: func(o *v1alpha1.TestClaimed) {
			o.SetConditions(api.Condition{Type: api.TypeReady, Status: corev1.ConditionUnknown})
		}},
		{name: "stale generation", change: func(o *v1alpha1.TestClaimed) { o.Generation++ }},
		{name: "suspended", change: func(o *v1alpha1.TestClaimed) { o.Labels = map[string]string{meta.SuspendKey: "true"} }},
		{name: "terminating", change: func(o *v1alpha1.TestClaimed) {
			o.DeletionTimestamp = &now
			o.Finalizers = []string{"test.example/finalizer"}
		}},
		{name: "malformed annotation", change: func(o *v1alpha1.TestClaimed) { o.Annotations[meta.FirstReadyAtKey] = "invalid" }},
		{name: "empty annotation", change: func(o *v1alpha1.TestClaimed) { o.Annotations[meta.FirstReadyAtKey] = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			obj := firstReadyObject()
			if tc.change != nil {
				tc.change(obj)
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(obj).WithStatusSubresource(obj).Build()
			require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(obj), obj))
			before := obj.DeepCopy()
			reg := prometheus.NewRegistry()
			m := metrics.MustMakeMetrics(scheme, reg)
			require.NoError(t, observeFirstReady(ctx, c, obj, m, zaptest.NewLogger(t).Sugar()))
			require.Equal(t, before, obj, "observing must not overwrite the reconciler's snapshot")
			require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(obj), obj))
			if tc.want {
				require.Equal(t, firstReadyTime.Format(time.RFC3339Nano), obj.Annotations[meta.FirstReadyAtKey])
				requireFirstReadyMetric(t, reg, obj, &firstReadyTime)
			} else {
				require.Equal(t, before.Annotations, obj.Annotations)
				requireFirstReadyMetric(t, reg, obj, nil)
			}
			if before.Annotations != nil {
				require.Equal(t, "preserved", obj.Annotations["example.com/other"])
			}
		})
	}
}

func TestFirstReadySurvivesRestartAndStatusChanges(t *testing.T) {
	ctx := context.Background()
	obj := firstReadyObject()
	// Stored timestamps can have subsecond precision and a non-UTC offset.
	stored := firstReadyTime.Add(123456789 * time.Nanosecond)
	obj.Annotations[meta.FirstReadyAtKey] = stored.In(time.FixedZone("offset", 3600)).Format(time.RFC3339Nano)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(obj).WithStatusSubresource(obj).Build()
	for _, state := range []string{"unready", "new generation", "ready again", "suspended", "terminating"} {
		t.Run(state, func(t *testing.T) {
			require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(obj), obj))
			switch state {
			case "unready":
				obj.SetConditions(api.Unavailable())
			case "new generation":
				obj.Generation++
			case "ready again":
				obj.SetConditions(api.Available())
			case "suspended":
				obj.Labels = map[string]string{meta.SuspendKey: "true"}
			case "terminating":
				obj.DeletionTimestamp = &now
			}
			reg := prometheus.NewRegistry()
			m := metrics.MustMakeMetrics(scheme, reg)
			// Restoring history must only read, even for suspended/deleting objects.
			noWrites := interceptor.NewClient(c, interceptor.Funcs{
				Patch: func(context.Context, client.WithWatch, client.Object, client.Patch, ...client.PatchOption) error {
					t.Fatal("unexpected patch for existing timestamp")
					return nil
				},
			})
			require.NoError(t, observeFirstReady(ctx, noWrites, obj, m, zaptest.NewLogger(t).Sugar()))
			requireFirstReadyMetric(t, reg, obj, &stored)
			m.Reset()
			requireFirstReadyMetric(t, reg, obj, nil)
			require.NoError(t, observeFirstReady(ctx, noWrites, obj, m, zaptest.NewLogger(t).Sugar()))
			requireFirstReadyMetric(t, reg, obj, &stored)
		})
	}
}

func TestFirstReadyFallback(t *testing.T) {
	ctx := context.Background()
	obj := firstReadyObject()
	condition := obj.GetCondition(api.TypeReady)
	condition.LastTransitionTime = metav1.Time{}
	obj.Status.Conditions = []api.Condition{condition}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(obj).Build()
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(obj), obj))
	reg := prometheus.NewRegistry()
	m := metrics.MustMakeMetrics(scheme, reg)
	start := time.Now()
	require.NoError(t, observeFirstReady(ctx, c, obj, m, zaptest.NewLogger(t).Sugar()))
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(obj), obj))
	got, err := time.Parse(time.RFC3339Nano, obj.Annotations[meta.FirstReadyAtKey])
	require.NoError(t, err)
	require.False(t, got.Before(start))
	require.False(t, got.After(time.Now()))
	requireFirstReadyMetric(t, reg, obj, &got)
}

func TestFirstReadyDisabled(t *testing.T) {
	for _, recorded := range []bool{false, true} {
		ctx := context.Background()
		obj := firstReadyObject()
		if recorded {
			obj.Annotations[meta.FirstReadyAtKey] = firstReadyTime.Format(time.RFC3339Nano)
		}
		reg := prometheus.NewRegistry()
		m := metrics.MustMakeMetricsWithOptions(scheme, reg, fsmtypes.MetricsOptions{
			DisableMetrics: []fsmtypes.AchillesMetrics{fsmtypes.AchillesResourceFirstReady},
		})
		before := obj.DeepCopy()
		// A nil client proves that disabled tracking performs no API calls.
		require.NoError(t, observeFirstReady(ctx, nil, obj, m, zaptest.NewLogger(t).Sugar()))
		require.Equal(t, before, obj)
		m.RecordFirstReady(obj, firstReadyTime)
		requireFirstReadyMetric(t, reg, obj, nil)
	}
}

func TestFirstReadyPatchFailureAndConflict(t *testing.T) {
	ctx := context.Background()
	obj := firstReadyObject()
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(obj).Build()
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(obj), obj))
	reg := prometheus.NewRegistry()
	m := metrics.MustMakeMetrics(scheme, reg)
	log := zaptest.NewLogger(t).Sugar()
	writeErr := errors.New("patch failed")
	failing := interceptor.NewClient(c, interceptor.Funcs{
		Patch: func(context.Context, client.WithWatch, client.Object, client.Patch, ...client.PatchOption) error {
			return writeErr
		},
	})
	require.ErrorIs(t, observeFirstReady(ctx, failing, obj, m, log), writeErr)
	require.NotContains(t, obj.Annotations, meta.FirstReadyAtKey)
	requireFirstReadyMetric(t, reg, obj, nil)

	// Another writer wins while this reconciler still holds an older snapshot.
	winner := obj.DeepCopy()
	winnerTime := firstReadyTime.Add(-time.Hour)
	winner.Annotations[meta.FirstReadyAtKey] = winnerTime.Format(time.RFC3339Nano)
	winner.Annotations["example.com/concurrent"] = "preserved"
	require.NoError(t, c.Update(ctx, winner))
	require.True(t, kerrors.IsConflict(observeFirstReady(ctx, c, obj, m, log)))
	requireFirstReadyMetric(t, reg, obj, nil)
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(obj), obj))
	require.NoError(t, observeFirstReady(ctx, c, obj, m, log))
	requireFirstReadyMetric(t, reg, obj, &winnerTime)
	require.Equal(t, "preserved", obj.Annotations["example.com/concurrent"])
}

func TestFirstReadyReplacementClearsOldSeries(t *testing.T) {
	ctx := context.Background()
	obj := firstReadyObject()
	reg := prometheus.NewRegistry()
	m := metrics.MustMakeMetrics(scheme, reg)
	m.RecordFirstReady(obj, firstReadyTime)
	obj.SetConditions(api.Unavailable())
	require.NoError(t, observeFirstReady(ctx, nil, obj, m, zaptest.NewLogger(t).Sugar()))
	requireFirstReadyMetric(t, reg, obj, nil)
	m.RecordFirstReady(obj, firstReadyTime)
	m.DeleteFirstReady(obj)
	requireFirstReadyMetric(t, reg, obj, nil)
}

func TestFirstReadyIndependentOfConditionMetrics(t *testing.T) {
	ctx := context.Background()
	obj := firstReadyObject()
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(obj).Build()
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(obj), obj))
	reg := prometheus.NewRegistry()
	m := metrics.MustMakeMetricsWithOptions(scheme, reg, fsmtypes.MetricsOptions{
		DisableMetrics: []fsmtypes.AchillesMetrics{fsmtypes.AchillesResourceReadiness, fsmtypes.AchillesResourceCondition},
	})
	require.NoError(t, observeFirstReady(ctx, c, obj, m, zaptest.NewLogger(t).Sugar()))
	requireFirstReadyMetric(t, reg, obj, &firstReadyTime)
}

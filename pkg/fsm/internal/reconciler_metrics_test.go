package internal

import (
	"context"
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap/zaptest"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

// staleReadClient models the informer cache lagging behind a successful status write.
// The applicator uses the underlying API client, while reconciler reads remain stale.
type staleReadClient struct {
	client.Client
	stale *testv1alpha1.TestClaim
}

func (c *staleReadClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if claim, ok := obj.(*testv1alpha1.TestClaim); ok && key == client.ObjectKeyFromObject(c.stale) {
		*claim = *c.stale.DeepCopy()
		return nil
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

type failingStatusApplicator struct{ io.Applicator }

func (failingStatusApplicator) ApplyStatus(context.Context, client.Object, ...io.ApplyOption) error {
	return errors.New("status write failed")
}

func TestReconcilerReadinessMetricUsesPersistedStatus(t *testing.T) {
	for _, tc := range []struct {
		name          string
		statusWriteOK bool
	}{
		{name: "successful status write with stale cache", statusWriteOK: true},
		{name: "failed status write does not publish proposed readiness", statusWriteOK: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			initial := newTestClaim() // Ready=False
			apiClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(initial).WithStatusSubresource(initial).Build()
			applicator := io.Applicator(io.NewAPIPatchingApplicator(apiClient))
			if !tc.statusWriteOK {
				applicator = failingStatusApplicator{applicator}
			}
			cachedClient := &staleReadClient{Client: apiClient, stale: initial.DeepCopy()}
			c := &io.ClientApplicator{Client: cachedClient, Applicator: applicator}
			registry := prometheus.NewRegistry()
			m := metrics.MustMakeMetrics(scheme, registry)
			m.InitializeForGVK(meta.MustGVKForObject(initial, scheme))

			state := &types.State[*testv1alpha1.TestClaim]{
				Name:      "ready",
				Condition: api.Condition{Type: api.ConditionType("StateReady")},
				Transition: func(context.Context, *testv1alpha1.TestClaim, *types.OutputSet) (*types.State[*testv1alpha1.TestClaim], types.Result) {
					return nil, types.DoneResult()
				},
			}
			r := NewFSMReconciler[testv1alpha1.TestClaim, *testv1alpha1.TestClaim]("test", zaptest.NewLogger(t).Sugar(), c, scheme, state, nil, nil, m, types.ReconcilerOptions[testv1alpha1.TestClaim, *testv1alpha1.TestClaim]{})
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(initial)})
			if tc.statusWriteOK && err != nil {
				t.Fatalf("Reconcile() returned error: %v", err)
			}
			if !tc.statusWriteOK && (err == nil || err.Error() != "updating status: status write failed") {
				t.Fatalf("Reconcile() error = %v, want status write failure", err)
			}

			stored := &testv1alpha1.TestClaim{}
			if err := apiClient.Get(ctx, client.ObjectKeyFromObject(initial), stored); err != nil {
				t.Fatalf("fetching persisted claim: %v", err)
			}
			wantTrue := float64(0)
			if tc.statusWriteOK {
				wantTrue = 1
			}
			if got := stored.GetCondition(api.TypeReady).Status; (got == corev1.ConditionTrue) != tc.statusWriteOK {
				t.Fatalf("persisted Ready status = %s, want write success %t", got, tc.statusWriteOK)
			}
			if got := readinessGaugeValue(t, registry, initial.Name, string(metav1.ConditionTrue)); got != wantTrue {
				t.Errorf("Ready=True gauge = %v, want %v", got, wantTrue)
			}
			if got := readinessGaugeValue(t, registry, initial.Name, string(metav1.ConditionFalse)); got != 1-wantTrue {
				t.Errorf("Ready=False gauge = %v, want %v", got, 1-wantTrue)
			}
		})
	}
}

func readinessGaugeValue(t *testing.T, registry *prometheus.Registry, name, status string) float64 {
	t.Helper()
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gathering metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() != "achilles_resource_readiness" {
			continue
		}
		for _, metric := range family.GetMetric() {
			labels := map[string]string{}
			for _, label := range metric.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["name"] == name && labels["type"] == "Ready" && labels["status"] == status {
				return metric.GetGauge().GetValue()
			}
		}
	}
	t.Fatalf("readiness metric for %s status %s not found", name, status)
	return 0
}

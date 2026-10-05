package internal

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap/zaptest"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/reddit/achilles-sdk-api/api"
	"github.com/reddit/achilles-sdk/pkg/fsm/metrics"
	"github.com/reddit/achilles-sdk/pkg/fsm/types"
	internalscheme "github.com/reddit/achilles-sdk/pkg/internal/scheme"
	"github.com/reddit/achilles-sdk/pkg/internal/tests"
	testv1alpha1 "github.com/reddit/achilles-sdk/pkg/internal/tests/api/test/v1alpha1"
	"github.com/reddit/achilles-sdk/pkg/io"
	"github.com/reddit/achilles-sdk/pkg/test"
)

func TestReconcilerStatusOptimisticLock(t *testing.T) {
	ctx := context.Background()
	scheme := internalscheme.MustNewScheme()
	env, err := test.NewEnvTestBuilder(ctx).
		WithScheme(scheme).
		WithCRDDirectoryPaths([]string{filepath.Join(tests.RootDir(), "pkg", "internal", "tests", "cluster", "crd", "bases")}).
		Start()
	if err != nil {
		t.Fatalf("starting test environment: %v", err)
	}
	t.Cleanup(func() {
		if err := env.Stop(); err != nil {
			t.Errorf("stopping test environment: %v", err)
		}
	})
	c := &io.ClientApplicator{
		Client:     env.Client,
		Applicator: io.NewAPIPatchingApplicator(env.Client),
	}

	for _, tc := range []struct {
		name     string
		conflict bool
		outputs  bool
	}{
		{name: "final status conflict", conflict: true},
		{name: "resource refs conflict", conflict: true, outputs: true},
		{name: "successful intermediate writes", outputs: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			obj := &testv1alpha1.TestClaim{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "fsm-status-lock-",
					Namespace:    "default",
				},
			}
			if err := c.Create(ctx, obj); err != nil {
				t.Fatalf("creating claim: %v", err)
			}
			var terminalStatus testv1alpha1.TestClaimStatus
			initial := &types.State[*testv1alpha1.TestClaim]{
				Name:      "write-status",
				Condition: api.Condition{Type: "StateReady"},
				Transition: func(ctx context.Context, obj *testv1alpha1.TestClaim, out *types.OutputSet) (*types.State[*testv1alpha1.TestClaim], types.Result) {
					if tc.conflict {
						stored := obj.DeepCopy()
						stored.Status.TestField = "terminal"
						if err := c.Status().Update(ctx, stored); err != nil {
							t.Fatalf("persisting newer terminal status: %v", err)
						}
						terminalStatus = *stored.Status.DeepCopy()
						obj.Status.TestField = "nonterminal"
						if !tc.outputs {
							// Return before applyOutputs to isolate the final status write.
							return nil, types.RequeueResult("waiting", time.Second)
						}
					} else {
						// This and the resource refs write must leave obj's version current
						// for the final optimistic status write to succeed.
						obj.Status.TestField = "intermediate"
						if err := c.Status().Update(ctx, obj); err != nil {
							t.Fatalf("persisting intermediate status: %v", err)
						}
						obj.Status.TestField = "finished"
					}
					out.Apply(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: obj.Name, Namespace: obj.Namespace}})
					return nil, types.DoneResult()
				},
			}
			m := metrics.MustMakeMetrics(scheme, prometheus.NewRegistry())
			m.InitializeForGVK(testv1alpha1.GroupVersion.WithKind("TestClaim"))
			r := NewFSMReconciler[testv1alpha1.TestClaim, *testv1alpha1.TestClaim](
				"status-lock-test", zaptest.NewLogger(t).Sugar(), c, scheme, initial,
				&types.State[*testv1alpha1.TestClaim]{Name: "finalize"},
				[]schema.GroupVersionKind{corev1.SchemeGroupVersion.WithKind("ConfigMap")},
				m,
				types.ReconcilerOptions[testv1alpha1.TestClaim, *testv1alpha1.TestClaim]{},
			)
			_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(obj)})
			stored := &testv1alpha1.TestClaim{}
			if err := c.Get(ctx, client.ObjectKeyFromObject(obj), stored); err != nil {
				t.Fatalf("fetching persisted claim: %v", err)
			}
			if tc.conflict {
				if !k8serrors.IsConflict(err) {
					t.Fatalf("Reconcile() error = %v, want conflict", err)
				}
				if diff := cmp.Diff(terminalStatus, stored.Status); diff != "" {
					t.Errorf("terminal status changed after conflict (-want +got):\n%s", diff)
				}
			} else {
				if err != nil {
					t.Fatalf("Reconcile() returned error: %v", err)
				}
				if stored.Status.TestField != "finished" {
					t.Errorf("persisted status = %q, want finished", stored.Status.TestField)
				}
				if len(stored.Status.ResourceRefs) != 1 {
					t.Errorf("persisted resource refs = %v, want one reference", stored.Status.ResourceRefs)
				}
			}
		})
	}
}

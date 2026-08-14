package internal

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap/zaptest"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/reddit/achilles-sdk-api/api"
	"github.com/reddit/achilles-sdk/pkg/fsm/metrics"
	fsmtypes "github.com/reddit/achilles-sdk/pkg/fsm/types"
	"github.com/reddit/achilles-sdk/pkg/internal/tests/api/test/v1alpha1"
)

const testFSMObjectName = "test-fsm-object"

var errStatusPatch = errors.New("status patch failed")

// TestReconcile_ContextDone asserts that errors encountered after the reconcile context is done are not surfaced as
// reconcile errors. The manager cancels the context of every in-flight reconcile when it shuts down, which fails all
// of their outstanding API requests.
func TestReconcile_ContextDone(t *testing.T) {
	cases := []struct {
		name      string
		cancelCtx bool
		wantErr   error
	}{
		{
			name:    "surfaces_status_update_errors",
			wantErr: errStatusPatch,
		},
		{
			name:      "ignores_errors_once_context_is_done",
			cancelCtx: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancelCtx {
				cancel()
			}

			obj := &v1alpha1.TestClaim{ObjectMeta: metav1.ObjectMeta{Name: testFSMObjectName}}
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(obj).
				WithStatusSubresource(obj).
				Build()

			// mimic the API client, which fails all requests once their context is done
			c := interceptor.NewClient(fakeClient, interceptor.Funcs{
				SubResourcePatch: func(
					ctx context.Context,
					_ client.Client,
					_ string,
					_ client.Object,
					_ client.Patch,
					_ ...client.SubResourcePatchOption,
				) error {
					if err := ctx.Err(); err != nil {
						return fmt.Errorf("client rate limiter Wait returned an error: %w", err)
					}
					return errStatusPatch
				},
			})

			r := NewFSMReconciler[v1alpha1.TestClaim, *v1alpha1.TestClaim](
				"test-fsm",
				zaptest.NewLogger(t).Sugar(),
				testApplicator(c),
				scheme,
				&fsmtypes.State[*v1alpha1.TestClaim]{
					Name:      "initial",
					Condition: api.Condition{Type: "Initial"},
				},
				nil,
				nil,
				metrics.MustMakeMetrics(scheme, prometheus.NewRegistry()),
				fsmtypes.ReconcilerOptions[v1alpha1.TestClaim, *v1alpha1.TestClaim]{},
			)

			res, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKey{Name: testFSMObjectName}})

			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("expected no error, got: %s", err)
				}
				if !res.IsZero() {
					t.Errorf("expected an empty result, got: %+v", res)
				}
				return
			}

			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error running reconciler did not match expected\ngot: %v\nwant: %s", err, tc.wantErr)
			}
		})
	}
}

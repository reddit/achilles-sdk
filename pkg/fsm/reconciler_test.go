package fsm_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/reddit/achilles-sdk-api/api"
	"github.com/reddit/achilles-sdk/pkg/fsm"
	"github.com/reddit/achilles-sdk/pkg/fsm/metrics"
	fsmtypes "github.com/reddit/achilles-sdk/pkg/fsm/types"
	internalscheme "github.com/reddit/achilles-sdk/pkg/internal/scheme"
	"github.com/reddit/achilles-sdk/pkg/internal/tests/api/test/v1alpha1"
)

const (
	applyOutputsConditionType = "ApplyOutputs"
	applyOutputsCMName        = "output-cm"
)

var scheme = func() *runtime.Scheme {
	s := internalscheme.MustNewScheme()
	if err := v1alpha1.AddToScheme(s); err != nil {
		panic(fmt.Sprintf("failed to initialize test scheme: %s", err))
	}
	return s
}()

func TestReconciler_ApplyOutputsErrors(t *testing.T) {
	const (
		claimName      = "test-claim"
		claimNamespace = "default"
	)

	claim := &v1alpha1.TestClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      claimName,
			Namespace: claimNamespace,
		},
	}

	conflictErr := k8serrors.NewConflict(
		corev1.Resource("configmaps"),
		applyOutputsCMName,
		errors.New("the object has been modified"),
	)
	internalErr := errors.New("connection refused")

	tcs := []struct {
		name           string
		createErr      error
		wantRequeue    bool
		wantErrContain string
		wantReason     string
		wantMsgContain string
	}{
		{
			name:           "conflict requeues without error",
			createErr:      conflictErr,
			wantRequeue:    true,
			wantReason:     "ApplyOutputsConflict",
			wantMsgContain: "conflict applying outputs",
		},
		{
			name:           "non-conflict returns error",
			createErr:      internalErr,
			wantErrContain: "applying outputs",
			wantReason:     "ApplyOutputsFailed",
			wantMsgContain: "connection refused",
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			req := reconcile.Request{NamespacedName: k8stypes.NamespacedName{
				Name:      claimName,
				Namespace: claimNamespace,
			}}

			fakeC := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(claim.DeepCopy()).
				WithStatusSubresource(claim.DeepCopy()).
				WithInterceptorFuncs(interceptor.Funcs{
					Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
						if obj.GetName() == applyOutputsCMName {
							return tc.createErr
						}
						return c.Create(ctx, obj, opts...)
					},
				}).
				Build()

			r := newApplyOutputsReconciler(t, fakeC)

			result, err := r.Reconcile(ctx, req)
			if tc.wantErrContain != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErrContain)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tc.wantRequeue, result.Requeue)

			gotClaim := &v1alpha1.TestClaim{}
			require.NoError(t, fakeC.Get(ctx, req.NamespacedName, gotClaim))

			cond := gotClaim.GetCondition(applyOutputsConditionType)
			assert.Equal(t, corev1.ConditionFalse, cond.Status)
			assert.Equal(t, api.ConditionReason(tc.wantReason), cond.Reason)
			assert.Contains(t, cond.Message, tc.wantMsgContain)
		})
	}
}

func newApplyOutputsReconciler(t *testing.T, fakeC client.Client) reconcile.Reconciler {
	t.Helper()

	log := zaptest.NewLogger(t).Sugar()
	m := metrics.MustMakeMetrics(scheme, prometheus.NewRegistry())
	m.InitializeForGVK(v1alpha1.TestClaimGroupVersionKind)

	initialState := &fsmtypes.State[*v1alpha1.TestClaim]{
		Name: "apply-outputs",
		Condition: api.Condition{
			Type:    applyOutputsConditionType,
			Message: "Applying outputs",
		},
		Transition: func(_ context.Context, claim *v1alpha1.TestClaim, out *fsmtypes.OutputSet) (*fsmtypes.State[*v1alpha1.TestClaim], fsmtypes.Result) {
			out.Apply(&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      applyOutputsCMName,
					Namespace: claim.GetNamespace(),
				},
			})
			return nil, fsmtypes.DoneResult()
		},
	}

	return fsm.NewBuilder(&v1alpha1.TestClaim{}, initialState, scheme).
		Manages(corev1.SchemeGroupVersion.WithKind("ConfigMap")).
		Reconciler(log, scheme, fakeC, m)
}

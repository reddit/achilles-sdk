package claim

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/reddit/achilles-sdk-api/api"
	"github.com/reddit/achilles-sdk/pkg/fsm"
	"github.com/reddit/achilles-sdk/pkg/fsm/metrics"
	fsmtypes "github.com/reddit/achilles-sdk/pkg/fsm/types"
	"github.com/reddit/achilles-sdk/pkg/internal/tests/api/test/v1alpha1"
	"github.com/reddit/achilles-sdk/pkg/io"
)

type state = fsmtypes.State[*v1alpha1.TestClaimed]

type reconciler struct {
	log *zap.SugaredLogger
	c   *io.ClientApplicator
}

func setupTestClaimController(
	log *zap.SugaredLogger,
	mgr ctrl.Manager,
	rl workqueue.TypedRateLimiter[reconcile.Request],
	c *io.ClientApplicator,
	metrics *metrics.Metrics,
) error {
	r := &reconciler{
		log: log,
		c:   c,
	}

	builder := fsm.NewClaimBuilder(
		&v1alpha1.TestClaimed{},
		&v1alpha1.TestClaim{},
		r.initialState(),
		mgr.GetScheme(),
	).
		BeforeDelete(func(claim *v1alpha1.TestClaim, _ *v1alpha1.TestClaimed) error {
			if claim.Spec.DontDelete {
				return fmt.Errorf("DontDelete flag is set")
			}
			return nil
		}).
		WithMaxConcurrentReconciles(5). // exercise concurrency to detect any race conditions caused by the FSM reconciler
		Manages(
			corev1.SchemeGroupVersion.WithKind("ConfigMap"),
		)

	return builder.Build()(mgr, log, rl, metrics)
}

func (r *reconciler) initialState() *state {
	return &state{
		Name:       "initial",
		Condition:  api.Creating(),
		Transition: r.initialStateFunc,
	}
}

func (r *reconciler) initialStateFunc(
	ctx context.Context,
	claimed *v1alpha1.TestClaimed,
	out *fsmtypes.OutputSet,
) (*state, fsmtypes.Result) {
	claim, err := getClaim(ctx, claimed, r.c)
	if err != nil {
		return nil, fsmtypes.ErrorResult(fmt.Errorf("getting claim: %w", err))
	}

	managed := &corev1.ConfigMap{}
	managed.Name = "foo"
	managed.Namespace = "default"
	managed.Data = map[string]string{
		"test": claim.Spec.TestField,
	}
	out.Apply(managed)

	return r.intermediateState(), fsmtypes.DoneResult()
}

func (r *reconciler) intermediateState() *state {
	return &state{
		Name:       "intermediate",
		Condition:  api.ReconcileSuccess(),
		Transition: r.intermediateStateMap,
	}
}

func (r *reconciler) intermediateStateMap(
	_ context.Context,
	claimed *v1alpha1.TestClaimed,
	_ *fsmtypes.OutputSet,
) (*state, fsmtypes.Result) {
	if claimed.Spec.Success {
		return r.successState(), fsmtypes.DoneResult()
	}

	return nil, fsmtypes.DoneResult()
}

func (r *reconciler) successState() *state {
	return &state{
		Name:      "success",
		Condition: api.Available(),
	}
}

func getClaim(ctx context.Context, claimed *v1alpha1.TestClaimed, c *io.ClientApplicator) (*v1alpha1.TestClaim, error) {
	claim := &v1alpha1.TestClaim{}
	err := c.Get(ctx, claimed.GetClaimRef().ObjectKey(), claim)
	return claim, err
}

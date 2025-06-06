package core

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlhandler "sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	achapi "github.com/reddit/achilles-sdk-api/api"
	"github.com/reddit/achilles-sdk/pkg/fsm"
	"github.com/reddit/achilles-sdk/pkg/fsm/handler"
	"github.com/reddit/achilles-sdk/pkg/fsm/metrics"
	fsmtypes "github.com/reddit/achilles-sdk/pkg/fsm/types"
	testv1alpha1 "github.com/reddit/achilles-sdk/pkg/internal/tests/api/test/v1alpha1"
	"github.com/reddit/achilles-sdk/pkg/io"
	"github.com/reddit/achilles-sdk/pkg/meta"
)

type state = fsmtypes.State[*testv1alpha1.TestClaim]

type reconciler struct {
	log    *zap.SugaredLogger
	c      *io.ClientApplicator
	scheme *runtime.Scheme
}

const (
	InitialStateConditionType   = "InitialState"
	FinalizerStateConditionType = "FinalizerState"
)

func fsmBuilder(
	log *zap.SugaredLogger,
	mgr ctrl.Manager,
	c *io.ClientApplicator,
	disableAutoCreate *atomic.Bool,
) *fsm.Builder[testv1alpha1.TestClaim, *testv1alpha1.TestClaim] {
	r := &reconciler{
		log:    log,
		c:      c,
		scheme: mgr.GetScheme(),
	}

	builder := fsm.NewBuilder(
		&testv1alpha1.TestClaim{},
		r.initialState(),
		mgr.GetScheme(),
	).
		WithFinalizerState(r.finalizerState()).
		WithMaxConcurrentReconciles(5). // exercise concurrency to detect any race conditions caused by the FSM reconciler
		Manages(
			corev1.SchemeGroupVersion.WithKind("ConfigMap"),
		).
		WithReconcilerOptions(
			fsmtypes.ReconcilerOptions[testv1alpha1.TestClaim, *testv1alpha1.TestClaim]{
				MetricsOptions: fsmtypes.MetricsOptions{
					ConditionTypes: []achapi.ConditionType{
						InitialStateConditionType,
					},
				},
				// exercise automatic creation feature
				CreateIfNotFound: true,
				CreateFunc: func(req ctrl.Request) *testv1alpha1.TestClaim {
					// only create the resource if it's named "test-create-func"
					if req.Name != "test-create-func" {
						return nil
					}
					// don't recreate if disabled (for exercising proper cleanup)
					if disableAutoCreate.Load() {
						return nil
					}

					return &testv1alpha1.TestClaim{
						ObjectMeta: metav1.ObjectMeta{
							Name:      req.Name,
							Namespace: req.Namespace,
						},
					}
				},
			},
		).
		WithSkipNameValidation().
		Watches(&corev1.ConfigMap{},
			// trigger auto creation of `test-create-func` TestClaim iff a ConfigMap of name `test-create-func` is created
			ctrlhandler.EnqueueRequestsFromMapFunc(func(ctx context.Context, o client.Object) []reconcile.Request {
				if o.GetName() == "test-create-func" {
					return []reconcile.Request{{NamespacedName: client.ObjectKeyFromObject(o)}}
				}
				return nil
			},
			), handler.TriggerTypeRelative)

	return builder
}

func BuildReconciler(
	log *zap.SugaredLogger,
	mgr ctrl.Manager,
	c *io.ClientApplicator,
	metrics *metrics.Metrics,
	disableAutoCreate *atomic.Bool,
) reconcile.TypedReconciler[ctrl.Request] {
	return fsmBuilder(log, mgr, c, disableAutoCreate).Reconciler(log, mgr.GetScheme(), c, metrics)
}

func SetupController(
	log *zap.SugaredLogger,
	mgr ctrl.Manager,
	rl workqueue.TypedRateLimiter[reconcile.Request],
	c *io.ClientApplicator,
	metrics *metrics.Metrics,
	disableAutoCreate *atomic.Bool,
) error {
	return fsmBuilder(log, mgr, c, disableAutoCreate).Build()(mgr, log, rl, metrics)
}

func (r *reconciler) initialState() *state {
	return &state{
		Name: "initial-state",
		Condition: achapi.Condition{
			Type:    InitialStateConditionType,
			Message: "This is the initial state of the FSM",
		},
		Transition: r.initialStateFunc,
	}
}

func (r *reconciler) initialStateFunc(
	ctx context.Context,
	claim *testv1alpha1.TestClaim,
	out *fsmtypes.OutputSet,
) (*state, fsmtypes.Result) {
	// return error if foo namespace doesn't exist
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "foo",
		},
	}
	if err := r.c.Get(ctx, client.ObjectKeyFromObject(ns), ns); err != nil {
		return nil, fsmtypes.ErrorResultWithReason(errors.New("foo namespace not found"), "FooNamespaceNotFound")
	}

	// return requeue if foo namespace doesn't have expected annotation
	if len(ns.GetAnnotations()) < 1 {
		return nil, fsmtypes.RequeueResultWithReason("foo namespace missing annotation", "FooNamespaceMissingAnnotation", 5*time.Second)
	}

	if claim.Spec.TestField != claim.Status.TestField {
		claim.Status.TestField = claim.Spec.TestField
		if err := r.c.Status().Update(ctx, claim); err != nil {
			return nil, fsmtypes.ErrorResult(fmt.Errorf("updating status: %w", err))
		}
	}

	return r.provisionConfigMapState(), fsmtypes.DoneResult()
}

func (r *reconciler) provisionConfigMapState() *state {
	return &state{
		Name: "config-map-provisioned",
		Condition: achapi.Condition{
			Type:    "ConfigMapProvisioned",
			Message: "ConfigMap has been provisioned",
		},
		Transition: r.provisionConfigMap,
	}
}

func (r *reconciler) provisionConfigMap(
	_ context.Context,
	claim *testv1alpha1.TestClaim,
	out *fsmtypes.OutputSet,
) (*state, fsmtypes.Result) {
	desiredCMName := ptr.Deref(claim.Spec.ConfigMapName, "")
	currentCMName := ptr.Deref(claim.Status.ConfigMapName, "")

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      desiredCMName,
			Namespace: "default",
		},
	}

	// deletion cases
	// if current CM exists AND (desired CM is empty OR desired CM is different than current)
	if len(currentCMName) > 0 && (len(desiredCMName) == 0 || currentCMName != desiredCMName) {
		// delete resources if they exist
		cm := cm.DeepCopy()
		cm.SetName(currentCMName)
		out.DeleteByRef(*meta.MustTypedObjectRefFromObject(cm, r.scheme))
		claim.Status.ConfigMapName = ptr.To("")
	}

	// creation case
	if len(desiredCMName) > 0 {
		out.Apply(cm)
		claim.Status.ConfigMapName = ptr.To(desiredCMName)
	}

	// create two extra children for testing DeleteChildrenForeground state
	finalizerCM1 := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "finalizer-child-1",
			Namespace:  "default",
			Finalizers: []string{"infrared.reddit.com/test-finalizer"},
		},
	}
	finalizerCM2 := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "finalizer-child-2",
			Namespace:  "default",
			Finalizers: []string{"infrared.reddit.com/test-finalizer"},
		},
	}
	out.ApplyAll(finalizerCM1, finalizerCM2)

	return r.testResultTypes(), fsmtypes.DoneResult()
}

func (r *reconciler) testResultTypes() *state {
	return &state{
		Name: "custom-status-condition-state-name",
		Condition: achapi.Condition{
			Type:    "custom-status-condition",
			Message: "default message",
			Reason:  "default reason",
		},
		Transition: func(
			ctx context.Context,
			claim *testv1alpha1.TestClaim,
			out *fsmtypes.OutputSet,
		) (next *fsmtypes.State[*testv1alpha1.TestClaim], result fsmtypes.Result) {
			if resultType, ok := claim.GetAnnotations()["result-type"]; ok {
				switch resultType {
				case "done":
					return nil, fsmtypes.DoneResult()
				case "done-with-status-condition":
					return nil, fsmtypes.DoneResultWithStatusCondition(fsmtypes.ResultStatusCondition{
						Status:  corev1.ConditionFalse,
						Reason:  "Test custom reason",
						Message: "Test custom message",
					})
				case "done-and-requeue":
					return nil, fsmtypes.DoneAndRequeueResult("Done and requeue message", 5*time.Second)
				case "requeue-with-backoff":
					return nil, fsmtypes.RequeueResultWithBackoff("Requeue with backoff")
				case "requeue-with-reason":
					return nil, fsmtypes.RequeueResultWithReason("Requeue with reason", "RequeueWithReason", 5*time.Second)
				case "requeue-with-reason-and-backoff":
					return nil, fsmtypes.RequeueResultWithReasonAndBackoff("Requeue with reason and backoff", "RequeueWithReasonAndBackoff")
				case "error":
					return nil, fsmtypes.ErrorResult(fmt.Errorf("error result"))
				case "error-with-reason":
					return nil, fsmtypes.ErrorResultWithReason(fmt.Errorf("error result"), "ErrorReason")
				case "requeue-after-completion-with-backoff":
					return r.noopState(), fsmtypes.DoneAndRequeueAfterCompletionWithBackoff("Done and requeue after completion with backoff")
				case "requeue-after-completion":
					return r.noopState(), fsmtypes.DoneAndRequeueAfterCompletion("Done and requeue after completion", 30*time.Second)
				}
			}

			return nil, fsmtypes.DoneResult()
		},
	}
}

// add a terminal noop state to exercise DoneAndRequeueAfterCompletion + DoneAndRequeueAfterCompletionWithBackoff
func (r *reconciler) noopState() *state {
	return &state{
		Name: "terminal-noop-state",
		Condition: achapi.Condition{
			Type:    "noop-state",
			Message: "default message",
			Reason:  "default reason",
		},
		Transition: func(
			ctx context.Context,
			claim *testv1alpha1.TestClaim,
			out *fsmtypes.OutputSet,
		) (next *fsmtypes.State[*testv1alpha1.TestClaim], result fsmtypes.Result) {
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name: fmt.Sprintf("noop-state-for-%s-%s", claim.Namespace, claim.Name),
				},
			}
			if err := r.c.Create(ctx, cm); err != nil {
				return nil, fsmtypes.ErrorResult(fmt.Errorf("creating configmap: %w", err))
			}
			return nil, fsmtypes.DoneResult()
		},
	}
}

func (r *reconciler) finalizerState() *state {
	return &state{
		Name: "",
		Condition: achapi.Condition{
			Type:    FinalizerStateConditionType,
			Message: "Deleting resources",
		},
		Transition: r.finalizer,
	}
}

func (r *reconciler) finalizer(
	ctx context.Context,
	_ *testv1alpha1.TestClaim,
	_ *fsmtypes.OutputSet,
) (*state, fsmtypes.Result) {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "foo",
		},
	}
	if err := r.c.Get(ctx, client.ObjectKeyFromObject(ns), ns); err != nil {
		return nil, fsmtypes.ErrorResultWithReason(errors.New("foo namespace not found"), "FooNamespaceNotFound")
	}

	// return error if foo namespace doesn't have expected annotation
	if len(ns.GetAnnotations()) < 2 {
		return nil, fsmtypes.RequeueResultWithReason("foo namespace missing two annotations", "FooNamespaceMissingAnnotations", 5*time.Second)
	}

	return r.deleteChildrenForegroundState(), fsmtypes.DoneResult()
}

func (r *reconciler) deleteChildrenForegroundState() *state {
	return &state{
		Name: "children-deleted",
		Condition: achapi.Condition{
			Type:    "ChildrenDeleted",
			Message: "Children have been deleted",
		},
		Transition: fsmtypes.DeleteChildrenForeground[*testv1alpha1.TestClaim](r.c, r.scheme, r.log, nil),
	}
}

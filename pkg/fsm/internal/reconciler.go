package internal

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"
	"golang.org/x/exp/slices"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlcontroller "sigs.k8s.io/controller-runtime/pkg/controller"

	"github.com/reddit/achilles-sdk-api/api"
	apitypes "github.com/reddit/achilles-sdk-api/pkg/types"
	fsmio "github.com/reddit/achilles-sdk/pkg/fsm/io"
	"github.com/reddit/achilles-sdk/pkg/fsm/metrics"
	"github.com/reddit/achilles-sdk/pkg/fsm/types"
	"github.com/reddit/achilles-sdk/pkg/io"
	"github.com/reddit/achilles-sdk/pkg/meta"
	"github.com/reddit/achilles-sdk/pkg/status"
)

const (
	deletedStateName = "deleted"
	finalizerKey     = "infrared.reddit.com/fsm"
)

var errStateLoop = errors.New("re-entered state")

type fsmReconciler[T any, Obj apitypes.FSMResource[T]] struct {
	log    *zap.SugaredLogger
	client *io.ClientApplicator
	scheme *runtime.Scheme

	name           string
	initialState   *types.State[Obj]
	finalizerState *types.State[Obj]
	managedTypes   map[schema.GroupVersionKind]struct{}

	metrics *metrics.Metrics

	reconcilerOptions types.ReconcilerOptions[T, Obj]
}

func NewFSMReconciler[T any, Obj apitypes.FSMResource[T]](
	name string,
	log *zap.SugaredLogger,
	client *io.ClientApplicator,
	scheme *runtime.Scheme,
	initialState *types.State[Obj],
	finalizerState *types.State[Obj],
	managedTypes []schema.GroupVersionKind,
	metrics *metrics.Metrics,
	reconcilerOptions types.ReconcilerOptions[T, Obj],
) *fsmReconciler[T, Obj] {
	managedTypesMap := map[schema.GroupVersionKind]struct{}{}
	for _, t := range managedTypes {
		managedTypesMap[t] = struct{}{}
	}

	if reconcilerOptions.CreateFunc == nil {
		reconcilerOptions.CreateFunc = types.DefaultCreateFunc[T, Obj]
	}

	return &fsmReconciler[T, Obj]{
		log:               log,
		client:            client,
		scheme:            scheme,
		name:              name,
		initialState:      initialState,
		finalizerState:    finalizerState,
		managedTypes:      managedTypesMap,
		metrics:           metrics,
		reconcilerOptions: reconcilerOptions,
	}
}

func (r *fsmReconciler[T, Obj]) Reconcile(ctx context.Context, req ctrl.Request) (res ctrl.Result, err error) {
	requestId := ctrlcontroller.ReconcileIDFromContext(ctx)
	log := r.log.With("request", req, "requestId", requestId)
	log.Debug("entering reconcile")
	startedAt := time.Now()
	defer func() { log.Debugf("finished reconcile in %s", time.Since(startedAt)) }()

	// record metrics
	defer func() {
		// fetch the object's latest state
		obj := Obj(new(T))
		if err := r.client.Get(ctx, req.NamespacedName, obj); err != nil {
			if !k8serrors.IsNotFound(err) {
				log.Error("fetching object for recording metrics: %w", err)
			}
			return
		}

		// record status condition metric for custom condition types
		for _, conditionType := range r.reconcilerOptions.MetricsOptions.ConditionTypes {
			r.metrics.RecordCondition(obj, conditionType)
		}

		// record processing duration
		var success bool
		if res.IsZero() && err == nil {
			success = true
		}
		if err := r.metrics.RecordProcessingDuration(meta.MustTypedObjectRefFromObject(obj, r.scheme).GroupVersionKind(), req, obj.GetGeneration(), success); err != nil {
			log.Errorf("recording processing duration: %s", err.Error())
		}

		// record object readiness
		r.metrics.RecordReadiness(obj)
	}()

	obj, conditions, result := r.reconcile(ctx, req, log)
	if obj == nil {
		return result.Get(log)
	}

	// merge computed conditions
	if conditions != nil {
		// set top level ready status condition
		if !r.reconcilerOptions.DisableReadyCondition {
			if result.IsDone() {
				conditions.SetConditions(status.NewReadyCondition(obj.GetGeneration()))
			} else {
				conditions.SetConditions(status.NewUnreadyCondition(obj.GetGeneration()))
			}
		}

		obj.SetConditions(conditions.GetConditions()...)

		// NOTE: status must be updated upon termination of FSM, otherwise steady state won't be reached because
		// later states that overwrite status conditions of earlier states will trigger reconcile events
		if err := r.client.ApplyStatus(ctx, obj); err != nil {
			return ctrl.Result{}, fmt.Errorf("updating status: %w", err)
		}
	}

	// For FSMs with finalizer states, remove finalizer when finalizer states have been completed.
	// NB: If the object has a non-zero deletion timestamp, its finalizer states are guaranteed to be processed
	// But this invariant relies on the object never being fetched from the server mid-reconcile,
	// which can lead to `meta.WasDeleted(obj) == true` and `result.IsDone()` without the finalizer states
	// having been processed (if, for instance, an external actor deletes the object after `r.reconcile(ctx, req)`
	// and before this condition.
	if meta.WasDeleted(obj) && r.finalizerState != nil && result.IsDone() {
		if err := meta.RemoveFinalizer(ctx, r.client, obj, finalizerKey); err != nil {
			return ctrl.Result{}, fmt.Errorf("removing FSM finalizer: %w", err)
		}
	}

	return result.Get(log)
}

// reconcile the object through a sequence of FSM states
// return the mutated object, status conditions, and result
func (r *fsmReconciler[T, Obj]) reconcile(
	ctx context.Context,
	req ctrl.Request,
	log *zap.SugaredLogger,
) (Obj, api.Conditioned, types.Result) {
	obj := Obj(new(T))
	if err := r.client.Get(ctx, req.NamespacedName, obj); k8serrors.IsNotFound(err) {
		// object not found, meaning that it has been deleted (not merely in terminating state)

		if r.reconcilerOptions.CreateIfNotFound {
			obj := r.reconcilerOptions.CreateFunc(req)
			// Create the object supplied by the caller if not nil.
			if obj != nil {
				// already exists error can occur if the CreateFunc sets the object name to something other than req.Name
				if err := r.client.Create(ctx, obj); client.IgnoreAlreadyExists(err) != nil {
					return nil, nil, types.ErrorResult(fmt.Errorf("creating object %s: %w", req.NamespacedName, err))
				}
				// NOTE: wait for next reconcile before updating status to reduce "object does not exist, cannot update its status" errors
				return nil, nil, types.DoneResult()
			}

			// If obj is nil, the caller signals that the object should not be created. This is primarily used by callers to prevent
			// stale requests in the event queue from triggering unneeded object creations.
		}

		// deregister metrics for deleted objects (to keep metrics cardinality count from monotonically increasing over an application's lifetime)
		r.metrics.DeleteTrigger(req.NamespacedName, r.name)

		obj.SetName(req.Name)
		obj.SetNamespace(req.Namespace)
		r.metrics.DeleteReadiness(obj)

		for _, conditionType := range r.reconcilerOptions.MetricsOptions.ConditionTypes {
			r.metrics.DeleteCondition(obj, conditionType)
		}

		return nil, nil, types.DoneResult()
	} else if err != nil {
		return nil, nil, types.ErrorResult(fmt.Errorf("getting %T: %w", obj, err))
	}

	isSuspended := meta.HasSuspendLabel(obj)
	r.metrics.RecordSuspend(obj, isSuspended)
	if isSuspended {
		log.Infof("Skipping reconciliation, the label %s is set", meta.SuspendKey)
		return nil, nil, types.DoneResult()
	}

	// ensure finalizer if finalizer states exist, do not add if the resource has already been deleted
	// as no new finalizers can be added to the resource
	if r.finalizerState != nil && !slices.Contains(obj.GetFinalizers(), finalizerKey) && !meta.WasDeleted(obj) {
		if err := meta.AddFinalizer(ctx, r.client, obj, finalizerKey); err != nil {
			return nil, nil, types.ErrorResult(fmt.Errorf("adding FSM finalizer: %w", err))
		}
	}

	// transition through states
	currentState := r.initialState
	// transition through finalizer states
	if meta.WasDeleted(obj) {
		currentState = DeletedStateFor(r) // default deleted state when finalizer states aren't provided
		if r.finalizerState != nil {
			currentState = r.finalizerState
		}
	}

	// empty object for accumulating conditions
	conditions := Obj(new(T))

	// transition state
	seenStates := sets.NewString()

	for currentState != nil {
		log.Debugw("entering state", "state", currentState.Name)
		// record seen states to prevent loops
		if seenStates.Has(currentState.Name) {
			return obj, conditions, types.ErrorResult(fmt.Errorf("%w %q", errStateLoop, currentState.Name))
		}
		seenStates.Insert(currentState.Name)

		// initialize output set scoped to the current state
		out := types.NewOutputSet(r.scheme)
		condition := *currentState.Condition.DeepCopy()    // copy the status condition so we can mutate its fields in a thread-safe manner
		condition.ObservedGeneration = obj.GetGeneration() // set observed generation to the object's generation

		// transition if a transition func is defined, else it's a terminal state
		var next *types.State[Obj]

		var result types.Result
		if currentState.Transition != nil {
			// obj, managedResources, and out can be mutated

			start := time.Now()
			next, result = currentState.Transition(ctx, obj, out)

			typedObjectRef := meta.MustTypedObjectRefFromObject(obj, r.scheme)
			r.metrics.RecordStateDuration(typedObjectRef.GroupVersionKind(), currentState.Name, time.Since(start))

			condition.LastTransitionTime = metav1.Now() // set status condition last transition time
			condition.Status = corev1.ConditionTrue     // set status condition to true if state is done

			if !result.IsDone() {
				// falsify condition if provided, set message and reason
				if !condition.IsEmpty() {
					condition.Status = corev1.ConditionFalse
					condition.Message, condition.Reason = result.GetMessageAndReason()
					conditions.SetConditions(condition)
				}
				return obj, conditions, result.WrapError(fmt.Sprintf("transitioning state %q", currentState.Name))
			}
		}

		if err := r.applyOutputs(ctx, log, obj, out); err != nil {
			return obj, conditions, types.ErrorResult(fmt.Errorf("applying outputs: %w", err))
		}

		// accumulate status conditions, overwrites duplicate conditions with those of later states
		if !condition.IsEmpty() {
			conditions.SetConditions(condition)
		}

		// for requeue results, requeue instead of proceeding to the following state
		if result.HasRequeue() {
			return obj, conditions, result
		}

		// update state
		currentState = next
	}

	return obj, conditions, types.DoneResult()
}

func (r *fsmReconciler[T, Obj]) applyOutputs(
	ctx context.Context,
	log *zap.SugaredLogger,
	obj Obj,
	outputSet *types.OutputSet,
) error {
	for _, res := range outputSet.ListApplied() {
		// guard against undeclared output types
		gvk := meta.MustGVKForObject(res, r.scheme)
		if _, ok := r.managedTypes[gvk]; !ok {
			log.DPanicf("unrecognized output resource type %s, must be added to managed types", gvk)
		}
		meta.SetRedditLabels(res, r.name)
	}
	return fsmio.ApplyOutputSet(ctx, r.log, r.client, r.scheme, obj, outputSet)
}

func DeletedStateFor[T any, Obj apitypes.FSMResource[T]](_ *fsmReconciler[T, Obj]) *types.State[Obj] {
	return &types.State[Obj]{
		Name:      deletedStateName,
		Condition: api.Deleting(), // Ready = false, deleting
	}
}

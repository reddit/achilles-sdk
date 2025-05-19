package fsm

import (
	"context"
	"fmt"

	"github.com/iancoleman/strcase"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlbuilder "sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	apitypes "github.com/reddit/achilles-sdk-api/pkg/types"
	fsmhandler "github.com/reddit/achilles-sdk/pkg/fsm/handler"
	"github.com/reddit/achilles-sdk/pkg/fsm/internal"
	"github.com/reddit/achilles-sdk/pkg/fsm/metrics"
	"github.com/reddit/achilles-sdk/pkg/fsm/types"
	"github.com/reddit/achilles-sdk/pkg/io"
	"github.com/reddit/achilles-sdk/pkg/meta"
	"github.com/reddit/achilles-sdk/pkg/ratelimiter"
)

// ClaimBuilder is a builder for an FSM controller managing a pair of claimed and claim resources.
type ClaimBuilder[T any, U any, ClaimedType apitypes.ClaimedType[T], ClaimType apitypes.ClaimType[U]] struct {
	obj                     ClaimedType
	claim                   ClaimType
	scheme                  *runtime.Scheme
	initialState            *types.State[ClaimedType]
	finalizerState          *types.State[ClaimedType]
	beforeDelete            internal.BeforeDelete[T, ClaimedType, U, ClaimType]
	managedTypes            []schema.GroupVersionKind
	controllerFns           []ControllerFunc
	watches                 []watch
	watchRemoteKinds        []watchRemoteKind
	watchRawSources         []source.Source
	opts                    []buildOption
	maxConcurrentReconciles int
}

// NewClaimBuilder returns a builder that builds a function wiring up a logical FSM controller to a manager.
// Obj is the object being reconciled and initialState is the initial state in the finite state machine,
func NewClaimBuilder[T any, U any, ClaimedType apitypes.ClaimedType[T], ClaimType apitypes.ClaimType[U]](
	obj ClaimedType,
	claim ClaimType,
	initialState *types.State[ClaimedType],
	scheme *runtime.Scheme,
) *ClaimBuilder[T, U, ClaimedType, ClaimType] {
	return &ClaimBuilder[T, U, ClaimedType, ClaimType]{
		obj:          obj,
		claim:        claim,
		scheme:       scheme,
		initialState: initialState,
	}
}

// Manages adds a managed resource type to the controller.
// All resource types that the controller manages must be included.
func (b *ClaimBuilder[T, U, ClaimedType, ClaimType]) Manages(
	gvks ...schema.GroupVersionKind,
) *ClaimBuilder[T, U, ClaimedType, ClaimType] {
	for _, gvk := range gvks {
		if b.scheme.Recognizes(gvk) {
			b.managedTypes = append(b.managedTypes, gvk)
		} else {
			panic(fmt.Sprintf("%s is not registered with runtime scheme", gvk))
		}
	}
	return b
}

// WithControllerHandle adds a ControllerFunc.
func (b *ClaimBuilder[T, U, ClaimedType, ClaimType]) WithControllerHandle(fn ControllerFunc) *ClaimBuilder[T, U, ClaimedType, ClaimType] {
	b.controllerFns = append(b.controllerFns, fn)
	return b
}

// WithFinalizerState adds an optional finalizer state, guaranteed to be executed after a deletion has been issued for the object
// and before the object is deleted from kubernetes.
func (b *ClaimBuilder[T, U, ClaimedType, ClaimType]) WithFinalizerState(state *types.State[ClaimedType]) *ClaimBuilder[T, U, ClaimedType, ClaimType] {
	b.finalizerState = state
	return b
}

// BeforeDelete adds a hook to perform custom actions before claimed resource is deleted.
// Claimed resource won't be deleted as long as hook returns error.
func (b *ClaimBuilder[T, U, ClaimedType, ClaimType]) BeforeDelete(
	beforeDelete internal.BeforeDelete[T, ClaimedType, U, ClaimType],
) *ClaimBuilder[T, U, ClaimedType, ClaimType] {
	b.beforeDelete = beforeDelete
	return b
}

// WithMaxConcurrentReconciles sets the maxConcurrentReconciles option for controller-runtime. Defaults to 1 if not specified or when a value <= 0 is passed.
// controller-runtime ensures a single object is not reconciled by multiple reconcilers concurrently. If your controller manages global state (e.g. caches attached to the controller struct), you need to ensure it is thread safe before increasing the concurrency.
func (b *ClaimBuilder[T, U, ClaimedType, ClaimType]) WithMaxConcurrentReconciles(maxConcurrentReconciles int) *ClaimBuilder[T, U, ClaimedType, ClaimType] {
	b.maxConcurrentReconciles = maxConcurrentReconciles
	return b
}

// Watches adds a custom watch to the controller.
func (b *ClaimBuilder[T, U, ClaimedType, ClaimType]) Watches(
	object client.Object,
	handler handler.EventHandler,
	triggerType fsmhandler.TriggerType,
	opts ...ctrlbuilder.WatchesOption,
) *ClaimBuilder[T, U, ClaimedType, ClaimType] {
	b.watches = append(b.watches, watch{
		object:      object,
		handler:     handler,
		triggerType: triggerType,
		opts:        opts,
	})
	return b
}

// WatchesRemoteKind adds a new watch to the controller for a specific kind located in a remote cluster.
// The remote cluster is specified through cache.Cache.
func (b *ClaimBuilder[T, U, ClaimedType, ClaimType]) WatchesRemoteKind(
	cache cache.Cache,
	obj client.Object,
	handler handler.EventHandler,
	triggerType fsmhandler.TriggerType,
	predicates ...predicate.Predicate,
) *ClaimBuilder[T, U, ClaimedType, ClaimType] {
	b.watchRemoteKinds = append(b.watchRemoteKinds, watchRemoteKind{
		cache:       cache,
		obj:         obj,
		handler:     handler,
		triggerType: triggerType,
		predicates:  predicates,
	})
	return b
}

// WatchesRawSource adds a new watch to the controller for events originating outside the cluster.
//
// This watch doesn't wrap the event handler with the FSM handler, so it's up to the caller to do so. You can use the
// fsmhandler.NewObservedEventHandler to wrap the handler with the FSM handler.
func (b *ClaimBuilder[T, U, ClaimedType, ClaimType]) WatchesRawSource(src source.Source) *ClaimBuilder[T, U, ClaimedType, ClaimType] {
	b.watchRawSources = append(b.watchRawSources, src)
	return b
}

// WithEventFilter adds a custom event filter to the controller.
func (b *ClaimBuilder[T, U, ClaimedType, ClaimType]) WithEventFilter(
	predicate predicate.Predicate,
) *ClaimBuilder[T, U, ClaimedType, ClaimType] {
	b.opts = append(b.opts, withEventFilter(predicate))
	return b
}

func (b *ClaimBuilder[T, U, ClaimedType, ClaimType]) Build() SetupFunc {
	return func(
		mgr ctrl.Manager,
		log *zap.SugaredLogger,
		rl workqueue.TypedRateLimiter[reconcile.Request],
		metrics *metrics.Metrics,
	) error {
		objGVK := meta.MustTypedObjectRefFromObject(b.obj, mgr.GetScheme())
		name := strcase.ToKebab(objGVK.Kind)
		log = log.Named(name)
		scheme := mgr.GetScheme()

		c := &io.ClientApplicator{
			Client:     mgr.GetClient(),
			Applicator: io.NewAPIPatchingApplicator(mgr.GetClient()),
		}

		// claim reconciler
		claimName := meta.MustGVKForObject(b.claim, scheme).Kind
		claimReconciler := internal.NewClaimReconciler(b.obj, b.claim, c, scheme, log, b.beforeDelete)
		if err := ctrl.NewControllerManagedBy(mgr).
			Named(claimName).
			WithOptions(controller.Options{
				RateLimiter:             ratelimiter.NewDefaultManagedRateLimiter(rl),
				MaxConcurrentReconciles: b.maxConcurrentReconciles,
			}).
			// equivalent to calling `builder.For` but uses an event handler that debug logs the event trigger
			For(
				b.claim,
				ctrlbuilder.WithPredicates(fsmhandler.NewForObservePredicate(log, scheme, claimName, metrics)),
			).
			Watches(
				b.obj,
				fsmhandler.NewObservedEventHandler(
					log,
					scheme,
					claimName,
					metrics,
					handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, object client.Object) []reconcile.Request {
						obj := object.(ClaimedType)
						return []reconcile.Request{{NamespacedName: obj.GetClaimRef().ObjectKey()}}
					}),
					fsmhandler.TriggerTypeRelative,
				),
			).
			Complete(&claimReconciler); err != nil {
			return fmt.Errorf("creating claim controller: %w", err)
		}

		r := internal.NewFSMReconciler(
			name,
			log,
			c,
			scheme,
			b.initialState,
			b.finalizerState,
			b.managedTypes,
			metrics,
			types.ReconcilerOptions[T, ClaimedType]{}, // TODO expose a builder method for setting ReconcilerOptions once a relevant one exists
		)

		// claimed reconciler
		claimedBuilder := ctrl.NewControllerManagedBy(mgr).
			WithOptions(controller.Options{
				RateLimiter:             ratelimiter.NewDefaultManagedRateLimiter(rl),
				MaxConcurrentReconciles: b.maxConcurrentReconciles,
			}).
			Watches(
				b.claim,
				fsmhandler.NewObservedEventHandler(
					log,
					scheme,
					name,
					metrics,
					handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, object client.Object) (reqs []reconcile.Request) {
						obj := object.(ClaimType)
						if obj.GetClaimedRef() != nil {
							reqs = append(reqs, reconcile.Request{NamespacedName: obj.GetClaimedRef().ObjectKey()})
						}
						return reqs
					}),
					fsmhandler.TriggerTypeRelative,
				),
			).
			// equivalent to calling `builder.For` but uses an event handler that debug logs the event trigger
			For(b.obj, ctrlbuilder.WithPredicates(fsmhandler.NewForObservePredicate(log, scheme, name, metrics)))

		// watch managed types
		for _, t := range b.managedTypes {
			o, err := meta.NewObjectForGVK(scheme, t)
			if err != nil {
				return fmt.Errorf("constructing new object of type %s: %s", t, err)
			}
			// equivalent to calling `builder.Owns` but uses an event handler that debug logs the event trigger
			claimedBuilder.Watches(
				o,
				fsmhandler.NewObservedEventHandler(
					log,
					scheme,
					name,
					metrics,
					handler.EnqueueRequestForOwner(scheme, mgr.GetRESTMapper(), b.obj, handler.OnlyControllerOwner()),
					fsmhandler.TriggerTypeChild,
				),
			)
		}

		// wire up custom watches to claimed
		for _, w := range b.watches {
			claimedBuilder.Watches(
				w.object,
				fsmhandler.NewObservedEventHandler(log, scheme, name, metrics, w.handler, w.triggerType),
				w.opts...,
			)
		}

		for _, w := range b.watchRemoteKinds {
			src := source.Kind(
				w.cache,
				w.obj,
				fsmhandler.NewObservedEventHandler(log, scheme, name, metrics, w.handler, w.triggerType),
				w.predicates...,
			)

			claimedBuilder.WatchesRawSource(src)
		}

		for _, w := range b.watchRawSources {
			claimedBuilder.WatchesRawSource(w)
		}

		// custom controller builder options
		for _, opt := range b.opts {
			opt(claimedBuilder)
		}

		claimedController, err := claimedBuilder.Build(r)
		if err != nil {
			return fmt.Errorf("initializing controller: %w", err)
		}

		// controller functions
		for _, fn := range b.controllerFns {
			fn(claimedController)
		}

		metrics.InitializeForGVK(objGVK.GroupVersionKind())
		metrics.InitializeForGVK(meta.MustGVKForObject(b.claim, scheme))

		return nil
	}
}

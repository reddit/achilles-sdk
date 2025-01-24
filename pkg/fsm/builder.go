package fsm

import (
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
	fsmtypes "github.com/reddit/achilles-sdk/pkg/fsm/types"
	"github.com/reddit/achilles-sdk/pkg/io"
	"github.com/reddit/achilles-sdk/pkg/meta"
	"github.com/reddit/achilles-sdk/pkg/ratelimiter"
)

// SetupFunc wires up the provided reconciler with the controller-runtime manager and other common dependencies.
type SetupFunc func(
	ctrl.Manager, // controller-runtime manager
	*zap.SugaredLogger, // logger
	workqueue.TypedRateLimiter[reconcile.Request], // reconciler rate limiter
	*metrics.Metrics, // metrics sink
) error

// ControllerFunc is a function with a handle to a controller.Controller.
// Typically used in cases where watches need to be initiated dynamically at run time.
type ControllerFunc func(controller.Controller)

// buildOption is a parameter when constructing a controller
type buildOption func(builder *ctrlbuilder.Builder)

func withEventFilter(predicate predicate.Predicate) buildOption {
	return func(builder *ctrlbuilder.Builder) {
		builder.WithEventFilter(predicate)
	}
}

// Builder is a builder for an FSM controller.
type Builder[T any, Obj apitypes.FSMResource[T]] struct {
	obj                     Obj
	scheme                  *runtime.Scheme
	initialState            *fsmtypes.State[Obj]
	finalizerState          *fsmtypes.State[Obj]
	managedTypes            []schema.GroupVersionKind
	controllerFns           []ControllerFunc
	watches                 []watch
	watchRemoteKinds        []watchRemoteKind
	watchSources            []source.Source
	opts                    []buildOption
	maxConcurrentReconciles int
	reconcilerOptions       fsmtypes.ReconcilerOptions[T, Obj]
}

type watch struct {
	object      client.Object
	handler     handler.EventHandler
	opts        []ctrlbuilder.WatchesOption
	triggerType fsmhandler.TriggerType
}

type watchRemoteKind struct {
	cache       cache.Cache
	obj         client.Object
	handler     handler.EventHandler
	predicates  []predicate.Predicate
	triggerType fsmhandler.TriggerType
}

// NewBuilder returns a builder that builds a function wiring up a logical FSM controller to a manager.
// Obj is the object being reconciled and initialState is the initial state in the finite state machine,
func NewBuilder[T any, Obj apitypes.FSMResource[T]](
	obj Obj,
	initialState *fsmtypes.State[Obj],
	scheme *runtime.Scheme,
) *Builder[T, Obj] {
	return &Builder[T, Obj]{
		obj:          obj,
		scheme:       scheme,
		initialState: initialState,
	}
}

// Manages adds a managed resource type to the controller.
// All resource types that the controller manages must be included.
func (b *Builder[T, Obj]) Manages(
	gvks ...schema.GroupVersionKind,
) *Builder[T, Obj] {
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
func (b *Builder[T, Obj]) WithControllerHandle(fn ControllerFunc) *Builder[T, Obj] {
	b.controllerFns = append(b.controllerFns, fn)
	return b
}

// WithFinalizerState adds an optional finalizer state, guaranteed to be executed after a deletion has been issued for the object
// and before the object is deleted from kubernetes.
func (b *Builder[T, Obj]) WithFinalizerState(state *fsmtypes.State[Obj]) *Builder[T, Obj] {
	b.finalizerState = state
	return b
}

// WithReconcilerOptions sets reconciler options.
func (b *Builder[T, Obj]) WithReconcilerOptions(reconcilerOptions fsmtypes.ReconcilerOptions[T, Obj]) *Builder[T, Obj] {
	b.reconcilerOptions = reconcilerOptions
	return b
}

// WithMaxConcurrentReconciles sets the maxConcurrentReconciles option for controller-runtime. Defaults to 1 if not specified or when a value <= 0 is passed.
// controller-runtime ensures a single object is not reconciled by multiple reconcilers concurrently. If your controller manages global state (e.g. caches attached to the controller struct), you need to ensure it is thread safe before increasing the concurrency.
func (b *Builder[T, Obj]) WithMaxConcurrentReconciles(maxConcurrentReconciles int) *Builder[T, Obj] {
	b.maxConcurrentReconciles = maxConcurrentReconciles
	return b
}

// Watches adds a custom watch to the controller.
func (b *Builder[T, Obj]) Watches(
	object client.Object,
	handler handler.EventHandler,
	triggerType fsmhandler.TriggerType,
	opts ...ctrlbuilder.WatchesOption,
) *Builder[T, Obj] {
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
func (b *Builder[T, Obj]) WatchesRemoteKind(
	cache cache.Cache,
	obj client.Object,
	handler handler.EventHandler,
	triggerType fsmhandler.TriggerType,
	predicates ...predicate.Predicate,
) *Builder[T, Obj] {
	b.watchRemoteKinds = append(b.watchRemoteKinds, watchRemoteKind{
		cache:       cache,
		obj:         obj,
		handler:     handler,
		triggerType: triggerType,
		predicates:  predicates,
	})
	return b
}

// WatchesSource adds a new watch to the controller for events originating outside the cluster.
func (b *Builder[T, Obj]) WatchesSource(src source.Source) *Builder[T, Obj] {
	b.watchSources = append(b.watchSources, src)
	return b
}

// WithEventFilter adds a custom event filter to the controller.
func (b *Builder[T, Obj]) WithEventFilter(
	predicate predicate.Predicate,
) *Builder[T, Obj] {
	b.opts = append(b.opts, withEventFilter(predicate))
	return b
}

func (b *Builder[T, Obj]) Build() SetupFunc {
	return func(
		mgr ctrl.Manager,
		log *zap.SugaredLogger,
		rl workqueue.TypedRateLimiter[reconcile.Request],
		metrics *metrics.Metrics,
	) error {
		scheme := mgr.GetScheme()
		objGVK := meta.MustTypedObjectRefFromObject(b.obj, scheme)
		name := strcase.ToKebab(objGVK.Kind)
		log = log.Named(name)

		c := &io.ClientApplicator{
			Client:     mgr.GetClient(),
			Applicator: io.NewAPIPatchingApplicator(mgr.GetClient()),
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
			b.reconcilerOptions,
		)

		builder := ctrl.NewControllerManagedBy(mgr).
			WithOptions(controller.Options{
				RateLimiter:             ratelimiter.NewDefaultManagedRateLimiter(rl),
				MaxConcurrentReconciles: b.maxConcurrentReconciles,
			}).
			// equivalent to calling `builder.For` but uses an event handler that debug logs the event trigger
			For(b.obj, ctrlbuilder.WithPredicates(fsmhandler.NewForObservePredicate(log, scheme, name, metrics)))

		// watch managed types
		for _, t := range b.managedTypes {
			o, err := meta.NewObjectForGVK(scheme, t)
			if err != nil {
				return fmt.Errorf("constructing new object of type %s: %s", t, err)
			}
			// equivalent to calling `builder.Owns` but uses an event handler that debug logs the event trigger
			builder.Watches(
				o,
				fsmhandler.NewObservedEventHandler(log, scheme, name, metrics, handler.EnqueueRequestForOwner(scheme, mgr.GetRESTMapper(), b.obj, handler.OnlyControllerOwner()), fsmhandler.TriggerTypeChild),
			)
		}

		// wire up custom watches
		for _, w := range b.watches {
			builder.Watches(
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

			builder.WatchesRawSource(src)
		}

		for _, w := range b.watchSources {
			builder.WatchesRawSource(w)
		}

		// custom controller builder options
		for _, opt := range b.opts {
			opt(builder)
		}

		con, err := builder.Build(r)
		if err != nil {
			return fmt.Errorf("initializing controller: %w", err)
		}

		// controller functions
		for _, fn := range b.controllerFns {
			fn(con)
		}

		return nil
	}
}

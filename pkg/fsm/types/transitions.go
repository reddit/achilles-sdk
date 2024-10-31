package types

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"
	coordination "k8s.io/api/coordination/v1"
	core "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/reddit/achilles-sdk-api/api"
	apitypes "github.com/reddit/achilles-sdk-api/pkg/types"
	"github.com/reddit/achilles-sdk/pkg/io"
	"github.com/reddit/achilles-sdk/pkg/meta"
	"github.com/reddit/achilles-sdk/pkg/sets"
	"github.com/reddit/achilles-sdk/pkg/status"
)

type ResourceManagerObject interface {
	client.Object            // must be a k8s resource
	apitypes.ResourceManager // must manage a set of child resources
}

type getUnreadyResourcesOptions struct {
	// customReadyFuncs is a list of custom resource readiness checks.
	customReadyFuncs []customResourceReadyFunc
}

// customResourceReadyFunc is a tuple of a resource type and a function that determines if the resource is ready.
type customResourceReadyFunc struct {
	Type      reflect.Type
	ReadyFunc func(o any) (ready, matched bool)
}

// GetUnreadyResourcesOption adds optional semantics to GetUnreadyResources.
type GetUnreadyResourcesOption func(*getUnreadyResourcesOptions)

// WithCustomReadyFuncs adds custom resource readiness checks to GetUnreadyResources.
func WithCustomReadyFuncs(customReadyFuncs ...customResourceReadyFunc) GetUnreadyResourcesOption {
	return func(o *getUnreadyResourcesOptions) {
		o.customReadyFuncs = append(o.customReadyFuncs, customReadyFuncs...)
	}
}

// MakeCustomReadyFunc creates a customResourceReadyFunc from a function that determines if a resource is ready.
func MakeCustomReadyFunc[T any](readyFunc func(T) bool) customResourceReadyFunc {
	return customResourceReadyFunc{
		Type: reflect.TypeFor[T](),
		ReadyFunc: func(o any) (ready, matched bool) {
			// if the object is of the expected type, call the readyFunc
			if t, ok := o.(T); ok {
				return readyFunc(t), true
			}
			return false, false
		},
	}
}

// GetUnreadyResources returns a list of child resources managed by obj that are not marked as ready,
// determined by reading the state of the child resources from the kube-apiserver.
// This function understands readiness of Achilles CRDs, and can be extended with
// custom resource readiness checks by providing a list of customResourceReadyFunc.
//
// Custom resource checks are performed in the order they are provided, with the first matching readiness
// function that returns ready determining the readiness of the resource.
// The resource is considered unready if and only if no custom readiness function matches and returns true.
func GetUnreadyResources(
	ctx context.Context,
	c client.Client,
	scheme *runtime.Scheme,
	log *zap.SugaredLogger,
	obj ResourceManagerObject,
	options ...GetUnreadyResourcesOption,
) ([]client.Object, error) {
	opts := &getUnreadyResourcesOptions{}
	for _, o := range options {
		o(opts)
	}

	unreadyResources := []client.Object{}

	managedResources, err := readManagedResources(ctx, c, scheme, obj)
	if err != nil {
		return nil, fmt.Errorf("reading managed resources: %w", err)
	}

	for _, o := range managedResources {
		switch res := o.(type) {
		case api.Conditioned: // achilles resources
			if !status.ResourceReady(res) {
				unreadyResources = append(unreadyResources, o)
			}
		case *coordination.Lease, *core.ConfigMap:
			// These resources don't have status and they are ready as soon as created.
			continue
		default:
			var foundReadyFunc, ready bool
			for _, customReadyFunc := range opts.customReadyFuncs {
				var matched bool
				ready, matched = customReadyFunc.ReadyFunc(res)
				if matched {
					foundReadyFunc = true
					if ready {
						break // first match wins
					}
				}
			}
			if !ready {
				unreadyResources = append(unreadyResources, o)
			}
			if !foundReadyFunc {
				log.Warnf("Recource %T doesn't have readiness flag so it won't be ever considered ready", res)
			}
		}
	}

	return unreadyResources, nil
}

type TransitionWhenReadyOption func(*transitionWhenReadyOpts)

type transitionWhenReadyOpts struct {
	// requeueAfter is the duration to wait before requeueing the reconcile loop. Defaults to 10 seconds.
	requeueAfter time.Duration

	// resources is a list of resources to check for readiness. If empty, all child resources of the parent object are checked.
	resources []client.Object

	getUnreadyResourcesFn func(
		ctx context.Context,
		c client.Client,
		scheme *runtime.Scheme,
		log *zap.SugaredLogger,
		obj ResourceManagerObject,
		options ...GetUnreadyResourcesOption,
	) ([]client.Object, error)
}

// WithRequeueAfter sets the requeue duration for TransitionWhenReady. If not set, the default is 10 seconds.
func WithRequeueAfter(requeueAfter time.Duration) func(*transitionWhenReadyOpts) {
	return func(o *transitionWhenReadyOpts) {
		o.requeueAfter = requeueAfter
	}
}

// WithResources sets the resources to check for readiness in TransitionWhenReady. If not set, all child resources of the parent object are checked.
func WithResources(resources ...client.Object) func(*transitionWhenReadyOpts) {
	return func(o *transitionWhenReadyOpts) {
		o.resources = resources
	}
}

// WithGetUnreadyResourcesFn sets the function to use for getting unready resources in TransitionWhenReady.
// If not set, GetUnreadyResources is used.
func WithGetUnreadyResourcesFn(fn func(
	ctx context.Context,
	c client.Client,
	scheme *runtime.Scheme,
	log *zap.SugaredLogger,
	obj ResourceManagerObject,
	options ...GetUnreadyResourcesOption,
) ([]client.Object, error)) func(*transitionWhenReadyOpts) {
	return func(o *transitionWhenReadyOpts) {
		o.getUnreadyResourcesFn = fn
	}
}

// TransitionWhenReady is a state transition function that returns the next state if all specified resources are marked Ready.
// If no resources are specified, the function will check all child resources of the parent object.
// Crossplane resources must have both Ready and Synced conditions set to True.
// If any in-scope resources are not ready, requeues reconcile loop in 10 seconds.
func TransitionWhenReady[T ResourceManagerObject](
	c client.Client,
	scheme *runtime.Scheme,
	log *zap.SugaredLogger,
	next *State[T],
	options ...TransitionWhenReadyOption,
) TransitionFunc[T] {
	opts := &transitionWhenReadyOpts{
		requeueAfter:          10 * time.Second,
		getUnreadyResourcesFn: GetUnreadyResources,
	}
	for _, o := range options {
		o(opts)
	}

	return func(
		ctx context.Context,
		obj T,
		out *OutputSet,
	) (*State[T], Result) {
		unreadyResources, err := opts.getUnreadyResourcesFn(ctx, c, scheme, log, obj)
		if err != nil {
			return nil, ErrorResult(err)
		}

		var applicableUnreadyResources []client.Object
		// if no resources are specified, check all child objects of the parent
		if len(opts.resources) == 0 {
			applicableUnreadyResources = unreadyResources
		} else {
			// otherwise check only the specified objects
			unreadyResourcesSet := sets.NewObjectSet(scheme, unreadyResources...)
			desiredResourceSet := sets.NewObjectSet(scheme, opts.resources...)
			applicableUnreadyResources = unreadyResourcesSet.Intersection(desiredResourceSet).List()
		}

		var unreadyNames []string

		// sort applicableUnreadyResources to ensure unreadyNames is stable, and therefore we don't generate
		// spurious mutations of the status.
		sort.SliceStable(applicableUnreadyResources, func(i, j int) bool {
			return applicableUnreadyResources[i].GetName() < applicableUnreadyResources[j].GetName()
		})

		for _, o := range applicableUnreadyResources {
			tof, err := meta.TypedObjectRefFromObject(o, scheme)
			if err != nil {
				log.Warnf("Unable to get typed object ref for %T %s: %v", o, o.GetName(), err)
				continue
			}

			// The length of 3 chosen arbitrarily to keep the message reasonably brief while still providing some info
			if len(unreadyNames) < 3 {
				unreadyNames = append(unreadyNames, tof.String())
			}

			log.Debugf("managed resource %s is not ready", tof)
		}
		if len(applicableUnreadyResources) == 0 {
			return next, DoneResult()
		}

		msg := fmt.Sprintf("some managed resources are not ready. First three:\n%s",
			strings.Join(unreadyNames, ",\n"))
		return nil, RequeueResult(msg, opts.requeueAfter)
	}
}

// DeleteChildrenForeground is a generic state that implements foreground cascading deletion
// of children resources (i.e. resources managed by the parent resource).
//
// The state will requeue until all child resources are fully deleted. Any child resources
// that remain in terminating state but not deleted will cause the state to requeue.
//
// This state is useful for preventing accidental orphaning of child resources. The parent
// resource's existence serves as a signal that underlying child resources have yet to be cleaned up.
//
// See the Kubernetes documentation for more information on foreground cascading deletion:
// https://kubernetes.io/docs/concepts/architecture/garbage-collection/#foreground-deletion/
func DeleteChildrenForeground[T ResourceManagerObject](
	c *io.ClientApplicator,
	scheme *runtime.Scheme,
	log *zap.SugaredLogger,
	next *State[T],
) TransitionFunc[T] {
	return func(
		ctx context.Context,
		parent T,
		out *OutputSet,
	) (*State[T], Result) {
		children, err := readManagedResources(ctx, c, scheme, parent)
		if err != nil {
			return nil, ErrorResultf("reading managed resources: %w", err)
		}

		// construct message hint for extant children
		var extantChildRefStrings []string
		var extantChildRefs []api.TypedObjectRef

		for _, child := range children {
			tof, err := meta.TypedObjectRefFromObject(child, scheme)
			if err != nil {
				log.Warnf("getting typed object ref for %T %s: %v", child, child.GetName(), err)
			} else {
				extantChildRefs = append(extantChildRefs, *tof)

				// only show the first three child refs in the hint
				if len(extantChildRefStrings) < 4 {
					extantChildRefStrings = append(extantChildRefStrings, tof.String())
				}
			}

			// delete child
			if err := c.Delete(ctx, child); client.IgnoreNotFound(err) != nil {
				return nil, ErrorResultf("deleting managed resource %s: %w", tof.String(), err)
			}
		}

		// update resource refs
		parent.SetManagedResources(extantChildRefs)
		if err := c.ApplyStatus(ctx, parent); err != nil {
			return nil, ErrorResultf("updating parent status' managed resource refs: %w", err)
		}

		if len(children) > 0 {
			msg := fmt.Sprintf("waiting for child resources to be deleted, first three:\n%s", strings.Join(extantChildRefStrings, ",\n"))
			return nil, RequeueResultWithReasonAndBackoff(msg, "WaitingForChildDeletion")
		}

		return next, DoneResult()
	}
}

// readManagedResources reads and returns all managed resources of the specified parent.
// Managed resources that are not found are ignored.
func readManagedResources(
	ctx context.Context,
	c client.Client,
	scheme *runtime.Scheme,
	parent ResourceManagerObject,
) ([]client.Object, error) {
	var managedResources []client.Object

	for _, res := range parent.GetManagedResources() {
		res := res // pike
		managedObj, err := meta.NewObjectForGVK(scheme, res.GroupVersionKind())
		if err != nil {
			return nil, fmt.Errorf("constructing new %T %s: %w", managedObj, client.ObjectKeyFromObject(managedObj), err)
		}

		if err := c.Get(ctx, res.ObjectKey(), managedObj); err != nil {
			if k8serrors.IsNotFound(err) {
				// ignore not found and continue
				continue
			}
			return nil, fmt.Errorf("getting managed resource %T %s: %w", managedObj, client.ObjectKeyFromObject(managedObj), err)
		} else {
			managedResources = append(managedResources, managedObj)
		}
	}
	return managedResources, nil
}

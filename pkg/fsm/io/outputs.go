package io

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/reddit/achilles-sdk-api/api"
	apitypes "github.com/reddit/achilles-sdk-api/pkg/types"
	"github.com/reddit/achilles-sdk/pkg/fsm/types"
	"github.com/reddit/achilles-sdk/pkg/io"
	"github.com/reddit/achilles-sdk/pkg/meta"
)

// ApplyOutputSet ensures that all objects declared in the OutputSet are applied,
// ensuring extant outputs and deleting outputs that are no longer needed.
// Metadata tracking extant outputs are persisted onto the specified object's status.
func ApplyOutputSet[T any, Obj apitypes.FSMResource[T]](
	ctx context.Context,
	log *zap.SugaredLogger,
	c *io.ClientApplicator,
	scheme *runtime.Scheme,
	obj Obj,
	out *types.OutputSet,
) error {
	// delete resources
	for _, o := range out.ListDeleted() {
		if err := c.Delete(ctx, o); err != nil && !k8serrors.IsNotFound(err) {
			return fmt.Errorf("deleting object %T %s: %w", o, client.ObjectKeyFromObject(o), err)
		}
	}

	// ensure output resources
	if err := ensureOutputs(ctx, c, scheme, obj, out.ListAppliedOutputs()); err != nil {
		return fmt.Errorf("ensuring outputs: %w", err)
	}

	// apply managed resource refs to status
	// NOTE: do this after ensuring the manage resource objects to prevent adding a managed resource ref for an
	// object that wasn't created successfully
	if err := applyManagedResourceRefs(ctx, log, c, scheme, obj, out); err != nil {
		return fmt.Errorf("applying managed resource refs: %w", err)
	}

	return nil
}

func applyManagedResourceRefs[T any, Obj apitypes.FSMResource[T]](
	ctx context.Context,
	log *zap.SugaredLogger,
	c *io.ClientApplicator,
	scheme *runtime.Scheme,
	obj Obj,
	outputSet *types.OutputSet,
) error {
	// initialize an empty object so we only update the status' resource refs
	copy := Obj(new(T))
	copy.SetName(obj.GetName())
	copy.SetNamespace(obj.GetNamespace())

	newRefs := outputSet.GetApplied().DeepCopy()
	deleted := outputSet.GetDeleted()

	// accumulate managed resource refs across all states by starting with the status' managed resources,
	// and deleting explicitly deleted objects and inserting any new objects (while deduplicating)
	refs := []api.TypedObjectRef{} // explicitly signal deletion if there are no managed resources
	for _, ref := range obj.GetManagedResources() {
		// verify that managed object exists, emit warning if not
		managedObj, err := meta.NewObjectForGVK(scheme, ref.GroupVersionKind())
		if err != nil {
			return fmt.Errorf("constructing new %T %s: %w", managedObj, client.ObjectKeyFromObject(managedObj), err)
		}
		managedObj.SetName(ref.Name)
		managedObj.SetNamespace(ref.Namespace)

		if err := c.Get(ctx, client.ObjectKeyFromObject(managedObj), managedObj); err != nil {
			if k8serrors.IsNotFound(err) {
				// warn for managed resource that wasn't explicitly deleted by the controller, but is deleted on the kube-apiserver
				// this shouldn't happen unless an external actor tampers with the state by deleting the object
				if deleted.GetByRef(ref) == nil {
					log.Warnf(
						"managed resource %s of type %T not found, an external actor may have deleted it",
						client.ObjectKeyFromObject(managedObj),
						managedObj,
					)
				}
				continue // remove refs for deleted objects
			} else {
				return fmt.Errorf("getting managed resource: %w", err)
			}
		}

		// remove ref from newly ensured objects (to prevent duplicate refs for objects that are applied in multiple states)
		newRefs.DeleteByRef(ref)

		refs = append(refs, ref)
	}

	for _, newRef := range newRefs.List() {
		refs = append(refs, *meta.MustTypedObjectRefFromObject(newRef, scheme))
	}
	copy.SetManagedResources(refs)

	if err := c.ApplyStatus(ctx, copy); err != nil {
		return fmt.Errorf("applying status resourceRefs: %w", err)
	}

	// update in-memory obj
	obj.SetManagedResources(refs)
	return nil
}

func ensureOutputs[T any, Obj apitypes.FSMResource[T]](
	ctx context.Context,
	c *io.ClientApplicator,
	scheme *runtime.Scheme,
	obj Obj,
	outputs []types.OutputObject,
) error {
	for _, output := range outputs {
		res := output.Object

		// we want the controller to be able to update resources while its deleting so we
		// can perform state cleanup, but we never want it to create a new resource.
		// TODO(eac): unify these code paths somehow
		if meta.WasDeleted(obj) {
			base, err := meta.NewObjectForGVK(scheme, meta.MustGVKForObject(res, scheme))
			if err != nil {
				return fmt.Errorf("constructing new %T: %w", res, err)
			}

			if err := c.Get(ctx, client.ObjectKeyFromObject(res), base); err != nil && !k8serrors.IsNotFound(err) {
				return fmt.Errorf("getting resource to check for deletion: %w", err)
			}

			if meta.WasCreated(base) {
				// ignore optimistic resource lock
				res.SetResourceVersion("")
				// we never want to create a new object here, so we Patch explicitly
				if err := c.Patch(ctx, res, client.Merge); err != nil {
					return fmt.Errorf("updating resource %q: %w", res.GetName(), err)
				}
			}
		} else {
			// NOTE: add the default WithControllerRef last so it's not invoked if WithoutOwnerRefs is set
			applyOpts := append(output.ApplyOpts, io.WithControllerRef(obj, scheme))

			if err := c.Apply(ctx, res, applyOpts...); err != nil {
				return fmt.Errorf("ensuring %s %s: %w", res.GetObjectKind().GroupVersionKind(), res.GetName(), err)
			}
		}
	}

	return nil
}

package meta

import (
	"context"
	"fmt"

	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// AddFinalizer patches an object by adding the given finalizer key.
// Conflicts with concurrent writers are retried with a bounded backoff.
func AddFinalizer(
	ctx context.Context,
	c client.Client,
	obj client.Object,
	finalizerKey string,
) error {
	return patchFinalizers(ctx, c, obj, "addition", func(o client.Object) bool {
		return controllerutil.AddFinalizer(o, finalizerKey)
	})
}

// RemoveFinalizer patches an object by removing the given finalizer key.
// Conflicts with concurrent writers are retried with a bounded backoff.
// A NotFound error is treated as success: the object is gone, so the finalizer no longer blocks anything.
func RemoveFinalizer(
	ctx context.Context,
	c client.Client,
	obj client.Object,
	finalizerKey string,
) error {
	err := patchFinalizers(ctx, c, obj, "removal", func(o client.Object) bool {
		return controllerutil.RemoveFinalizer(o, finalizerKey)
	})
	return client.IgnoreNotFound(err)
}

// patchFinalizers fetches obj, applies mutate, and merge-patches the result back.
//
// The patch carries an optimistic lock. A JSON merge patch replaces metadata.finalizers wholesale, so a patch
// computed from a stale read would reinstate or drop finalizers owned by other actors (e.g. the garbage collector's
// foregroundDeletion). Locking on resourceVersion turns that race into a Conflict, which is retried against a fresh read.
//
// mutate reports whether it changed obj; when it didn't, no write is issued.
func patchFinalizers(
	ctx context.Context,
	c client.Client,
	obj client.Object,
	verb string,
	mutate func(client.Object) bool,
) error {
	objKey := client.ObjectKeyFromObject(obj)
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		if err := c.Get(ctx, objKey, obj); err != nil {
			return fmt.Errorf("fetching object %q before finalizer %s: %w", objKey, verb, err)
		}

		base := client.MergeFromWithOptions(obj.DeepCopyObject().(client.Object), client.MergeFromWithOptimisticLock{})
		if !mutate(obj) {
			return nil
		}
		if err := c.Patch(ctx, obj, base); err != nil {
			return fmt.Errorf("patching object %q with finalizer %s: %w", objKey, verb, err)
		}
		return nil
	})
}

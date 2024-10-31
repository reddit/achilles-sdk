package meta

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// AddFinalizer patches an object by adding the given finalizer key.
func AddFinalizer(
	ctx context.Context,
	c client.Client,
	obj client.Object,
	finalizerKey string,
) error {
	// first fetch the object to ensure that it's up to date
	objKey := client.ObjectKeyFromObject(obj)
	if err := c.Get(ctx, objKey, obj); err != nil {
		return fmt.Errorf("fetching object %q before updating with finalizer: %w", objKey, err)
	}

	base := client.MergeFrom(obj.DeepCopyObject().(client.Object))
	// patch object with appended finalizer key
	controllerutil.AddFinalizer(obj, finalizerKey)
	if err := c.Patch(ctx, obj, base); err != nil {
		return fmt.Errorf("patching object %q with finalizer addition: %w", objKey, err)
	}
	return nil
}

// RemoveFinalizer patches an object by removing the given finalizer key.
func RemoveFinalizer(
	ctx context.Context,
	c client.Client,
	obj client.Object,
	finalizerKey string,
) error {
	// first fetch the object to ensure that it's up to date
	objKey := client.ObjectKeyFromObject(obj)
	if err := c.Get(ctx, objKey, obj); err != nil {
		return fmt.Errorf("fetching object %q before remove finalizer: %w", objKey, err)
	}

	base := client.MergeFrom(obj.DeepCopyObject().(client.Object))
	// patch object with appended finalizer key
	controllerutil.RemoveFinalizer(obj, finalizerKey)
	if err := c.Patch(ctx, obj, base); err != nil {
		return fmt.Errorf("patching object %q with finalizer removal: %w", objKey, err)
	}

	return nil
}

package io

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/reddit/achilles-sdk/pkg/meta"
)

// requestOption builds an ApplyOption whose hook runs only during configureRequestOptions.
// Use it for flags that affect how Apply/ApplyStatus talk to the API (patch vs update, SSA,
// optimistic lock) and must be known before Get or create, without mutating the desired object.
func requestOption(fn func(*RequestOptions) error) ApplyOption {
	return ApplyOption{configureRequest: fn}
}

// objectOption builds an ApplyOption whose hook runs only during applyObjectOptions.
// Use it to mutate the desired object (labels, owner references) once request options are
// resolved and the applicator has a concrete object to send (create, patch, or SSA).
func objectOption(fn func(context.Context, client.Object, *RequestOptions) error) ApplyOption {
	return ApplyOption{configureObject: fn}
}

// WithRedditLabels applies a standard set of labels for managed resources.
func WithRedditLabels(controllerName string) ApplyOption {
	return objectOption(func(_ context.Context, o client.Object, _ *RequestOptions) error {
		meta.SetRedditLabels(o, controllerName)
		return nil
	})
}

// WithControllerRef sets an owner reference on the object and controller flag to true.
// When used in the context of OutputSet, this option is used by default unless WithoutOwnerRef is specified.
func WithControllerRef(owner client.Object, scheme *runtime.Scheme) ApplyOption {
	return objectOption(func(_ context.Context, o client.Object, opts *RequestOptions) error {
		// skip if WithoutOwnerRefs is set or if the caller explicitly specifies ownerReferences
		if opts.WithoutOwnerRefs || opts.hasExplicitOwnerRefs {
			return nil
		}
		return meta.SetControllerRef(o, owner, scheme)
	})
}

// WithOwnerRef sets an owner reference on the object and controller flag to false.
// Multiple owner references can be set on an object if their controller flag is false.
func WithOwnerRef(owner client.Object, scheme *runtime.Scheme) ApplyOption {
	return ApplyOption{
		configureRequest: func(requestOpts *RequestOptions) error {
			if requestOpts.WithoutOwnerRefs {
				return nil
			}
			requestOpts.hasExplicitOwnerRefs = true // prevent FSM reconciler from adding the default controller reference
			return nil
		},
		configureObject: func(_ context.Context, o client.Object, requestOpts *RequestOptions) error {
			if requestOpts.WithoutOwnerRefs {
				return nil
			}
			return meta.SetOwnerRef(o, owner, scheme)
		},
	}
}

// WithoutOwnerRefs explicitly prevents owner refs (either controller or owner) from being set on the applied object.
// Generally this should not be used—only set it if your controller is intentionally managing owner references on
// managed resources.
func WithoutOwnerRefs() ApplyOption {
	return requestOption(func(requestOpts *RequestOptions) error {
		requestOpts.WithoutOwnerRefs = true
		return nil
	})
}

// WithOptimisticLock requires the desired object to include a resource version when patching
// or updating an existing object. The check is deferred until apply time so creates are not blocked.
func WithOptimisticLock() ApplyOption {
	return requestOption(func(requestOpts *RequestOptions) error {
		requestOpts.EnforceOptimisticLock = true
		return nil
	})
}

// AsUpdate uses an update request to overwrite the entire object if it exists, rather than selective patching.
// Using this option without the optimistic lock implies a full overwrite of the object, so use with caution.
func AsUpdate() ApplyOption {
	return requestOption(func(requestOpts *RequestOptions) error {
		requestOpts.Update = true
		return nil
	})
}

// AsServerSideApply causes Apply (and ApplyStatus) to use server-side apply (PATCH with ApplyPatchType)
// instead of the default JSON merge patch. fieldManager is the name recorded in managedFields to track
// which fields this caller owns. Use this when multiple controllers manage different fields of the same
// resource — the apiserver will enforce that two managers cannot own the same field without an explicit
// conflict resolution.
//
// Cannot be combined with AsUpdate. May be combined with WithForceOwnership to claim fields
// currently owned by another manager.
//
// Important caveats:
//   - Server-side apply skips the local Get + diff loop; idempotency is handled by the apiserver.
//   - Fields with the `omitempty` JSON tag whose Go value is the zero value (e.g. 0, "", false) will be
//     omitted from the JSON body and will NOT be set to zero — they will instead release ownership.
//     Use pointer types (*int, *string, etc.) for fields you need to explicitly zero via SSA.
func AsServerSideApply(fieldManager string) ApplyOption {
	return requestOption(func(requestOpts *RequestOptions) error {
		if fieldManager == "" {
			return fmt.Errorf("AsServerSideApply requires a non-empty fieldManager name")
		}
		requestOpts.ServerSideApply = true
		requestOpts.FieldManager = fieldManager
		return nil
	})
}

// WithForceOwnership, when used with AsServerSideApply, sends ?force=true to claim ownership of fields
// currently owned by another field manager, resolving any SSA conflicts. Without this option, the
// apiserver returns HTTP 409 if another manager owns a field present in the apply body.
//
// This is typically needed during a one-time migration from client-side apply/patch to server-side apply,
// to reclaim ownership of fields that were previously written by a different manager name.
//
// No-op when AsServerSideApply is not also set.
func WithForceOwnership() ApplyOption {
	return requestOption(func(requestOpts *RequestOptions) error {
		requestOpts.ForceOwnership = true
		return nil
	})
}

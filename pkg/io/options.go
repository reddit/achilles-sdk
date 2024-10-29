package io

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/reddit/achilles-sdk/pkg/meta"
)

// WithRedditLabels applies a standard set of labels for managed resources.
func WithRedditLabels(controllerName string) ApplyOption {
	return func(ctx context.Context, o client.Object, _ *RequestOptions) error {
		meta.SetRedditLabels(o, controllerName)
		return nil
	}
}

// WithControllerRef sets an owner reference on the object and controller flag to true.
// When used in the context of OutputSet, this option is used by default unless WithoutOwnerRef is specified.
func WithControllerRef(owner client.Object, scheme *runtime.Scheme) ApplyOption {
	return func(ctx context.Context, o client.Object, opts *RequestOptions) error {
		// skip if WithoutOwnerRefs is set or if the caller explicitly specifies ownerReferences
		if opts.WithoutOwnerRefs || opts.hasExplicitOwnerRefs {
			return nil
		}
		return meta.SetControllerRef(o, owner, scheme)
	}
}

// WithOwnerRef sets an owner reference on the object and controller flag to false.
// Multiple owner references can be set on an object if their controller flag is false.
func WithOwnerRef(owner client.Object, scheme *runtime.Scheme) ApplyOption {
	return func(ctx context.Context, o client.Object, opts *RequestOptions) error {
		// skip if WithoutOwnerRefs is set
		if opts.WithoutOwnerRefs {
			return nil
		}
		opts.hasExplicitOwnerRefs = true // prevent FSM reconciler from adding the default controller reference
		return meta.SetOwnerRef(o, owner, scheme)
	}
}

// WithoutOwnerRefs explicitly prevents owner refs (either controller or owner) from being set on the applied object.
// Generally this should not be used—only set it if your controller is intentionally managing owner references on
// managed resources.
func WithoutOwnerRefs() ApplyOption {
	return func(ctx context.Context, o client.Object, requestOpts *RequestOptions) error {
		requestOpts.WithoutOwnerRefs = true
		return nil
	}
}

// WithOptimisticLock returns an error if the desired object is missing the resource version
func WithOptimisticLock() ApplyOption {
	return func(ctx context.Context, o client.Object, opts *RequestOptions) error {
		if o.GetResourceVersion() == "" {
			return ResourceVersionMissing{}
		}
		opts.EnforceOptimisticLock = true
		return nil
	}
}

// AsUpdate uses an update request to overwrite the entire object if it exists, rather than selective patching.
// Using this option without the optimistic lock implies a full overwrite of the object, so use with caution.
func AsUpdate() ApplyOption {
	return func(ctx context.Context, _ client.Object, requestOpts *RequestOptions) error {
		requestOpts.Update = true
		return nil
	}
}

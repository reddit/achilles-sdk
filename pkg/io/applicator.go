package io

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/reddit/achilles-sdk/pkg/encoding/json"
	liberrors "github.com/reddit/achilles-sdk/pkg/errors"
)

// A ClientApplicator may be used to build a single 'client' that satisfies both
// client.Client and Applicator.
type ClientApplicator struct {
	client.Client
	Applicator
}

// An Applicator applies changes to an object.
type Applicator interface {
	Apply(context.Context, client.Object, ...ApplyOption) error
	ApplyStatus(context.Context, client.Object, ...ApplyOption) error
}

// An ApplyOption mutates the desired object before applying
type ApplyOption func(ctx context.Context, o client.Object, requestOpts *RequestOptions) error

// options for the kube-apiserver request
type RequestOptions struct {
	// Update, if true, overrides the entire object with an update request (instead of selective patch)
	Update bool

	// EnforceOptimisticLock, if true, enforces usage of the optimistic resource lock by erroring
	// if the object to be updated doesn't include `meta.resourceVersion`
	EnforceOptimisticLock bool

	// WithoutOwnerRefs, if true, prevents any owner refs from being set on the applied object.
	// Generally this should not be used—only set it if your controller is intentionally managing owner references on
	// managed resources.
	WithoutOwnerRefs bool

	// hasExplicitOwnerRefs is true if the caller explicitly sets ownerReferences
	// This flag, if true, prevents the FSM reconciler from adding the default controller reference.
	hasExplicitOwnerRefs bool
}

// An APIPatchingApplicator applies changes to an object by either creating or
// patching it in a Kubernetes API server.
// For a detailed discussion of the reasoning behind these semantics, see this doc,
// https://github.com/reddit/achilles-sdk/blob/main/docs/sdk-apply-objects.md.
type APIApplicator struct {
	client client.Client
}

// NewAPIPatchingApplicator returns an Applicator that applies changes to an
// object by either creating or patching it in a Kubernetes API server.
func NewAPIPatchingApplicator(c client.Client) *APIApplicator {
	return &APIApplicator{client: c}
}

// Apply changes to the supplied object. The object will be created if it does
// not exist, or patched if it does. If the object does exist, it will only be
// patched if the passed object has the same or an empty resource version.
func (a *APIApplicator) Apply(ctx context.Context, current client.Object, opts ...ApplyOption) error {
	m, ok := current.(metav1.Object)
	requestOpts := &RequestOptions{}

	if !ok {
		return errors.New("cannot access object metadata")
	}

	if m.GetName() == "" && m.GetGenerateName() != "" {
		if err := a.client.Create(ctx, current); err != nil {
			return fmt.Errorf("cannot create object: %w", err)
		}
	}

	desired := current.DeepCopyObject().(client.Object)

	err := a.client.Get(ctx, types.NamespacedName{Name: m.GetName(), Namespace: m.GetNamespace()}, current)
	if kerrors.IsNotFound(err) {
		// apply options to current (for create)
		if err := applyOpts(ctx, current, requestOpts, opts); liberrors.Ignore(func(err error) bool {
			// ignore optimistic lock error when creating an object because resource version does not yet exist
			return errors.Is(err, ResourceVersionMissing{})
		}, err) != nil {
			return err
		}

		if err := a.client.Create(ctx, current); err != nil {
			return fmt.Errorf("cannot create object: %w", err)
		}
		// no need to patch if object was created
		return nil
	} else if err != nil {
		return fmt.Errorf("cannot get object: %w", err)
	}

	// apply options to desired
	if err := applyOpts(ctx, desired, requestOpts, opts); err != nil {
		return fmt.Errorf("applying options: %w", err)
	}

	// If there is no difference, we need not perform an update. We convert each into
	// unstructured data and remove status fields before the comparison.
	before, err := runtime.DefaultUnstructuredConverter.ToUnstructured(current)
	if err != nil {
		return fmt.Errorf("converting current obj to unstructured: %w", err)
	}

	after, err := runtime.DefaultUnstructuredConverter.ToUnstructured(desired)
	if err != nil {
		return fmt.Errorf("converting desired obj to unstructured: %w", err)
	}

	// https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/#subresources
	hasStatusSubresource := false
	for _, managedFields := range current.GetManagedFields() {
		// we're doing a client-side apply, so we assume we own all fields even if the manager is not our own.
		// in other words, no need to ensure that managedFields.Manager == a.managerName
		// TODO: we should explore using server-side apply
		if managedFields.Subresource == "status" {
			hasStatusSubresource = true
			break
		}
	}
	if hasStatusSubresource {
		unstructured.RemoveNestedField(before, "status")
		unstructured.RemoveNestedField(after, "status")
	}

	if reflect.DeepEqual(before, after) {
		return nil
	}

	// request options that modify apply behavior
	if requestOpts.Update {
		// update
		// automatically set resource version if not supplied by caller
		if desired.GetResourceVersion() == "" {
			desired.SetResourceVersion(current.GetResourceVersion())
		}

		if err = a.client.Update(ctx, desired); err != nil {
			return fmt.Errorf("cannot update object: %w", err)
		}
	} else {
		// patch
		if !requestOpts.EnforceOptimisticLock {
			// ignore optimistic resource lock if `WithOptimisticLock` wasn't specified
			desired.SetResourceVersion("")
		}
		p := &patch{from: desired}
		if err = a.client.Patch(ctx, current, p); err != nil {
			return fmt.Errorf("cannot patch object: %w", err)
		}
	}

	return nil
}

// ApplyStatus updates the object's status subresource. If the object does not exist, an
// error will be returned.
func (a *APIApplicator) ApplyStatus(ctx context.Context, o client.Object, opts ...ApplyOption) error {
	m, ok := o.(metav1.Object)
	if !ok {
		return errors.New("cannot access object metadata")
	}
	requestOpts := &RequestOptions{}

	current := o.DeepCopyObject().(client.Object) // copy so original object isn't mutated by patch
	desired := o.DeepCopyObject().(client.Object)

	err := a.client.Get(ctx, types.NamespacedName{Name: m.GetName(), Namespace: m.GetNamespace()}, current)
	if kerrors.IsNotFound(err) {
		return errors.New("object does not exist, cannot update its status")
	} else if err != nil {
		return fmt.Errorf("cannot get object: %w", err)
	}

	// apply options to desired
	if err := applyOpts(ctx, desired, requestOpts, opts); err != nil {
		return fmt.Errorf("applying options: %w", err)
	}

	before, err := runtime.DefaultUnstructuredConverter.ToUnstructured(current)
	if err != nil {
		return fmt.Errorf("converting current obj to unstructured: %w", err)
	}

	after, err := runtime.DefaultUnstructuredConverter.ToUnstructured(desired)
	if err != nil {
		return fmt.Errorf("converting desired obj to unstructured: %w", err)
	}

	beforeStatus, hasBeforeStatus, err := unstructured.NestedFieldCopy(before, "status")
	if err != nil {
		return fmt.Errorf("copying nested field from current unstructured: %w", err)
	}

	afterStatus, hasAfterStatus, err := unstructured.NestedFieldCopy(after, "status")
	if err != nil {
		return fmt.Errorf("copying nested field from desired unstructured: %w", err)
	}

	if (!hasBeforeStatus && !hasAfterStatus) || reflect.DeepEqual(beforeStatus, afterStatus) {
		return nil
	}

	// copy fields from server data needed for generating a correct patch
	desired.(metav1.Object).SetUID(current.(metav1.Object).GetUID())

	// ignore optimistic resource lock
	// TODO should we add option to enforce optimistic lock?
	desired.SetResourceVersion("")

	if requestOpts.Update {
		// update
		// automatically set resource version if not supplied by caller
		if desired.GetResourceVersion() == "" {
			desired.SetResourceVersion(current.GetResourceVersion())
		}

		if err = a.client.Status().Update(ctx, desired); err != nil {
			return fmt.Errorf("cannot update object status: %w", err)
		}
	} else {
		// patch
		if !requestOpts.EnforceOptimisticLock {
			// ignore optimistic resource lock if `WithOptimisticLock` wasn't specified
			desired.SetResourceVersion("")
		}
		if err = a.client.Status().Patch(ctx, current, &patch{from: desired}); err != nil {
			return fmt.Errorf("cannot patch object status: %w", err)
		}
	}

	return nil
}

type patch struct{ from runtime.Object }

// TODO switch to server side apply
func (p *patch) Type() types.PatchType                { return types.MergePatchType }
func (p *patch) Data(_ client.Object) ([]byte, error) { return json.Marshal(p.from) }

// apply the apply options, mutating the specified object and request opts
func applyOpts(ctx context.Context, o client.Object, requestOpts *RequestOptions, opts []ApplyOption) error {
	// apply options
	for _, fn := range opts {
		if err := fn(ctx, o, requestOpts); err != nil {
			return err
		}
	}

	// apply options that mutate object
	if requestOpts.WithoutOwnerRefs {
		o.SetOwnerReferences([]metav1.OwnerReference{}) // must explicitly signal deletion when using JSON merge semantics
	}

	return nil
}

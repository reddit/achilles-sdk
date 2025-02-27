package internal

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlcontroller "sigs.k8s.io/controller-runtime/pkg/controller"

	"github.com/reddit/achilles-sdk-api/api"
	apitypes "github.com/reddit/achilles-sdk-api/pkg/types"
	"github.com/reddit/achilles-sdk/pkg/io"
	"github.com/reddit/achilles-sdk/pkg/meta"
	"github.com/reddit/achilles-sdk/pkg/status"
)

const (
	finalizer = "cloud.infrared.reddit.com/claim"
)

var (
	errClaimedRefMismatch = errors.New("claimed not owned by claim")
)

type ClaimReconciler[T any, Claimed apitypes.ClaimedType[T], U any, Claim apitypes.ClaimType[U]] struct {
	Client *io.ClientApplicator
	Scheme *runtime.Scheme
	Log    *zap.SugaredLogger

	Name string
	// Hook to run before deleting claimed resource.
	beforeDelete BeforeDelete[T, Claimed, U, Claim]
}

type BeforeDelete[
	T any, Claimed apitypes.ClaimedType[T], U any, Claim apitypes.ClaimType[U],
] func(Claim, Claimed) error

func (r *ClaimReconciler[T, Claimed, U, Claim]) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	requestId := ctrlcontroller.ReconcileIDFromContext(ctx)
	if requestId == "" {
		requestId = uuid.NewUUID()
	}
	log := r.Log.With("request", req, "requestId", requestId)
	log.Debug("entering reconcile")
	defer log.Debug("exiting reconcile")

	claim := Claim(new(U))
	if err := r.Client.Get(ctx, req.NamespacedName, claim); k8serrors.IsNotFound(err) {
		return ctrl.Result{}, nil
	} else if err != nil {
		return ctrl.Result{}, fmt.Errorf("fetching %T %q: %w", claim, req.NamespacedName, err)
	}

	claimed := Claimed(new(T))
	if ref := claim.GetClaimedRef(); ref != nil {
		// claim already bound, populate resource fields for future use
		claimed.SetName(ref.Name)
		if err := r.Client.Get(ctx, ref.ObjectKey(), claimed); err != nil && !k8serrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("fetching %T %q: %w", claimed, ref.Name, err)
		}
	} else {
		// this is a fresh claim, generate a resource name.
		//
		// running a DryRun Create will cause the apiserver to generate and populate a Name without
		// actually creating a new resource.
		//
		// nb: there is a _highly_ unlikely possibility that the generated name will be taken by the time
		// we actually create the claimed resource, this will result in an API error down the line
		// and will require manual intervention to clean up.
		claimed.SetGenerateName(fmt.Sprintf("%s-", claim.GetName()))
		if err := r.Client.Create(ctx, claimed, client.DryRunAll); err != nil {
			return ctrl.Result{}, fmt.Errorf("generating a unique resource name: %w", err)
		}
	}

	// guaranteed to succeed
	claimRef := meta.MustTypedObjectRefFromObject(claim, r.Scheme)
	claimedRef := meta.MustTypedObjectRefFromObject(claimed, r.Scheme)

	if claimed.GetClaimRef() != nil && *claimed.GetClaimRef() != *claimRef {
		// indicates broken references between the claim and the bound resource. needs manual intervention.
		return ctrl.Result{}, fmt.Errorf("%w. got=%s, expected=%s", errClaimedRefMismatch, claimed.GetClaimRef(), claimRef)
	}

	// NOTE: only delete claimed object if claim is deleted and not suspended
	if meta.WasDeleted(claim) && !meta.HasSuspendLabel(claim) {
		if r.beforeDelete != nil {
			if err := r.beforeDelete(claim, claimed); err != nil {
				claim.SetConditions(api.Deleting().WithMessage(err.Error()))
				if err := r.Client.ApplyStatus(ctx, claim); err != nil {
					return ctrl.Result{}, fmt.Errorf("updating claim conditions: %w", err)
				}
				return ctrl.Result{}, fmt.Errorf("before delete hook: %w", err)
			}
		}

		if meta.WasCreated(claimed) {
			if err := r.Client.Delete(ctx, claimed, client.PropagationPolicy(metav1.DeletePropagationForeground)); k8serrors.IsNotFound(err) {
				return ctrl.Result{}, nil
			} else if err != nil {
				return ctrl.Result{}, fmt.Errorf("deleting claimed: %w", err)
			}
		} else {
			// remove finalizer, we're ready to delete
			if err := meta.RemoveFinalizer(ctx, r.Client, claim, finalizer); err != nil && !k8serrors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("removing finalizer: %w", err)
			}
		}

		return ctrl.Result{}, nil
	}

	// ensure finalizer on claim
	if err := meta.AddFinalizer(ctx, r.Client, claim, finalizer); err != nil {
		return ctrl.Result{}, fmt.Errorf("adding finalizer: %w", err)
	}

	// we must claim the bound resource first to prevent races
	if claim.GetClaimedRef() == nil {
		claim.SetClaimedRef(claimedRef)
		if err := r.Client.Apply(ctx, claim); err != nil {
			return ctrl.Result{}, fmt.Errorf("updating client with resource ref: %w", err)
		}
	}

	// ensure the state of the claimed resource
	meta.SetRedditLabels(claimed, r.Name)
	claimed.SetClaimRef(claimRef)

	// NOTE: propagate suspend label to the claimed object
	if meta.HasSuspendLabel(claim) {
		// copy the suspended label
		labels := map[string]string{}
		if claimed.GetLabels() != nil {
			labels = claimed.GetLabels()
		}

		labels[meta.SuspendKey] = "true"
		claimed.SetLabels(labels)
	} else if meta.HasSuspendLabel(claimed) {
		delete(claimed.GetLabels(), meta.SuspendKey)
	}

	// update operation is needed to ensure suspend label is deleted from claimed object
	if err := r.Client.Apply(ctx, claimed, io.AsUpdate()); err != nil {
		return ctrl.Result{}, fmt.Errorf("applying claimed: %w", err)
	}

	// initialize claim conditions if not previously initialized,
	// to avoid live-lock caused by constantly updating lastTransitionTime
	if claim.GetCondition(api.TypeReady).Status == corev1.ConditionUnknown {
		claim.SetConditions(api.Creating())
	}

	// only condition for claim readiness is if the claimed is ready
	if status.ResourceReady(claimed) {
		availableCondition := api.Available()
		availableCondition.ObservedGeneration = claim.GetGeneration()
		claim.SetConditions(availableCondition)
	}

	// update claim status
	if err := r.Client.ApplyStatus(ctx, claim); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating claim conditions: %w", err)
	}

	return ctrl.Result{}, nil
}

func NewClaimReconciler[T any, Claimed apitypes.ClaimedType[T], U any, Claim apitypes.ClaimType[U]](
	_ Claimed,
	claim Claim,
	client *io.ClientApplicator,
	scheme *runtime.Scheme,
	log *zap.SugaredLogger,
	beforeDelete BeforeDelete[T, Claimed, U, Claim],
) ClaimReconciler[T, Claimed, U, Claim] {
	gvk := meta.MustGVKForObject(claim, scheme)

	return ClaimReconciler[T, Claimed, U, Claim]{
		Name:         gvk.Kind,
		Client:       client,
		Scheme:       scheme,
		Log:          log.Named(gvk.Kind),
		beforeDelete: beforeDelete,
	}
}

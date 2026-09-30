package shard

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
	coordinationv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Ownership is the answer to "may this instance reconcile this object".
type Ownership int

const (
	// Owned means this instance holds the lease on the object's partition.
	Owned Ownership = iota

	// NotSelected means the object belongs to another shard's slice. Nothing will change that, so
	// there is nothing to wait for.
	NotSelected

	// Pending means this instance selects the object's partition but does not hold its lease,
	// either because it has not synced yet or because another shard still holds it. Transient, so
	// the object is worth revisiting.
	Pending
)

// ListFunc reports the shard partitions objects currently occupy.
type ListFunc func(context.Context) (Inventory, error)

// OwnerConfig configures an Owner.
type OwnerConfig struct {
	// Shard is the slice this instance selects.
	Shard *Shard

	// Group scopes ownership to the controller partitioning these types, so that leases of other
	// controllers are left alone.
	Group Group

	// Instance names this controller instance, i.e. its Deployment. Leader election elects one
	// leader per instance, which is what makes takeover of our own instance's leases safe.
	Instance string

	// Namespace is where ownership Leases live.
	Namespace string

	// Identity distinguishes this process from others, conventionally the pod name.
	Identity string

	// LeaseDuration is how long a held lease stays valid without renewal. Defaults to
	// DefaultLeaseDuration.
	LeaseDuration time.Duration

	// List enumerates the partitions in use.
	List ListFunc

	// Log is optional.
	Log *zap.SugaredLogger
}

// heldSet is the set of partitions this instance currently holds, replaced wholesale on each sync
// so that readers never observe a partially updated view.
type heldSet struct {
	values    sets.Set[string]
	unlabeled bool
}

// Owner arbitrates which instance of a controller reconciles which objects, by holding a Lease per
// concrete shard value rather than by trusting selectors to be disjoint.
//
// Arbitrating over the values in use rather than over the selectors is what makes overlapping
// selectors safe: two shards may both select a value, but only one can hold its lease, so the
// objects carrying it are still reconciled once. The cost is that ownership is decided at run time,
// so a reconcile must consult Ownership rather than assume its cache only contains its own work.
//
// Mutual exclusion is as strong as Kubernetes leader election and no stronger: a process stalled
// past its lease expiry can wake up believing it still holds a value. Leases carry no fencing
// token, so this is a guarantee about the common case, not about all cases.
type Owner struct {
	client client.Client
	cfg    OwnerConfig
	held   atomic.Pointer[heldSet]
}

// NewOwner returns an Owner for the given configuration.
func NewOwner(c client.Client, cfg OwnerConfig) *Owner {
	if cfg.LeaseDuration == 0 {
		cfg.LeaseDuration = DefaultLeaseDuration
	}
	return &Owner{client: c, cfg: cfg}
}

// Shard returns the slice this instance selects.
func (o *Owner) Shard() *Shard { return o.cfg.Shard }

// Ownership reports whether this instance may reconcile the given object.
//
// Before the first sync every selected partition is Pending rather than Owned, since reconciling
// an object whose lease is unheld is exactly what the lease exists to prevent.
func (o *Owner) Ownership(obj client.Object) Ownership {
	ref := valueOf(obj, o.cfg.Shard.Key())
	if !ref.selectedBy(o.cfg.Shard) {
		return NotSelected
	}

	held := o.held.Load()
	if held == nil {
		return Pending
	}
	if ref.unlabeled {
		if held.unlabeled {
			return Owned
		}
		return Pending
	}
	if held.values.Has(ref.value) {
		return Owned
	}
	return Pending
}

// Start keeps this instance's ownership current until the context is cancelled, satisfying
// manager.Runnable.
func (o *Owner) Start(ctx context.Context) error {
	if err := o.Sync(ctx); err != nil && o.cfg.Log != nil {
		o.cfg.Log.Warnw("syncing shard ownership", "error", err)
	}

	ticker := time.NewTicker(o.cfg.LeaseDuration / 3)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := o.Sync(ctx); err != nil && o.cfg.Log != nil {
				// A failed sync lets held leases lapse, which hands work to another shard rather
				// than duplicating it, so it is not worth taking the process down for.
				o.cfg.Log.Warnw("syncing shard ownership", "error", err)
			}
		}
	}
}

// NeedLeaderElection reports that only the leader of a shard holds its values, so that a standby
// replica does not hold work it will not do.
func (o *Owner) NeedLeaderElection() bool { return true }

// Sync brings the ownership Leases in line with the partitions in use and with this shard's
// selector: it creates a Lease for every partition, acquires or renews the ones this shard selects,
// releases the ones it no longer selects, and deletes the ones no object occupies.
func (o *Owner) Sync(ctx context.Context) error {
	inventory, err := o.cfg.List(ctx)
	if err != nil {
		return fmt.Errorf("listing shard values in use: %w", err)
	}

	held := &heldSet{values: sets.New[string]()}
	live := sets.New[string]()
	var contested, unowned int

	for _, ref := range inventory.refs() {
		name := valueLeaseName(o.cfg.Group.Name, ref.value, ref.unlabeled)
		live.Insert(name)

		state, err := o.reconcileLease(ctx, name, ref)
		if err != nil {
			return err
		}

		switch state {
		case leaseHeld:
			if ref.unlabeled {
				held.unlabeled = true
			} else {
				held.values.Insert(ref.value)
			}
		case leaseContested:
			contested++
			if o.cfg.Log != nil {
				o.cfg.Log.Warnw("shard value is selected but held by another shard",
					"value", ref.String(), "shard", o.cfg.Shard.ID())
			}
		case leaseUnowned:
			unowned++
			if o.cfg.Log != nil {
				o.cfg.Log.Warnw("shard value is owned by no instance, its objects go unreconciled",
					"value", ref.String())
			}
		}
	}

	o.held.Store(held)

	if err := o.prune(ctx, live); err != nil {
		return err
	}

	recordOwnership(o.cfg.Group.Name, o.cfg.Shard.ID(), len(held.values), contested, unowned)
	return nil
}

// leaseState is the outcome of reconciling one partition's Lease.
type leaseState int

const (
	leaseHeld leaseState = iota
	leaseContested
	leaseUnowned
	leaseHeldByOther
)

// reconcileLease drives one partition's Lease towards the state this shard wants for it.
//
// Acquisition is a conflict-checked write against the resource version just read, so two instances
// racing for the same value cannot both succeed. That is the difference between this and comparing
// selectors at startup, which could only detect a conflict that had already happened.
func (o *Owner) reconcileLease(ctx context.Context, name string, ref valueRef) (leaseState, error) {
	selected := ref.selectedBy(o.cfg.Shard)

	var lease coordinationv1.Lease
	err := o.client.Get(ctx, client.ObjectKey{Namespace: o.cfg.Namespace, Name: name}, &lease)

	if apierrors.IsNotFound(err) {
		created, err := o.create(ctx, name, ref, selected)
		if err == nil {
			return created, nil
		}
		if !apierrors.IsAlreadyExists(err) {
			return leaseUnowned, fmt.Errorf("creating ownership lease for value %s: %w", ref, err)
		}
		// Another instance created it first. Re-read rather than assume, so that losing the race
		// is not reported as the value being unowned.
		if err := o.client.Get(ctx, client.ObjectKey{Namespace: o.cfg.Namespace, Name: name}, &lease); err != nil {
			return leaseUnowned, fmt.Errorf("getting ownership lease for value %s: %w", ref, err)
		}
		return o.stateOf(&lease, selected), nil
	}
	if err != nil {
		return leaseUnowned, fmt.Errorf("getting ownership lease for value %s: %w", ref, err)
	}

	switch {
	case heldBy(&lease) == o.cfg.Identity && selected:
		return o.write(ctx, &lease, ref, o.cfg.Identity, leaseHeld)

	case heldBy(&lease) == o.cfg.Identity && !selected:
		// Releasing rather than letting it expire lets another shard pick the value up at once.
		if _, err := o.write(ctx, &lease, ref, "", leaseUnowned); err != nil {
			return leaseUnowned, err
		}
		return leaseUnowned, nil

	case selected && o.mayTakeOver(&lease):
		return o.write(ctx, &lease, ref, o.cfg.Identity, leaseHeld)

	default:
		return o.stateOf(&lease, selected), nil
	}
}

// mayTakeOver reports whether this instance can claim a lease it does not hold.
//
// A lease labelled with our own instance belongs to a predecessor leader of this very Deployment:
// leader election guarantees one leader per instance at a time, so waiting out its expiry would only
// add failover latency. Keying on the instance rather than the shard also keeps a selector edit a
// clean rolling update, since the incoming pod reclaims its predecessor's values immediately even
// though its shard ID has changed.
func (o *Owner) mayTakeOver(lease *coordinationv1.Lease) bool {
	return heldBy(lease) == "" ||
		expired(lease) ||
		lease.Labels[InstanceLabelKey] == o.cfg.Instance
}

func (o *Owner) stateOf(lease *coordinationv1.Lease, selected bool) leaseState {
	switch {
	case heldBy(lease) == "" || expired(lease):
		return leaseUnowned
	case selected:
		return leaseContested
	default:
		return leaseHeldByOther
	}
}

func (o *Owner) create(ctx context.Context, name string, ref valueRef, selected bool) (leaseState, error) {
	lease := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: o.cfg.Namespace},
	}

	identity := ""
	state := leaseUnowned
	if selected {
		identity = o.cfg.Identity
		state = leaseHeld
	}

	o.stampValue(lease, ref, identity)
	if err := o.client.Create(ctx, lease); err != nil {
		return leaseUnowned, err
	}
	return state, nil
}

// write persists an ownership change, treating a lost race as a state to re-evaluate next sync
// rather than as an error.
func (o *Owner) write(
	ctx context.Context,
	lease *coordinationv1.Lease,
	ref valueRef,
	identity string,
	onSuccess leaseState,
) (leaseState, error) {
	o.stampValue(lease, ref, identity)

	if err := o.client.Update(ctx, lease); err != nil {
		if apierrors.IsConflict(err) {
			return leaseContested, nil
		}
		return leaseUnowned, fmt.Errorf("writing ownership lease for value %s: %w", ref, err)
	}
	return onSuccess, nil
}

func (o *Owner) stampValue(lease *coordinationv1.Lease, ref valueRef, identity string) {
	if lease.Labels == nil {
		lease.Labels = map[string]string{}
	}
	lease.Labels[GroupLabelKey] = o.cfg.Group.Name
	lease.Labels[KindLabelKey] = kindValue

	if lease.Annotations == nil {
		lease.Annotations = map[string]string{}
	}
	lease.Annotations[ValueAnnotationKey] = ref.value
	lease.Annotations[TypesAnnotationKey] = o.cfg.Group.Types

	now := metav1.NewMicroTime(time.Now())
	seconds := int32(o.cfg.LeaseDuration.Seconds())
	lease.Spec.LeaseDurationSeconds = &seconds
	lease.Spec.RenewTime = &now

	if identity == "" {
		// Released, so the instance label must go too: it is what permits immediate takeover.
		delete(lease.Labels, InstanceLabelKey)
		delete(lease.Labels, IDLabelKey)
		lease.Spec.HolderIdentity = nil
		lease.Spec.AcquireTime = nil
		return
	}

	lease.Labels[InstanceLabelKey] = o.cfg.Instance
	lease.Labels[IDLabelKey] = o.cfg.Shard.ID()
	if heldBy(lease) != identity {
		lease.Spec.AcquireTime = &now
	}
	lease.Spec.HolderIdentity = &identity
}

// prune deletes the Leases of partitions no object occupies, keeping the Lease set a faithful map
// of the shard values actually in use.
func (o *Owner) prune(ctx context.Context, live sets.Set[string]) error {
	leases, err := o.listValueLeases(ctx)
	if err != nil {
		return err
	}

	for i := range leases {
		lease := &leases[i]
		if live.Has(lease.Name) {
			continue
		}
		if err := o.client.Delete(ctx, lease); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("deleting stale ownership lease %s: %w", lease.Name, err)
		}
	}
	return nil
}

func (o *Owner) listValueLeases(ctx context.Context) ([]coordinationv1.Lease, error) {
	var leases coordinationv1.LeaseList
	if err := o.client.List(ctx, &leases,
		client.InNamespace(o.cfg.Namespace),
		client.MatchingLabels{GroupLabelKey: o.cfg.Group.Name, KindLabelKey: kindValue},
	); err != nil {
		return nil, fmt.Errorf("listing ownership leases: %w", err)
	}
	return leases.Items, nil
}

func heldBy(lease *coordinationv1.Lease) string {
	if lease.Spec.HolderIdentity == nil {
		return ""
	}
	return *lease.Spec.HolderIdentity
}

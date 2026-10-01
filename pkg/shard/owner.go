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

	// NotManaged means the object's shard value is not one this instance was configured with,
	// either because another instance manages it or because no instance does. Nothing will change
	// that without a configuration change, so there is nothing to wait for.
	NotManaged

	// Pending means this instance manages the object's partition but does not hold its lease,
	// either because it has not synced yet or because another instance still holds it. Transient,
	// so the object is worth revisiting.
	Pending
)

// ListFunc reports the shard partitions objects currently occupy.
type ListFunc func(context.Context) (Inventory, error)

// OwnerConfig configures an Owner.
type OwnerConfig struct {
	// Shard is the set of values this instance manages.
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

	// List enumerates the partitions in use, used to report the ones no instance manages.
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
// managed shard value.
//
// Ownership is configured, not inferred: an instance claims exactly the values it was given, so
// two instances misconfigured with the same value still reconcile its objects once — one holds the
// lease and the other defers. A value no instance was given is reconciled by nobody and reported
// through achilles_shard_values_ignored, since which instance should serve it is not ours to guess.
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

// Shard returns the set of values this instance manages.
func (o *Owner) Shard() *Shard { return o.cfg.Shard }

// Ownership reports whether this instance may reconcile the given object.
//
// Before the first sync every managed partition is Pending rather than Owned, since reconciling an
// object whose lease is unheld is exactly what the lease exists to prevent.
func (o *Owner) Ownership(obj client.Object) Ownership {
	ref := valueOf(obj, o.cfg.Shard.Key())
	if !ref.managedBy(o.cfg.Shard) {
		return NotManaged
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
				// A failed sync lets held leases lapse, which hands work to another instance
				// rather than duplicating it, so it is not worth taking the process down for.
				o.cfg.Log.Warnw("syncing shard ownership", "error", err)
			}
		}
	}
}

// NeedLeaderElection reports that only the leader of an instance holds its values, so that a
// standby replica does not hold work it will not do.
func (o *Owner) NeedLeaderElection() bool { return true }

// Sync acquires or renews a Lease for every value this instance is configured with, releases the
// ones it no longer is, and reports which partitions in use no instance manages.
func (o *Owner) Sync(ctx context.Context) error {
	held := &heldSet{values: sets.New[string]()}
	managed := sets.New[string]()
	var contested int

	for _, ref := range o.cfg.Shard.refs() {
		name := valueLeaseName(o.cfg.Group.Name, ref.value, ref.unlabeled)
		managed.Insert(name)

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
				o.cfg.Log.Warnw("shard value is managed by this instance but held by another",
					"value", ref.String(), "shard", o.cfg.Shard.ID())
			}
		}
	}

	o.held.Store(held)

	inUse, err := o.reportPartitionsInUse(ctx)
	if err != nil {
		return err
	}
	if err := o.releaseStale(ctx, managed, inUse); err != nil {
		return err
	}

	recordOwnership(o.cfg.Group.Name, o.cfg.Shard.ID(), held.count(), contested)
	return nil
}

func (h *heldSet) count() int {
	n := h.values.Len()
	if h.unlabeled {
		n++
	}
	return n
}

// reportPartitionsInUse records, for every partition objects actually occupy, whether this instance
// ignores it. Returns the lease names of those partitions.
//
// Both outcomes are recorded, not just the ignored ones, so that a partition ignored by *every*
// instance is identifiable as one whose series are all 1. Reporting only the ignored ones would
// instead require knowing how many instances are running.
func (o *Owner) reportPartitionsInUse(ctx context.Context) (sets.Set[string], error) {
	inventory, err := o.cfg.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing shard values in use: %w", err)
	}

	names := sets.New[string]()
	ignored := map[string]bool{}
	for _, ref := range inventory.refs() {
		names.Insert(valueLeaseName(o.cfg.Group.Name, ref.value, ref.unlabeled))

		managed := ref.managedBy(o.cfg.Shard)
		ignored[ref.String()] = !managed
		if !managed && o.cfg.Log != nil {
			o.cfg.Log.Debugw("ignoring shard value, this instance does not manage it",
				"value", ref.String(), "shard", o.cfg.Shard.ID())
		}
	}

	recordIgnored(o.cfg.Group.Name, o.cfg.Shard.ID(), ignored)
	return names, nil
}

// leaseState is the outcome of reconciling one partition's Lease.
type leaseState int

const (
	leaseHeld leaseState = iota
	leaseContested
	leaseUnowned
	leaseHeldByOther
)

// reconcileLease drives one managed partition's Lease towards being held by this instance.
//
// Acquisition is a conflict-checked write against the resource version just read, so two instances
// misconfigured with the same value cannot both succeed.
func (o *Owner) reconcileLease(ctx context.Context, name string, ref valueRef) (leaseState, error) {
	var lease coordinationv1.Lease
	err := o.client.Get(ctx, client.ObjectKey{Namespace: o.cfg.Namespace, Name: name}, &lease)

	if apierrors.IsNotFound(err) {
		created, err := o.create(ctx, name, ref)
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
		return o.stateOf(&lease), nil
	}
	if err != nil {
		return leaseUnowned, fmt.Errorf("getting ownership lease for value %s: %w", ref, err)
	}

	if heldBy(&lease) == o.cfg.Identity || o.mayTakeOver(&lease) {
		return o.write(ctx, &lease, ref, o.cfg.Identity, leaseHeld)
	}
	return o.stateOf(&lease), nil
}

// mayTakeOver reports whether this instance can claim a lease it does not hold.
//
// A lease labelled with our own instance belongs to a predecessor leader of this very Deployment:
// leader election guarantees one leader per instance at a time, so waiting out its expiry would only
// add failover latency. Keying on the instance rather than the shard also keeps a change to the
// managed values a clean rolling update, since the incoming pod reclaims its predecessor's values
// immediately even though its shard ID has changed.
func (o *Owner) mayTakeOver(lease *coordinationv1.Lease) bool {
	return heldBy(lease) == "" ||
		expired(lease) ||
		lease.Labels[InstanceLabelKey] == o.cfg.Instance
}

func (o *Owner) stateOf(lease *coordinationv1.Lease) leaseState {
	if heldBy(lease) == "" || expired(lease) {
		return leaseUnowned
	}
	return leaseContested
}

func (o *Owner) create(ctx context.Context, name string, ref valueRef) (leaseState, error) {
	lease := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: o.cfg.Namespace},
	}

	o.stampValue(lease, ref, o.cfg.Identity)
	if err := o.client.Create(ctx, lease); err != nil {
		return leaseUnowned, err
	}
	return leaseHeld, nil
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
	// The resolved value, spelled as --shard-values takes it, since the Lease name only
	// approximates it once a value is long enough or odd enough to need hashing.
	lease.Annotations[ValueAnnotationKey] = ref.String()
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

// releaseStale gives up the leases this instance holds for values it is no longer configured with,
// and deletes the leases of partitions that neither any object occupies nor this instance manages.
//
// Releasing rather than waiting for expiry lets the instance now configured with the value pick it
// up at once, which is what makes a change to --shard-values a clean rolling update.
func (o *Owner) releaseStale(ctx context.Context, managed, inUse sets.Set[string]) error {
	leases, err := o.listValueLeases(ctx)
	if err != nil {
		return err
	}

	for i := range leases {
		lease := &leases[i]
		if managed.Has(lease.Name) {
			continue
		}

		if heldBy(lease) == o.cfg.Identity {
			ref := refFromLease(lease)
			if _, err := o.write(ctx, lease, ref, "", leaseUnowned); err != nil {
				return err
			}
			continue
		}

		// Unheld and occupied by nothing: no instance can want it, and whichever instance is
		// configured with it recreates it on its next sync.
		if heldBy(lease) == "" && !inUse.Has(lease.Name) {
			if err := o.client.Delete(ctx, lease); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("deleting stale ownership lease %s: %w", lease.Name, err)
			}
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

// refFromLease recovers the partition a Lease confers from its annotation, which holds the value
// verbatim even when the name had to be hashed.
func refFromLease(lease *coordinationv1.Lease) valueRef {
	value := lease.Annotations[ValueAnnotationKey]
	if value == UnlabeledValue {
		return valueRef{unlabeled: true}
	}
	return valueRef{value: value}
}

func heldBy(lease *coordinationv1.Lease) string {
	if lease.Spec.HolderIdentity == nil {
		return ""
	}
	return *lease.Spec.HolderIdentity
}

package shard

import (
	"context"
	"errors"
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

// Ownership answers whether this instance may reconcile an object.
type Ownership int

const (
	// Owned means this instance serves the object's shard value and holds its Lease.
	Owned Ownership = iota

	// NotManaged means the object's value is not one this instance serves. Another instance serves
	// it, or no instance does; either way there is nothing to wait for.
	NotManaged

	// Pending means this instance serves the object's value but does not hold its Lease, because
	// it has not synced yet or because another instance still holds it. Both resolve on their own.
	Pending
)

// ListFunc reports the shard values objects currently carry.
type ListFunc func(context.Context) (Inventory, error)

// OwnerConfig configures an Owner.
type OwnerConfig struct {
	// Shard is the set of values this instance serves.
	Shard *Shard

	// Instance names this controller instance, which is the name of the workload running it.
	Instance string

	// Namespace is where shard Leases live.
	Namespace string

	// Identity distinguishes this process from others of the same instance, conventionally its
	// pod name.
	Identity string

	// LeasePrefix names this controller's shard Leases. Validate it against the configured values
	// with ValidateLeasePrefix.
	LeasePrefix string

	// LeaseDuration defaults to DefaultLeaseDuration.
	LeaseDuration time.Duration

	// List enumerates the values objects carry, used to report the ones no instance serves.
	List ListFunc

	// Log is optional.
	Log *zap.SugaredLogger
}

// Owner holds a Lease for each shard value this instance serves, and answers whether an object may
// be reconciled.
//
// Configuration decides which values an instance wants; the Leases decide which it may act on.
//
// Exclusion is as strong as Kubernetes leader election and no stronger. A Lease carries no fencing
// token, so a process stalled past its lease expiry can wake up still believing it holds a value.
type Owner struct {
	client client.Client
	cfg    OwnerConfig

	// Replaced wholesale on each sync, so a reader never observes a partially updated view.
	held atomic.Pointer[sets.Set[string]]

	// syncedAt is when the sync loop last completed, in Unix nanoseconds.
	syncedAt atomic.Int64

	// observed and confirmed are read and written only by Sync, which one goroutine runs.
	observed  map[string]observation
	confirmed map[string]time.Time
}

// observation is what this process last saw of one value's Lease, and when it saw it.
//
// Age is measured from at, this process's own clock, rather than from the holder's RenewTime, so
// that a holder whose clock is skewed cannot have its live claim mistaken for an abandoned one.
// Mirrors client-go's leader election, which tracks an observedTime for the same reason. The cost
// is that a first sighting proves nothing, so an abandoned Lease takes a full duration to claim.
type observation struct {
	holder   string
	renewed  time.Time
	duration time.Duration
	at       time.Time
}

// NewOwner returns an Owner for the given configuration.
func NewOwner(c client.Client, cfg OwnerConfig) *Owner {
	if cfg.LeaseDuration == 0 {
		cfg.LeaseDuration = DefaultLeaseDuration
	}
	return &Owner{
		client:    c,
		cfg:       cfg,
		observed:  map[string]observation{},
		confirmed: map[string]time.Time{},
	}
}

// Ownership reports whether this instance may reconcile the given object.
func (o *Owner) Ownership(obj client.Object) Ownership {
	value, ok := valueOf(obj, o.cfg.Shard.Key())
	if !ok || !o.cfg.Shard.Serves(value) {
		return NotManaged
	}

	held := o.held.Load()
	if held == nil || !held.Has(value) || o.stale() {
		return Pending
	}
	return Owned
}

// stale reports whether this instance's view of what it holds is too old to act on.
//
// Bounds double reconciliation when the sync loop stops running at all, which refreshes nothing
// and so never reaches claimIsFresh. Measured against the lease duration, since that is when
// another instance can take the values over.
func (o *Owner) stale() bool {
	at := o.syncedAt.Load()
	return at == 0 || time.Since(time.Unix(0, at)) > o.cfg.LeaseDuration
}

// NeedLeaderElection reports that only an instance's leader claims its values, so that a standby
// replica does not hold work it will not do.
func (o *Owner) NeedLeaderElection() bool { return true }

// Start keeps this instance's claims current until the context is cancelled, then gives them up,
// satisfying manager.Runnable.
func (o *Owner) Start(ctx context.Context) error {
	o.sync(ctx)

	ticker := time.NewTicker(o.cfg.LeaseDuration / 3)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			o.release()
			return nil
		case <-ticker.C:
			o.sync(ctx)
		}
	}
}

// release gives up every value this instance holds, so that an instance newly configured with one
// of them can claim it at once instead of waiting out the lease.
//
// Runs on its own context, because the one Start was given is cancelled by the time we get here.
// Best effort: a process that is killed outright releases nothing, so lease expiry remains what
// actually bounds how long a value stays claimed.
func (o *Owner) release() {
	empty := sets.New[string]()
	held := o.held.Swap(&empty)
	if held == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), releaseTimeout)
	defer cancel()

	for _, value := range sets.List(*held) {
		if err := o.releaseValue(ctx, value); err != nil && o.cfg.Log != nil {
			o.cfg.Log.Warnw("releasing shard value", "value", value, "error", err)
		}
	}
}

// releaseValue clears this process's claim on one value.
//
// Identity-checked and conflict-checked, because clearing the holder of a value another process
// has since taken would take it from them rather than give it up.
func (o *Owner) releaseValue(ctx context.Context, value string) error {
	key := client.ObjectKey{Namespace: o.cfg.Namespace, Name: leaseName(o.cfg.LeasePrefix, value)}

	var lease coordinationv1.Lease
	if err := o.client.Get(ctx, key, &lease); apierrors.IsNotFound(err) {
		return nil
	} else if err != nil {
		return err
	}

	if heldBy(&lease) != o.cfg.Identity {
		return nil
	}

	delete(lease.Labels, InstanceLabelKey)
	lease.Spec.HolderIdentity = nil
	lease.Spec.AcquireTime = nil

	if err := o.client.Update(ctx, &lease); apierrors.IsConflict(err) {
		// Another process wrote first, so there is nothing left to give up.
		return nil
	} else if err != nil {
		return err
	}
	return nil
}

// sync logs a failure rather than returning it, since failing the runnable would take the process
// down. Ownership is given up by claimIsFresh rather than by this returning, so a failure that
// persists stops this instance acting on the value regardless.
func (o *Owner) sync(ctx context.Context) {
	if err := o.Sync(ctx); err != nil && o.cfg.Log != nil {
		o.cfg.Log.Warnw("syncing shard ownership", "error", err)
	}
}

// Sync claims or renews a Lease for every value this instance serves, then reports which values
// objects carry that this instance does not serve.
//
// Every value is attempted even when earlier ones fail, so that one unreachable Lease cannot mask
// the state of the values after it, and the failures are returned together.
func (o *Owner) Sync(ctx context.Context) error {
	held := sets.New[string]()
	contested := 0
	var errs []error

	for _, value := range o.cfg.Shard.Values() {
		holder, err := o.claim(ctx, value)
		switch {
		case err != nil:
			errs = append(errs, err)
			// A failure is not evidence the claim is gone, so keep it while it could still be
			// live. Past that another instance may hold it and acting would double-reconcile.
			if o.claimIsFresh(value) {
				held.Insert(value)
			}

		case holder == o.cfg.Instance:
			o.confirmed[value] = time.Now()
			held.Insert(value)

		default:
			delete(o.confirmed, value)
			contested++
			if o.cfg.Log != nil {
				o.cfg.Log.Warnw("another instance holds a shard value this instance serves",
					"value", value, "holder", holder)
			}
		}
	}
	o.held.Store(&held)

	if err := o.report(ctx); err != nil {
		errs = append(errs, err)
	}

	recordClaims(o.cfg.Instance, held.Len(), contested)
	o.syncedAt.Store(time.Now().UnixNano())

	return errors.Join(errs...)
}

// claimIsFresh reports whether this process's last confirmed claim on a value could still be live,
// bounding how long a failing instance goes on acting on it by the lease duration itself.
func (o *Owner) claimIsFresh(value string) bool {
	confirmed, ok := o.confirmed[value]
	return ok && time.Since(confirmed) < o.cfg.LeaseDuration
}

// observe records the state of a value's Lease, keeping the time of the first sighting of an
// unchanged one so that age accumulates across syncs.
func (o *Owner) observe(value string, lease *coordinationv1.Lease) {
	current := observation{
		holder:   heldBy(lease),
		renewed:  renewedAt(lease),
		duration: declaredDuration(lease, o.cfg.LeaseDuration),
		at:       time.Now(),
	}

	if prev, ok := o.observed[value]; ok &&
		prev.holder == current.holder &&
		prev.renewed.Equal(current.renewed) {
		return
	}
	o.observed[value] = current
}

// aged reports whether the Lease this process last saw for a value has gone unrenewed for its
// whole duration, measured from when we saw it.
func (o *Owner) aged(value string) bool {
	obs, ok := o.observed[value]
	if !ok {
		return false
	}
	// Nobody ever renewed it, so there is no claim to have aged out.
	if obs.renewed.IsZero() {
		return true
	}
	return time.Since(obs.at) > obs.duration
}

// claim drives one value's Lease towards being held by this process, returning the instance that
// holds it afterwards, which is this one when the claim succeeded.
//
// The claim is a conflict-checked write against the version just read, so two instances configured
// with the same value cannot both succeed.
func (o *Owner) claim(ctx context.Context, value string) (string, error) {
	key := client.ObjectKey{Namespace: o.cfg.Namespace, Name: leaseName(o.cfg.LeasePrefix, value)}

	var lease coordinationv1.Lease
	err := o.client.Get(ctx, key, &lease)
	if apierrors.IsNotFound(err) {
		lease = coordinationv1.Lease{ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace}}
		o.stamp(&lease)

		err = o.client.Create(ctx, &lease)
		if err == nil {
			return o.cfg.Instance, nil
		}
		if !apierrors.IsAlreadyExists(err) {
			return "", fmt.Errorf("creating shard lease for value %q: %w", value, err)
		}
		// Another instance created it first. Re-read rather than assume, so that losing the race
		// is not mistaken for the value being free.
		if err := o.client.Get(ctx, key, &lease); err != nil {
			return "", fmt.Errorf("getting shard lease for value %q: %w", value, err)
		}
	} else if err != nil {
		return "", fmt.Errorf("getting shard lease for value %q: %w", value, err)
	}

	o.observe(value, &lease)

	holder := lease.Labels[InstanceLabelKey]
	if !o.mayClaim(value, &lease) {
		return holder, nil
	}

	o.stamp(&lease)
	if err := o.client.Update(ctx, &lease); err != nil {
		if apierrors.IsConflict(err) {
			// Another process wrote first; the next sync reads the result.
			return holder, nil
		}
		return "", fmt.Errorf("claiming shard lease for value %q: %w", value, err)
	}
	return o.cfg.Instance, nil
}

// mayClaim reports whether this process can take a Lease.
//
// A Lease bearing our own instance is claimable even while live: leader election admits one leader
// per instance at a time, so it was left by a predecessor of ours and waiting for it to age out
// would only add failover latency. That shortcut is what keeps a rolling update prompt despite
// age being measured from first sighting.
func (o *Owner) mayClaim(value string, lease *coordinationv1.Lease) bool {
	return heldBy(lease) == "" ||
		o.aged(value) ||
		lease.Labels[InstanceLabelKey] == o.cfg.Instance
}

func (o *Owner) stamp(lease *coordinationv1.Lease) {
	now := metav1.NewMicroTime(time.Now())
	seconds := int32(o.cfg.LeaseDuration.Seconds())

	if lease.Labels == nil {
		lease.Labels = map[string]string{}
	}
	lease.Labels[InstanceLabelKey] = o.cfg.Instance

	if heldBy(lease) != o.cfg.Identity {
		lease.Spec.AcquireTime = &now
	}
	lease.Spec.HolderIdentity = &o.cfg.Identity
	lease.Spec.RenewTime = &now
	lease.Spec.LeaseDurationSeconds = &seconds
}

// report records, for every value objects carry, whether this instance serves it.
//
// Both outcomes are recorded, not just the ignored ones, so that a value served by nobody is
// identifiable as one every instance reports as ignored. Reporting only ignored values would
// instead require knowing how many instances are running.
func (o *Owner) report(ctx context.Context) error {
	inventory, err := o.cfg.List(ctx)
	if err != nil {
		return fmt.Errorf("listing shard values in use: %w", err)
	}

	ignored := make(map[string]bool, inventory.Values.Len()+1)
	for _, value := range inventory.reported() {
		ignored[value] = !o.cfg.Shard.Serves(value)
	}

	if inventory.Missing && o.cfg.Log != nil {
		o.cfg.Log.Warnw("objects carry no shard value, so no instance can reconcile them",
			"key", o.cfg.Shard.Key())
	}

	recordIgnored(o.cfg.Instance, ignored)
	return nil
}

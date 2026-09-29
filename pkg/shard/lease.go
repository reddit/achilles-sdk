package shard

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	coordinationv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// GroupLabelKey scopes a shard advertisement to one controller, so that unrelated controllers
	// sharing a namespace do not appear to conflict.
	GroupLabelKey = "shard.infrared.reddit.com/group"

	// IDLabelKey holds Shard.ID, which distinguishes a genuine conflict from a rolling update of
	// the same shard.
	IDLabelKey = "shard.infrared.reddit.com/id"

	// SelectorAnnotationKey holds the advertised selector verbatim.
	SelectorAnnotationKey = "shard.infrared.reddit.com/selector"
)

// DefaultLeaseDuration is how long an advertisement stays valid without renewal.
const DefaultLeaseDuration = 30 * time.Second

// Config describes the advertisement this process publishes.
type Config struct {
	// Shard is the slice this process owns.
	Shard *Shard

	// Group identifies the controller, so peers of other controllers are ignored.
	Group string

	// Namespace is where advertisement Leases live.
	Namespace string

	// Identity distinguishes this process from others serving the same shard, conventionally the
	// pod name.
	Identity string

	// LeaseDuration defaults to DefaultLeaseDuration.
	LeaseDuration time.Duration

	// Log is optional.
	Log *zap.SugaredLogger
}

// Advertiser publishes this process's shard and refuses to start if a different shard already
// claims an overlapping slice.
//
// It deliberately records what a process is *running* rather than what a manifest declares, so it
// cannot be fooled by args arriving through an entrypoint wrapper, env expansion, or a ConfigMap.
// The cost is that it cannot see a peer that crashed before advertising; catching that case belongs
// in a pre-merge lint over the manifests.
type Advertiser struct {
	client client.Client
	cfg    Config
}

// NewAdvertiser returns an Advertiser for the given configuration.
func NewAdvertiser(c client.Client, cfg Config) *Advertiser {
	if cfg.LeaseDuration == 0 {
		cfg.LeaseDuration = DefaultLeaseDuration
	}
	return &Advertiser{client: c, cfg: cfg}
}

// LeaseName is the name of this shard's advertisement Lease. Pods serving the same shard share one
// Lease and take over its holder identity in turn.
func (a *Advertiser) LeaseName() string {
	return fmt.Sprintf("%s-shard-%s", a.cfg.Group, a.cfg.Shard.ID())
}

// Claim verifies no other shard of the same controller claims an overlapping slice, then publishes
// this process's own advertisement.
//
// Must run before the manager starts, and therefore against an uncached client: the manager's
// client is backed by a cache whose informers have not synced yet, and a shard-filtered cache would
// not match these Leases anyway.
func (a *Advertiser) Claim(ctx context.Context) error {
	if err := a.verify(ctx); err != nil {
		return err
	}
	return a.renew(ctx)
}

// verify fails if a live peer advertising a different shard overlaps this one.
func (a *Advertiser) verify(ctx context.Context) error {
	var leases coordinationv1.LeaseList
	if err := a.client.List(ctx, &leases,
		client.InNamespace(a.cfg.Namespace),
		client.MatchingLabels{GroupLabelKey: a.cfg.Group},
	); err != nil {
		return fmt.Errorf("listing shard advertisements: %w", err)
	}

	for _, lease := range leases.Items {
		if expired(&lease) {
			continue
		}

		raw := lease.Annotations[SelectorAnnotationKey]

		if lease.Labels[IDLabelKey] == a.cfg.Shard.ID() {
			// Pods of our own shard are expected during a rolling update, but only if they really
			// are our shard. A matching ID with a different selector means two distinct shards are
			// colliding on one identity, which would otherwise silently disable this check.
			if raw != a.cfg.Shard.String() {
				return fmt.Errorf(
					"shard %q is already claimed with a different selector %q (held by %q): refusing to start",
					a.cfg.Shard.ID(), raw, holder(&lease),
				)
			}
			continue
		}

		peer, err := Parse(a.cfg.Shard.Key(), raw)
		if err != nil || peer == nil {
			// An unparseable advertisement is not evidence of a conflict, but it does mean some
			// peer is misconfigured.
			if a.cfg.Log != nil {
				a.cfg.Log.Warnw("ignoring unparseable shard advertisement", "lease", lease.Name, "selector", raw)
			}
			continue
		}

		if a.cfg.Shard.Overlaps(peer) {
			return fmt.Errorf(
				"shard selector %q would overlap shard %q (selector %q, held by %q): refusing to start",
				a.cfg.Shard, lease.Labels[IDLabelKey], raw, holder(&lease),
			)
		}
	}

	return nil
}

// renew creates or refreshes this shard's advertisement. Without periodic renewal the Lease goes
// stale and peers correctly start ignoring it, silently disabling the safeguard.
func (a *Advertiser) renew(ctx context.Context) error {
	now := metav1.NewMicroTime(time.Now())
	seconds := int32(a.cfg.LeaseDuration.Seconds())

	var lease coordinationv1.Lease
	key := client.ObjectKey{Namespace: a.cfg.Namespace, Name: a.LeaseName()}
	err := a.client.Get(ctx, key, &lease)

	if apierrors.IsNotFound(err) {
		lease = coordinationv1.Lease{
			ObjectMeta: metav1.ObjectMeta{
				Name:      a.LeaseName(),
				Namespace: a.cfg.Namespace,
			},
		}
		a.stamp(&lease, now, seconds)
		if err := a.client.Create(ctx, &lease); err != nil {
			return fmt.Errorf("creating shard advertisement: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("getting shard advertisement: %w", err)
	}

	a.stamp(&lease, now, seconds)
	if err := a.client.Update(ctx, &lease); err != nil {
		return fmt.Errorf("renewing shard advertisement: %w", err)
	}
	return nil
}

func (a *Advertiser) stamp(lease *coordinationv1.Lease, now metav1.MicroTime, seconds int32) {
	if lease.Labels == nil {
		lease.Labels = map[string]string{}
	}
	lease.Labels[GroupLabelKey] = a.cfg.Group
	lease.Labels[IDLabelKey] = a.cfg.Shard.ID()

	if lease.Annotations == nil {
		lease.Annotations = map[string]string{}
	}
	lease.Annotations[SelectorAnnotationKey] = a.cfg.Shard.String()

	identity := a.cfg.Identity
	lease.Spec.HolderIdentity = &identity
	lease.Spec.RenewTime = &now
	lease.Spec.LeaseDurationSeconds = &seconds
	if lease.Spec.AcquireTime == nil {
		lease.Spec.AcquireTime = &now
	}
}

// Start renews the advertisement until the context is cancelled, satisfying manager.Runnable so the
// manager owns its lifecycle.
func (a *Advertiser) Start(ctx context.Context) error {
	ticker := time.NewTicker(a.cfg.LeaseDuration / 3)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := a.renew(ctx); err != nil && a.cfg.Log != nil {
				a.cfg.Log.Warnw("renewing shard advertisement", "error", err)
			}
		}
	}
}

// NeedLeaderElection reports that every replica advertises, not just the leader: a standby replica
// still holds the shard and must be visible to peers.
func (a *Advertiser) NeedLeaderElection() bool { return false }

func expired(lease *coordinationv1.Lease) bool {
	if lease.Spec.RenewTime == nil {
		return true
	}
	duration := DefaultLeaseDuration
	if lease.Spec.LeaseDurationSeconds != nil {
		duration = time.Duration(*lease.Spec.LeaseDurationSeconds) * time.Second
	}
	return time.Since(lease.Spec.RenewTime.Time) > duration
}

func holder(lease *coordinationv1.Lease) string {
	if lease.Spec.HolderIdentity == nil {
		return "<unknown>"
	}
	return *lease.Spec.HolderIdentity
}

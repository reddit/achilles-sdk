package shard

import (
	"context"
	"fmt"
	"strings"
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

	// ValuesAnnotationKey holds the concrete shard values the advertising instance manages, as a
	// comma-separated list in canonical order.
	ValuesAnnotationKey = "shard.infrared.reddit.com/values"
)

// DefaultLeaseDuration is how long an advertisement stays valid without renewal.
const DefaultLeaseDuration = 30 * time.Second

// Config describes the advertisement this process publishes.
type Config struct {
	// Shard is the set of values this process manages.
	Shard *Shard

	// Group scopes this advertisement to the controller partitioning these types.
	Group Group

	// Instance names this controller instance, i.e. its Deployment. Two instances sharing a name
	// share a leader election lock, so one of them never runs.
	Instance string

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

// Advertiser publishes the shard values this process manages and reports when another instance
// manages one of them too.
//
// Overlap is not an error, because Owner arbitrates ownership over each concrete value and so keeps
// two instances from double-reconciling anything. It is still worth reporting: it means two
// Deployments were configured with the same value, and which of them serves it is then arbitrary
// rather than designed.
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

// LeaseName is the name of this instance's advertisement Lease. Replicas of one instance share it
// and take over its holder identity in turn.
func (a *Advertiser) LeaseName() string {
	return instanceLeaseName(a.cfg.Group.Name, a.cfg.Instance)
}

// Advertise publishes this process's shard values and warns when a peer manages any of them too.
//
// Runs before the manager starts, and therefore against an uncached client: the manager's client is
// backed by a cache whose informers have not synced yet, and caching Leases cluster-wide would be
// wasteful in any case, since the kubelet keeps one per node.
func (a *Advertiser) Advertise(ctx context.Context) error {
	overlap, err := a.Verify(ctx)
	if err != nil {
		return err
	}

	recordOverlap(a.cfg.Group.Name, a.cfg.Shard.ID(), overlap != "")
	if overlap != "" && a.cfg.Log != nil {
		a.cfg.Log.Warnw("another instance of this controller manages one of this instance's shard "+
			"values, so which of them serves it is arbitrary", "overlap", overlap)
	}

	return a.renew(ctx)
}

// Verify inspects live peers, returning a description of any overlap and an error for the one
// misconfiguration that is not self-correcting.
//
// Overlap is merely unintended: Owner arbitrates over each concrete value, so the objects are still
// reconciled exactly once and only the assignment becomes arbitrary. Two pods sharing an instance
// name is different in kind, because the name is the leader election lock: they look to leader
// election like replicas of each other, so pods that disagree about their shard values would have
// one set of values served and the other silently never run. Nothing downstream can recover from
// that, so a disagreement under one name is fatal.
func (a *Advertiser) Verify(ctx context.Context) (string, error) {
	var leases coordinationv1.LeaseList
	if err := a.client.List(ctx, &leases,
		client.InNamespace(a.cfg.Namespace),
		client.MatchingLabels{GroupLabelKey: a.cfg.Group.Name, KindLabelKey: kindAdvertisement},
	); err != nil {
		return "", fmt.Errorf("listing shard advertisements: %w", err)
	}

	overlap := ""
	for i := range leases.Items {
		lease := &leases.Items[i]
		if expired(lease) {
			continue
		}

		raw := lease.Annotations[ValuesAnnotationKey]

		// The group name drops the API group to stay readable, so two controllers partitioning
		// same-named Kinds land in one group and would block each other's values.
		if peerTypes := lease.Annotations[TypesAnnotationKey]; peerTypes != "" && peerTypes != a.cfg.Group.Types {
			return "", fmt.Errorf(
				"shard group %q is already used for the different types %q (held by %q), "+
					"whose values this controller would contend for; the two controllers must not "+
					"share a namespace",
				a.cfg.Group.Name, peerTypes, holder(lease),
			)
		}

		peer, err := Parse(a.cfg.Shard.Key(), strings.Split(raw, ","))
		if err != nil || peer == nil {
			// An unparseable advertisement is not evidence of a conflict, but it does mean some
			// peer is misconfigured.
			if a.cfg.Log != nil {
				a.cfg.Log.Warnw("ignoring unparseable shard advertisement", "lease", lease.Name, "values", raw)
			}
			continue
		}

		if lease.Labels[InstanceLabelKey] == a.cfg.Instance {
			// Replicas of our own instance are expected during a rolling update, but only if they
			// agree about their work. Disagreement under one name means two Deployments were given
			// one name, and so one leader election lock.
			if !a.cfg.Shard.Equal(peer) {
				return "", fmt.Errorf(
					"instance name %q is already advertising the different shard values %q (held by %q): "+
						"pods sharing an instance name share a leader election lock, so only one set "+
						"of values would ever be served; give each instance the name of its own Deployment",
					a.cfg.Instance, raw, holder(lease),
				)
			}
			continue
		}

		if conflict := a.cfg.Shard.Conflict(peer); conflict != "" && overlap == "" {
			overlap = fmt.Sprintf(
				"shard values %q overlap instance %q (values %q, held by %q) on %s",
				a.cfg.Shard, lease.Labels[InstanceLabelKey], raw, holder(lease), conflict,
			)
		}
	}

	return overlap, nil
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
	lease.Labels[GroupLabelKey] = a.cfg.Group.Name
	lease.Labels[InstanceLabelKey] = a.cfg.Instance
	lease.Labels[IDLabelKey] = a.cfg.Shard.ID()
	lease.Labels[KindLabelKey] = kindAdvertisement

	if lease.Annotations == nil {
		lease.Annotations = map[string]string{}
	}
	lease.Annotations[ValuesAnnotationKey] = a.cfg.Shard.String()
	lease.Annotations[TypesAnnotationKey] = a.cfg.Group.Types

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

package shard

import (
	"fmt"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
)

// InstanceLabelKey names the instance holding a Lease. Leader election admits one leader per
// instance, so a Lease bearing our own instance was left by a predecessor of ours.
const InstanceLabelKey = "shard.infrared.reddit.com/instance"

// DefaultLeaseDuration is how long a claimed value stays claimed without renewal. Long enough to
// survive a slow API call, short enough that failover is not noticeable.
const DefaultLeaseDuration = 30 * time.Second

// leasePrefix separates shard Leases from the leader election and kubelet Leases that share the
// namespace, and makes them identifiable in `kubectl get leases`.
const leasePrefix = "achilles-shard"

// releaseTimeout bounds giving up claimed values at shutdown, well inside the manager's default
// 30-second grace period.
const releaseTimeout = 5 * time.Second

// leaseName names the Lease conferring one shard value.
func leaseName(value string) string {
	return fmt.Sprintf("%s-%s", leasePrefix, value)
}

// renewedAt is when a Lease's holder last renewed it, zero when nothing ever has.
//
// Only ever compared against itself to detect renewal. Measuring age from it would trust the
// holder's clock; see observation.
func renewedAt(lease *coordinationv1.Lease) time.Time {
	if lease.Spec.RenewTime == nil {
		return time.Time{}
	}
	return lease.Spec.RenewTime.Time
}

// declaredDuration is how long the holder says its claim lasts.
func declaredDuration(lease *coordinationv1.Lease, fallback time.Duration) time.Duration {
	if lease.Spec.LeaseDurationSeconds == nil {
		return fallback
	}
	return time.Duration(*lease.Spec.LeaseDurationSeconds) * time.Second
}

// heldBy is the identity currently holding a Lease, empty when nothing holds it.
func heldBy(lease *coordinationv1.Lease) string {
	if lease.Spec.HolderIdentity == nil {
		return ""
	}
	return *lease.Spec.HolderIdentity
}

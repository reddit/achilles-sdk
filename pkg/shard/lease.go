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

// expired reports whether a Lease's holder has stopped renewing it.
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

// heldBy is the identity currently holding a Lease, empty when nothing holds it.
func heldBy(lease *coordinationv1.Lease) string {
	if lease.Spec.HolderIdentity == nil {
		return ""
	}
	return *lease.Spec.HolderIdentity
}

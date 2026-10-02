package shard

import (
	"fmt"
	"strings"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	"k8s.io/apimachinery/pkg/util/validation"
)

// InstanceLabelKey names the instance holding a Lease. Leader election admits one leader per
// instance, so a Lease bearing our own instance was left by a predecessor of ours.
const InstanceLabelKey = "shard.infrared.reddit.com/instance"

// DefaultLeaseDuration is how long a claimed value stays claimed without renewal. Long enough to
// survive a slow API call, short enough that failover is not noticeable.
const DefaultLeaseDuration = 30 * time.Second

// releaseTimeout bounds giving up claimed values at shutdown, well inside the manager's default
// 30-second grace period.
const releaseTimeout = 5 * time.Second

// leaseName names the Lease conferring one shard value.
//
// The prefix separates shard Leases from the leader election and kubelet Leases that share the
// namespace, makes them identifiable in `kubectl get leases`, and — because it is per controller —
// keeps two sharded controllers in one namespace off each other's Leases. Instances of one
// controller must share it, or the Lease excludes nobody.
func leaseName(prefix, value string) string {
	return fmt.Sprintf("%s-%s", prefix, value)
}

// ValidateLeasePrefix rejects a prefix that cannot name the Leases for the given values.
//
// Checked against each resulting name rather than against the prefix alone, since the separator
// and the value count towards both the character set and the length ceiling: a prefix that is
// itself a valid object name can still produce one that is not. Checked at startup because at
// runtime an unnameable Lease surfaces only as a value no instance ever owns.
func ValidateLeasePrefix(prefix string, values []string) error {
	for _, value := range values {
		if errs := validation.IsDNS1123Subdomain(leaseName(prefix, value)); len(errs) > 0 {
			return fmt.Errorf("shard lease prefix %q cannot name the Lease for shard value %q: %s",
				prefix, value, strings.Join(errs, "; "))
		}
	}
	return nil
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

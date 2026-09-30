package shard

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PendingRequeueInterval is how long to wait before revisiting an object whose value this instance
// selects but does not yet hold. Long enough that a contested value is not a busy loop, short
// enough that a released value is picked up promptly.
const PendingRequeueInterval = DefaultLeaseDuration / 3

type ownerKey struct{}

// NewContext returns a context carrying the shard Owner, to be passed to manager.Start.
//
// The manager hands its start context to every Reconcile, which makes it the one seam reaching the
// reconciler that stays scoped to a single manager. A package-level owner would be simpler but
// would make two shards untestable in one process, and the SDK's own envtest harness builds
// managers in-process.
func NewContext(ctx context.Context, o *Owner) context.Context {
	return context.WithValue(ctx, ownerKey{}, o)
}

// OwnerFromContext returns the shard Owner, or nil when sharding is disabled.
func OwnerFromContext(ctx context.Context) *Owner {
	o, _ := ctx.Value(ownerKey{}).(*Owner)
	return o
}

// Skip reports whether the object belongs to another instance, and whether it is worth revisiting.
//
// Returns skip=false when sharding is disabled, so an unsharded controller pays one type assertion
// per reconcile and nothing more.
func Skip(ctx context.Context, obj client.Object) (skip, retry bool) {
	o := OwnerFromContext(ctx)
	if o == nil {
		return false, false
	}

	switch o.Ownership(obj) {
	case Owned:
		return false, false
	case NotSelected:
		recordSkip(o.cfg.Group.Name, o.cfg.Shard.ID(), "not-selected")
		return true, false
	default:
		recordSkip(o.cfg.Group.Name, o.cfg.Shard.ID(), "lease-not-held")
		return true, true
	}
}

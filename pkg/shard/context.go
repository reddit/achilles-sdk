package shard

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PendingRequeueInterval is how long a reconciler waits before revisiting an object whose value it
// serves but does not yet hold. A fraction of the lease duration, so a value changing hands is
// picked up promptly.
const PendingRequeueInterval = DefaultLeaseDuration / 3

type ownerKey struct{}

// NewContext carries an Owner on a context.
//
// Reconcilers are built by callers who do not know whether their controller is sharded, so the
// Owner reaches them through the context the manager is started with rather than through the
// signature of every reconciler.
func NewContext(ctx context.Context, o *Owner) context.Context {
	return context.WithValue(ctx, ownerKey{}, o)
}

// Check reports whether this instance may reconcile the object, and Owned when sharding is
// disabled, so that an unsharded controller pays one type assertion per reconcile and nothing more.
func Check(ctx context.Context, obj client.Object) Ownership {
	o := ownerFrom(ctx)
	if o == nil {
		return Owned
	}
	return o.Ownership(obj)
}

// Enabled reports whether this process is sharded, letting a caller skip work it would only need
// in order to answer Check.
func Enabled(ctx context.Context) bool {
	return ownerFrom(ctx) != nil
}

func ownerFrom(ctx context.Context) *Owner {
	o, _ := ctx.Value(ownerKey{}).(*Owner)
	return o
}

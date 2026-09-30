# Sharding a controller

A controller normally reconciles every object of its kind in a cluster: one work queue, one blast
radius. Sharding lets several instances of the same controller run in one cluster, each reconciling
a mutually exclusive slice of those objects.

Each object is assigned to a shard with the `shard.infrared.reddit.com/key` label, and each instance
is told which values it claims:

```
--shard-selector=shard.infrared.reddit.com/key=shard1
```

The selector **claims** values; it does not filter anything. Every instance still watches and caches
every object, and exclusivity is settled at run time: the SDK holds a Lease per concrete shard value
and an instance reconciles an object only while it holds the Lease on that object's value.

That indirection is the whole design. Selectors describe an unbounded space of values, so two
selectors can only ever be *proven* disjoint over the values that exist today — which makes any
selector-only scheme wrong the moment a new value appears. Leases are taken over the values actually
in use, a finite set, so overlapping selectors still divide the objects cleanly.

The label convention and selector grammar follow
[Flux's sharding](https://fluxcd.io/flux/installation/configuration/sharding/); the ownership
mechanism does not.

## Enabling it

Sharding is opt-in per controller, and needs three things beyond the flag.

Declare the root types being partitioned. These are the types whose labels are scanned to discover
the values in use, and whose reconciliation is gated on ownership:

```go
opts := &bootstrap.Options{
	ShardedTypes: []client.Object{&v1alpha1.MyRoot{}},
}
opts.AddToFlags(flags)
```

> A root type left out of this list is reconciled by **every** shard. If your binary registers
> several controllers, every one of their root types belongs here.

Set `--leader-election-id`. The SDK appends the shard to it, so each shard elects a leader among its
own replicas — do **not** vary it per shard yourself. If every shard shared one lock, exactly one
replica across all shards would be active and every other shard's objects would go unreconciled
while its pods stayed healthy.

Set `--leader-election`. Only the leader of a shard claims its values, so without it two replicas of
one shard would both claim and both reconcile. The SDK refuses to start without it.

## Assigning objects to shards

Applying the label is the platform's responsibility, not the SDK's — through `commonMetadata` in a
Kustomization, an admission policy, or whatever creates the objects.

The SDK does propagate the label from a root object onto the children that root manages, so children
always agree with their owner's shard. The value comes from the root rather than from the instance's
own selector, which stays correct when a selector claims several values.

## Always run a catch-all

Objects carrying no shard label form their own partition, with its own Lease. Run exactly one
instance with the negated selector so that partition has an owner:

```
--shard-selector=!shard.infrared.reddit.com/key
```

This works because Kubernetes treats `!=`, `notin`, and `!key` as satisfied by objects that do not
carry the key at all. Note there is no `OR` in the selector grammar, so "my shard or unlabeled"
cannot be written as a single selector.

## New shard values

Nothing to do. A value appearing is discovered on the next ownership sync, its Lease is created, and
whichever instance claims it acquires it. If several instances claim it, one wins and the others
defer — the objects are reconciled exactly once either way.

This is the case a selector-only scheme cannot get right. `notin (0,1)` and `notin (2,3)` look like
a clean split of the values in use, but both claim every value neither excludes, so a new value 4
belongs to both. Arbitrating over the concrete value means exactly one of them serves it.

## Overlapping selectors

Overlap is safe but almost never intended, so the SDK reports it without refusing to run. Each
instance publishes its selector in an advertisement Lease, and warns when a different shard of the
same controller advertises an overlapping one:

```
shard selector overlaps another shard of this controller, so which shard serves the overlapping
values is arbitrary   overlap: selector "shard.infrared.reddit.com/key notin (0,1)" overlaps shard
"not-2-3" (selector "shard.infrared.reddit.com/key notin (2,3)", held by "peer-pod") on objects
carrying no shard label and every shard value except 0, 1, 2, 3
```

Overlap costs you predictability, not correctness: the assignment of the overlapping values to
shards becomes incidental rather than designed. Name the slices you want owned and negate once —
`in (0,1)` paired with `notin (0,1)` is disjoint and total.

The warning is emitted at startup only, and reflects what peers are *running* rather than what their
manifests declare, so it cannot see a peer that crashed before advertising.
`achilles_shard_selector_overlap` carries the same signal as a gauge.

## Shard identifiers

Each shard gets a short identifier derived from the values its selector claims, which names its
advertisement Lease and its leader election lock. It spells the value set out where it can, so
`key in (0,1)` becomes `0-1`, `key notin (0,1)` becomes `not-0-1`, and `!key` becomes `catchall`;
anything without a short DNS-safe rendering falls back to a hash.

Distinct selectors can render to the same identifier, which means they would also share a leader
election lock and one of them would never run. That case is reported as an overlap.

## Adding, changing, or removing a shard

An ordinary rolling update. Values move between shards as Leases are released and acquired, so there
is no drain-then-deploy step and no window in which two instances reconcile the same object.

Removing a shard is the one case needing care: its values are released, and if no remaining selector
claims them, their objects go unreconciled with nothing reporting an error. Always leave a catch-all
running.

## Failure modes to alert on

| Metric | Meaning |
| --- | --- |
| `achilles_shard_values_unowned` | A value in use whose Lease no instance holds. Its objects are reconciled by nobody. The one to page on. |
| `achilles_shard_values_contested` | This instance claims a value another shard holds. Correct but unintended; the selectors overlap. |
| `achilles_shard_selector_overlap` | Another shard advertises an overlapping selector. |
| `achilles_shard_reconciles_skipped_total` | Reconciles skipped for lack of ownership, by reason. |

Mutual exclusion is as strong as Kubernetes leader election and no stronger. Leases carry no fencing
token, so a process stalled past its lease expiry can wake believing it still owns a value.

## What sharding does and does not buy

It distributes reconcile work and shrinks the blast radius of one instance. It does **not** shrink
per-instance memory: every instance caches every object, because ownership is decided after the
object is in hand rather than by filtering it out of the informer.

The ownership sync scans the sharded types on every tick, so cost scales with the number of objects
and with the **cardinality of the shard key** — one Lease per distinct value, each renewed every few
seconds. A handful of values is free; thousands is not. Shard on something low-cardinality.

## Changing an object's shard

Treat the shard label as immutable. If an object moves shards while its old owner still holds the
finalizer, the new owner reconciles it but the finalizer belongs to an instance that no longer owns
it, so deletion can hang with nothing reporting an error. Enforce immutability with a validating
webhook, or ensure nothing relabels live objects.

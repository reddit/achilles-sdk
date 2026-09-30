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

Sharding is opt-in per controller, and needs three things beyond the selector.

**Declare the root types being partitioned.** These are the types whose labels are scanned to
discover the values in use, and whose reconciliation is gated on ownership:

```go
opts := &bootstrap.Options{
	ShardedTypes: []client.Object{&v1alpha1.MyRoot{}},
}
opts.AddToFlags(flags)
```

> A root type left out of this list is reconciled by **every** shard. If your binary registers
> several controllers, every one of their root types belongs here.

**Run each shard as its own Deployment, and give the SDK that Deployment's name.** Every shard needs
its own leader election lock, and the lock is named after this:

```yaml
env:
  - name: INSTANCE_NAME
    valueFrom:
      fieldRef:
        fieldPath: metadata.labels['app']   # must equal the Deployment name
```

`--instance-name` overrides it. The SDK refuses to start without one, and refuses to start if
`--leader-election-id` is also set — the lock has exactly one source of truth.

The Deployment name is used rather than anything derived from the selector because Kubernetes
already guarantees Deployment names are unique in a namespace. Two shards therefore cannot share a
lock, and editing a selector does not rename the lock.

**Set `--leader-election`.** Only the leader of a shard claims its values, so without it two
replicas of one shard would both claim and both reconcile. The SDK refuses to start without it.

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

## Two shards must never share a name

This is the one sharding misconfiguration the SDK still refuses to run on, because it is the only
one nothing downstream can recover from. Leader election compares nothing but the lock name, so two
Deployments given one instance name look to it like ordinary replicas of each other: one wins, the
other stands by without ever starting a reconciler, and its shard's objects go unreconciled while
its pod stays `Running` and passes its probes.

Taking the name from the Deployment makes this near-impossible, but a copied manifest can still get
it wrong, so a peer advertising our instance name with a different selector is fatal:

```
instance name "application-controller-workload-standard" is already advertising the different
selector "shard.infrared.reddit.com/key in (0,1)" (held by "peer-pod"): two instances sharing a
name share a leader election lock, so one shard would never run; give each instance the name of
its own Deployment
```

## Overlapping selectors

Overlap is safe but almost never intended, so the SDK reports it without refusing to run. Each
instance publishes its selector in an advertisement Lease, and warns when a different instance of
the same controller advertises an overlapping one:

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

## What the Leases are called

All of a controller's shard Leases are prefixed with a group derived from the **types it
partitions** — a controller sharding `Application` uses `application-…`. Every shard of that
controller must contend for the same value Leases, while an unrelated controller in the namespace
must not, and "what is being partitioned" is exactly that scope. Nothing needs configuring.

| Lease | Purpose |
| --- | --- |
| `<group>-value-<value>` | Ownership of one concrete shard value |
| `<group>-unlabeled` | Ownership of the objects carrying no shard label |
| `<group>-instance-<instance>` | One instance's advertised selector |
| `<instance>` | Leader election, one per shard |

Values that are not valid object names (uppercase, underscores, overly long) are hashed. Shards also
carry a short readable ID derived from their value set (`0-1`, `not-0-1`, `catchall`, `any`), which
appears as a label and in logs but no longer names anything that must be unique.

## Adding, changing, or removing a shard

An ordinary rolling update. Values move between shards as Leases are released and acquired, so there
is no drain-then-deploy step and no window in which two instances reconcile the same object. Editing
a shard's selector is also a rolling update: the incoming pod is the same instance as the outgoing
one, so it holds the same leader election lock and reclaims its predecessor's values immediately.

Removing a shard is the one case needing care: its values are released, and if no remaining selector
claims them, their objects go unreconciled with nothing reporting an error. Always leave a catch-all
running.

## Failure modes to alert on

| Metric | Meaning |
| --- | --- |
| `achilles_shard_values_unowned` | A value in use whose Lease no instance holds. Its objects are reconciled by nobody. The one to page on. |
| `achilles_shard_values_contested` | This instance claims a value another shard holds. Correct but unintended; the selectors overlap. |
| `achilles_shard_selector_overlap` | Another instance advertises an overlapping selector. |
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

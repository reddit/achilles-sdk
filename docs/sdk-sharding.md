# Sharding a controller

A controller normally reconciles every object of its kind in a cluster: one informer cache holding
everything, one work queue, one blast radius. Sharding lets several instances of the same controller
run in one cluster, each owning a mutually exclusive slice of those objects.

Each object is assigned to a shard with the `shard.infrared.reddit.com/key` label, and each instance
is told which shard it owns:

```
--watch-label-selector=shard.infrared.reddit.com/key=shard1
```

That instance then watches, caches, and reconciles only the objects carrying that label.

The design follows [Flux's sharding](https://fluxcd.io/flux/installation/configuration/sharding/),
including the flag name, so operators familiar with sharded Flux controllers can read an Achilles
deployment without learning a second vocabulary.

## Enabling it

Sharding is opt-in per controller, and needs two things beyond the flag.

Declare the root types whose informers the selector should filter. Without this the selector would
filter nothing, so the SDK refuses to start rather than appearing to shard:

```go
opts := &bootstrap.Options{
	ShardedTypes: []client.Object{&v1alpha1.MyRoot{}},
}
opts.AddToFlags(flags)
```

Set `--leader-election-id`. It names both the per-shard leader election lock and the shard's
advertisement, so the SDK requires it whenever `--watch-label-selector` is set. The SDK appends the
shard to it automatically — do **not** vary it per shard yourself. If every shard shared one lock,
exactly one replica across all shards would be active and the rest would idle.

## Assigning objects to shards

Applying the label is the platform's responsibility, not the SDK's — through `commonMetadata` in a
Kustomization, an admission policy, or whatever creates the objects.

The SDK does propagate the label from a root object onto the children that root manages, so children
always agree with their owner's shard. The value comes from the root rather than from the instance's
own selector, which stays correct when a selector matches several values.

## Exactly one shard may be negated

Negation is how a shard says "everything not claimed by name", so two negated shards always overlap
no matter how their exclusions are written. `notin (0,1)` and `notin (2,3)` look like a clean split
of the values in use, but both own every unlabelled object and every value neither excludes, so the
second one to start is refused:

```
shard selector "shard.infrared.reddit.com/key notin (0,1)" overlaps shard "not-2-3"
(selector "shard.infrared.reddit.com/key notin (2,3)", held by "peer-pod")
on objects carrying no shard label and every shard value except 0, 1, 2, 3: refusing to start
```

Name the slices you want owned and negate once: `in (0,1)` paired with `notin (0,1)` is disjoint and
total. Pairing `in (0,1)` with `in (2,3)` is disjoint but not total, which leaves unlabelled objects
and any future value owned by nobody.

## Always run a catch-all

An object whose label matches no shard is reconciled by nobody, and nothing anywhere reports an
error. Run exactly one instance with the negated selector so unlabeled objects always have an owner:

```
--watch-label-selector=!shard.infrared.reddit.com/key
```

This works because Kubernetes treats `!=`, `notin`, and `!key` as satisfied by objects that do not
carry the key at all. Note there is no `OR` in the selector grammar, so "my shard or unlabeled"
cannot be written as a single selector.

## The overlap safeguard

Two instances whose selectors intersect would both reconcile the same objects and fight over them.
At startup each instance publishes its selector in a Lease and refuses to run if a *different* shard
of the same controller already claims an overlapping slice.

Because every requirement must reference the one shard key, overlap is an exact set intersection
rather than a conservative guess. And because the check keys on shard identity rather than on the
mere presence of a peer, a rolling update of the same shard passes cleanly.

Each shard gets a short identifier derived from the values its selector matches, which names the
Lease and the leader election lock. It spells the value set out where it can, so
`key in (0,1)` becomes `0-1`, `key notin (0,1)` becomes `not-0-1`, and `!key` becomes `catchall`;
anything without a short DNS-safe rendering falls back to a hash. Distinct selectors can render to
the same identifier, so a peer holding your identifier with a *different* selector is also refused.

Two consequences worth knowing:

- The check records what a process is *running*, so it cannot see a peer that crashed before
  advertising. Catching that belongs in a lint over your manifests.
- It runs once, at startup. A conflict is caught by whichever instance arrives second; an instance
  already running never re-evaluates its peers. An advertisement stops being renewed when its
  process exits and is ignored once it expires, but the Lease object itself is left behind, so stale
  advertisements from retired shards accumulate until something prunes them.
- A genuine overlap fails before health probes are served, so the rollout stalls rather than
  crash-looping into service. Renaming a shard is therefore a drain-then-deploy operation, not a
  rolling update.

Pass `--disable-shard-overlap-check` to skip it, which is mainly useful when running a controller
out of cluster against a cluster that has live shards.

## Adding a shard

New shard *values* need no action. A negated shard absorbs every value it does not exclude, including
values that did not exist when it was deployed, so labelling objects with a brand new value simply
leaves them with the negated shard. Nothing is ever orphaned by the appearance of a value.

Adding a new *instance* to own some of those values is the harder direction, because that slice is by
definition already owned by whichever live shard absorbs it. Both halves of the change overlap the
shard still running — the narrowed selector because it still shares every value it did not newly
exclude, and the new shard because its values are exactly the ones being taken away — so neither can
be rolled out underneath it. The incoming pod detects the overlap and refuses to start, and because
that happens before health probes are served, the rollout stalls with the old pod still serving.

Two ways through, and which you want depends on whether you would rather have a gap or a brief
double-reconcile:

- **Stop the shard being carved, then deploy both selectors.** Its slice goes unreconciled until the
  replacements come up. Existing objects are untouched during the gap; only changes go unprocessed.
- **Deploy the new topology with `--disable-shard-overlap-check`, then remove the flag.** Two
  instances briefly reconcile the overlapping slice. For identical builds they render the same
  children and converge on the same result, so the cost is duplicated work and some write conflicts
  rather than divergence — but `status` can flap between them if the builds differ.

The same applies to any change of an existing shard's selector, not just to adding one: widening,
narrowing, and renaming are all drain-then-deploy.

## Rolling it out to an existing controller

Objects created before sharding carry no shard label. Only the root types you declare in
`ShardedTypes` are filtered; children are labelled but their informers are left unfiltered, so
existing children keep working while normal reconciliation backfills their labels. Filtering child
types too is safe only once that backfill is complete.

Leaving child informers unfiltered is safe, not merely convenient: the SDK deletes only objects a
reconciler explicitly enqueues, never objects it merely sees, so one shard can never prune another's
children.

## Changing an object's shard

Treat the shard label as immutable. If an object moves shards while its old owner still holds the
finalizer, that instance stops watching it and the new one does not own the finalizer, so deletion
hangs indefinitely with nothing reporting an error. Enforce immutability with a validating webhook,
or ensure nothing relabels live objects.

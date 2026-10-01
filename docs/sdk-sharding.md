# Sharding

Sharding divides a controller's objects between several instances of that controller, so that one
controller's work can be spread over more than one Deployment.

An object belongs to the shard named by its `shard.infrared.reddit.com/key` label. Each instance is
configured with the concrete values it serves, and reconciles an object only while it holds that
value's Lease.

Values are assigned, never inferred. **Every value in use must be given to some instance.** A value
no instance is given — including the absent value, meaning an object carrying no shard label — is
reconciled by nobody. There is deliberately no catch-all: which instance serves a value is an
operator's decision, so an unassigned value is reported rather than guessed at.

## Enabling it

1. Label the objects. Every object of a partitioned type needs a shard value before sharding is
   turned on, or nothing will be reconciled. See [Objects must carry a shard
   label](#objects-must-carry-a-shard-label).
2. Declare the partitioned types programmatically, since they are types rather than configuration:

   ```go
   opts := &bootstrap.Options{
       Shard: bootstrap.ShardOptions{
           Types: []client.Object{&appv1alpha1.Application{}},
       },
   }
   ```

   Only root types need listing. Objects a reconciler produces inherit their root's shard value, so
   children follow their parent's instance automatically.
3. Give each Deployment its values, and enable leader election:

   ```yaml
   # Deployment A
   - --leader-election
   - --shard-values=0,1
   ```

   ```yaml
   # Deployment B
   - --leader-election
   - --shard-values=2,3
   ```

Leaving `--shard-values` empty disables sharding, and the controller reconciles every object it
manages.

## How exclusion works

Two Leases carry the whole design.

**The leader election Lease**, named after the instance's own workload, admits one active replica
per Deployment. This is controller-runtime's own leader election, which is why
`--leader-election` is required.

**A Lease per shard value**, named `achilles-shard-<value>`, admits one active instance per value.
Claiming it is a conflict-checked write, so two instances configured with the same value cannot
both succeed.

Both are needed because they answer different questions. Configuration says which values an
instance *wants*; the value Lease says which it may *act on*. Configuration can be wrong, and the
Lease is what keeps a mistake from turning into double reconciliation.

The two also reinforce each other. The value Lease is claimable immediately when it bears our own
instance, which would be unsafe if two Deployments could share an instance name — and they cannot,
because the instance name is the name of the pod's own workload, read from its owner chain rather
than configured. Two workloads in a namespace cannot share a name.

Exclusion is as strong as Kubernetes leader election and no stronger. A Lease carries no fencing
token, so a process stalled past its lease expiry can wake up still believing it holds a value.

### What it does and does not buy

Sharding bounds the *write* path: each instance reconciles a slice of the objects. It does not bound
the *read* path. Every instance still watches and caches every object of the partitioned types, so
memory stays proportional to the whole set. Use `Options.Cache.ByObject` to scope informers.

## Assigning objects to shards

Put the value on the object:

```yaml
metadata:
  labels:
    shard.infrared.reddit.com/key: "0"
```

The label must be a valid DNS label, since it names a Lease. Values that are not — uppercase,
underscores, or longer than 63 characters — are rejected at startup rather than mangled, so that the
Lease name stays derivable from the value alone.

Any stable property works as the value: a criticality tier, a region, a hash of the object name. A
mutating policy is the usual way to stamp it, so objects are labelled without their authors knowing
sharding exists.

### Objects must carry a shard label

An object with no shard label, or an empty one, has no value that could be assigned to an instance,
so no instance reconciles it. Empty and absent are treated identically: neither can be configured,
so distinguishing them would only add a partition nobody could claim.

They are not silent. Every instance reports them as ignored under the value `<none>`, and logs a
warning each sync naming the label key.

**This makes labelling a prerequisite rather than a follow-up.** Turning on `--shard-values` over a
fleet whose objects are unlabelled stops everything from being reconciled.

### Changing an object's shard

Editing the label moves the object between instances. The old instance stops reconciling it and the
new one starts, with no coordination between them, since each only acts on values it holds.

If a shard value encodes something mutable, an object can move mid-reconcile: one instance can be
partway through when ownership changes. Reconcilers are idempotent, so the new owner converges —
but a long multi-step reconcile can interleave. Prefer values derived from immutable properties.

## New shard values

A value no instance is configured with is **ignored until an instance is given it**. Nothing claims
it, and nothing reconciles its objects.

This is deliberate. The alternative — having some instance adopt unknown values — makes the
assignment arbitrary and makes a typo in a label silently land real objects on an arbitrary
instance.

Since every instance sees every object, every instance reports every value in use, `0` when it
serves the value and `1` when it does not. A value nobody serves is therefore the one where every
instance reports `1`:

```promql
min by (value) (achilles_shard_values_ignored) == 1
```

Alert on that. It fires for a new value nobody was configured with, and for objects carrying no
label at all under the value `<none>`.

## Overlapping shard values

Two instances configured with the same value is a misconfiguration, but not a correctness problem:
one claims the value's Lease and the other defers, so its objects are still reconciled once. Which
of the two serves it is arbitrary rather than designed.

The deferring instance reports it:

```promql
achilles_shard_values_contested > 0
```

## Adding, changing, or removing a shard

Editing `--shard-values` is an ordinary rolling update. An incoming pod reclaims its predecessor's
values immediately, because the Lease bears its instance name, rather than waiting out the 30-second
expiry.

Moving a value between instances is safe in either order. If the losing instance goes first, the
value is briefly unclaimed and its objects are not reconciled. If the gaining instance goes first,
it is briefly contested and defers. Neither window exceeds the lease duration.

## Metrics

| Metric | Meaning |
| --- | --- |
| `achilles_shard_values_held` | Number of values this instance holds the Lease for. |
| `achilles_shard_values_contested` | Number of values this instance serves but another holds. |
| `achilles_shard_values_ignored` | Per value in use: `1` when this instance does not serve it. |

## Failure modes to alert on

| Symptom | Query | Meaning |
| --- | --- | --- |
| Objects reconciled by nobody | `min by (value) (achilles_shard_values_ignored) == 1` | No instance was given that value, or the objects carry no label. |
| Two instances given one value | `achilles_shard_values_contested > 0` | Overlapping `--shard-values`. Safe, but the assignment is arbitrary. |
| An instance holds nothing | `achilles_shard_values_held == 0` | Its values are all held elsewhere, or it has not synced. |

## Requirements

Each instance reads its own workload to learn its name, and reads and writes the shard Leases:

```yaml
- apiGroups: [""]
  resources: [pods]
  verbs: [get]
- apiGroups: [apps]
  resources: [replicasets]
  verbs: [get]
- apiGroups: [coordination.k8s.io]
  resources: [leases]
  verbs: [get, create, update]
```

`replicasets` is needed because a Deployment interposes a per-revision ReplicaSet between itself and
its pods. Pods of a StatefulSet or DaemonSet need only `pods`.

Leases live in `--leader-election-namespace`, defaulting to the pod's own namespace. Because the
Lease name is derived from the value alone, two *different* sharded controllers sharing a namespace
would contend for the same Leases. Give each sharded controller its own namespace.

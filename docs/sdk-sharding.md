# Sharding a controller

A controller normally reconciles every object of its kind in a cluster: one work queue, one blast
radius. Sharding lets several instances of the same controller run in one cluster, each reconciling
a mutually exclusive slice of those objects.

Each object is assigned to a shard with the `shard.infrared.reddit.com/key` label, and each instance
is told which concrete values it manages:

```
--shard-values=0,1
```

The values **claim** objects; they do not filter anything. Every instance still watches and caches
every object, and exclusivity is settled at run time: the SDK holds a Lease per managed shard value
and an instance reconciles an object only while it holds the Lease on that object's value.

## Enabling it

Sharding is opt-in per controller, and needs three things beyond `--shard-values`.

1. **Declare the GVKs being partitioned.** These are the types whose labels are read to decide
   ownership, and whose reconciliation is gated on it:

   ```go
   opts := &bootstrap.Options{
   	Shard: bootstrap.ShardOptions{
   		Types: []client.Object{&v1alpha1.MyRoot{}},
   	},
   }
   opts.AddToFlags(flags)
   ```

   > A GVK left out of this list is reconciled by **every** instance. If your binary registers
   > several controllers, every one of their root GVKs belongs here.

2. **Run each instance as its own Deployment, and give the SDK that Deployment's name.** Every
   instance needs its own leader election lock, and the lock is named after this:

   ```yaml
   env:
     - name: INSTANCE_NAME
       valueFrom:
         fieldRef:
           fieldPath: metadata.labels['app']   # must equal the Deployment name
   ```

   `--instance-name` overrides it. The SDK refuses to start without one, and refuses to start if
   `--leader-election-id` is also set — the lock has exactly one source of truth.

   The Deployment name is used rather than anything derived from the values because Kubernetes
   already guarantees Deployment names are unique in a namespace. Two instances therefore cannot
   share a lock, and editing `--shard-values` does not rename the lock.

3. **Set `--leader-election`.** Only the leader of an instance claims its values, so without it two
   replicas of one instance would both claim and both reconcile. The SDK refuses to start without
   it.

## Assigning objects to shards

Applying the label is the platform's responsibility, not the SDK's — through `commonMetadata` in a
Kustomization, an admission policy, or whatever creates the objects.

The SDK does propagate the label from a root object onto the children that root manages, so children
always agree with their owner's shard. The value comes from the root rather than from the instance's
own configuration, which stays correct when an instance manages several values.

## Always run a catch-all

Objects carrying no shard label form their own partition, with its own Lease. It is claimed
explicitly, by the reserved value `@unlabeled`, so run exactly one instance with it:

```
--shard-values=@unlabeled
```

Spelled with a leading `@` because Kubernetes label values are alphanumeric with dashes, dots, and
underscores — so the sentinel cannot collide with a real value. An instance may mix it with
ordinary values, e.g. `--shard-values=0,1,@unlabeled`.

## New shard values

New shard values appearing at runtime are **ignored until an instance is configured with them**.
Their objects are reconciled by nobody, and every instance reports them as ignored so this is
visible rather than silent:

```
min by (group, value) (achilles_shard_values_ignored) == 1
```

A value is reported by every instance, `0` when that instance manages it and `1` when it does not,
which is what makes "ignored by *all* instances" answerable without knowing how many are running.

This is deliberate. Which instance should serve a new value is an operator decision — capacity,
blast radius, which tier the value belongs to — so the SDK reports the value and waits rather than
letting whichever instance happens to claim the widest slice absorb it. Assigning it is an edit to
one Deployment's `--shard-values` and a rolling update.

## Two Pods sharing a leader election ID must agree on their shard values

This is the one sharding misconfiguration the SDK refuses to run on, because it is the only one
nothing downstream can recover from. Leader election compares nothing but the lock name, so two
Deployments given one instance name look to it like ordinary replicas of each other: one wins, the
other stands by without ever starting a reconciler, and its values go unreconciled while its pod
stays `Running` and passes its probes.

Taking the name from the Deployment makes this near-impossible, but a copied manifest can still get
it wrong, so a peer advertising our instance name with different shard values is fatal:

```
instance name "application-controller-workload-standard" is already advertising the different
shard values "2,3" (held by "peer-pod"): pods sharing an instance name share a leader election
lock, so only one set of values would ever be served; give each instance the name of its own
Deployment
```

Replicas of one instance that agree are fine, including during a rolling update, and the order the
values are given in does not matter.

## Overlapping shard values

Two instances configured with the same value is safe but almost never intended, so the SDK reports
it without refusing to run. Each instance publishes its values in an advertisement Lease, and warns
when a different instance of the same controller advertises any of ours:

```
another instance of this controller manages one of this instance's shard values, so which of them
serves it is arbitrary   overlap: shard values "0,1" overlap instance "app-controller-critical"
(values "1,2", held by "peer-pod") on the shard value(s) 1
```

Overlap costs you predictability, not correctness: the value's Lease still goes to exactly one
instance, so its objects are reconciled once, but *which* instance serves it is incidental rather
than designed.

The warning is emitted at startup only, and reflects what peers are *running* rather than what their
manifests declare, so it cannot see a peer that crashed before advertising.
`achilles_shard_overlap` carries the same signal as a gauge.

## What the Leases are called

All of a controller's shard Leases are prefixed with a group derived from the **GVKs it
partitions** — a controller sharding `Application` uses `application-…`. Every instance of that
controller must contend for the same value Leases, while an unrelated controller in the namespace
must not, and "what is being partitioned" is exactly that scope. Nothing needs configuring.

| Lease | Purpose |
| --- | --- |
| `<group>-value-<value>` | Ownership of one concrete shard value |
| `<group>-unlabeled` | Ownership of the objects carrying no shard label |
| `<group>-instance-<instance>` | One instance's advertised shard values |
| `<instance>` | Leader election, one per instance |

Values that are not valid object names (uppercase, underscores, overly long) are hashed. Instances
also carry a short readable shard ID derived from their values (`0-1`, `catchall`), which appears as
a label and in logs but does not name anything that must be unique.

## Adding, changing, or removing a shard

An ordinary rolling update. Editing an instance's `--shard-values` is also a rolling update: the
incoming pod is the same instance as the outgoing one, so it holds the same leader election lock and
reclaims its predecessor's values immediately.

Moving a value between two instances means editing both. Order does not matter for correctness — the
value's Lease keeps it reconciled by at most one instance throughout — but removing it from its
current owner first leaves it ignored until the other instance picks it up, while adding it to the
new owner first leaves it briefly contested.

Removing an instance is the case needing care: its values become ignored, and their objects go
unreconciled. Reassign them to a remaining instance in the same change.

## Failure modes to alert on

| Metric | Meaning |
| --- | --- |
| `achilles_shard_values_ignored` | A value in use and whether this instance ignores it. `min by (group, value) (...) == 1` means no instance manages it, so its objects are reconciled by nobody. The one to page on. |
| `achilles_shard_values_contested` | This instance manages a value another instance holds. Correct but unintended; two instances were configured with it. |
| `achilles_shard_overlap` | Another instance manages one of this instance's values. |
| `achilles_shard_values_held` | How many values this instance holds. Zero on a leader means ownership is not being acquired. |

Mutual exclusion is as strong as Kubernetes leader election and no stronger. Leases carry no fencing
token, so a process stalled past its lease expiry can wake believing it still owns a value.

## What sharding does and does not buy

It distributes reconcile work and shrinks the blast radius of one instance. It does **not** shrink
per-instance memory: every instance caches every object, because ownership is decided after the
object is in hand rather than by filtering it out of the informer.

The ownership sync scans the partitioned GVKs on every tick to report ignored values, so cost scales
with the number of objects and with the **cardinality of the shard key** — one Lease per managed
value, each renewed every few seconds, and one metric series per value in use. A handful of values
is free; thousands is not. Shard on something low-cardinality.

## Changing an object's shard

Treat the shard label as immutable. If an object moves shards while its old owner still holds the
finalizer, the new owner reconciles it but the finalizer belongs to an instance that no longer owns
it, so deletion can hang with nothing reporting an error. Enforce immutability with a validating
webhook, or ensure nothing relabels live objects.

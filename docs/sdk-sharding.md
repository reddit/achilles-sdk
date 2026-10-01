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

4. Webhooks—To shard webhooks, you must properly populate the webhook configuration's `objectSelector`.

    ```yaml
    webhooks:
      - name: critical.applications.infrared.reddit.com
        objectSelector:
          matchExpressions:
            - key: shard.infrared.reddit.com/key
              operator: In
              values: ["0", "1"]
        clientConfig:
          service:
            name: application-controller-workload-critical
    ```

    Webhook servers are not leader-gated, so every replica of every instance serves webhook traffic
    and in-process filtering is not an option — the API server calls one endpoint and the Service
    load-balances, so a request for any shard can land on any pod. Routing has to happen at the API
    server, which is what `objectSelector` does. Four things to know before relying on it:
   - **`objectSelector` matches if either the new or the old object matches.** The API server evaluates
     `matchObject(attr.GetObject(), selector) || matchObject(attr.GetOldObject(), selector)`
     ([`predicates/object/matcher.go`](https://github.com/kubernetes/kubernetes/blob/master/staging/src/k8s.io/apiserver/pkg/admission/plugin/webhook/predicates/object/matcher.go#L60)).
     So relabelling an object from one shard to another calls **both** instances' webhooks. Per-shard
     webhooks are not mutually exclusive across a relabel.
   - **On CREATE there is no old object**, so an object whose shard label has not been stamped yet
     matches no sharded webhook. If the label comes from a mutating webhook or a Kyverno policy, do not
     rely on inter-webhook ordering to fix this; require the label on the incoming object instead.
   - **The serving certificate needs SANs for every per-instance Service name**, or a certificate each.
   - **Failure blast radius is split, which is the upside.** With `failurePolicy: Fail`, one instance
     being down blocks writes only to its own shard rather than to the whole type.

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

A Lease carries no fencing token, so exclusion depends on an instance giving a value up on its own.
Two bounds make it do that. An instance that cannot renew a claim keeps acting on the value only
until the claim could have expired, and an instance whose sync loop stops running altogether stops
acting on every value after one lease duration. Both are measured from this process's own clock, so
neither depends on the failing instance noticing anything.

Expiry is judged the same way: from when *this* process saw a Lease, never from the holder's own
`renewTime`. A holder with a skewed clock therefore cannot have its live claim mistaken for an
abandoned one. The cost is that a first sighting proves nothing — see below.

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
it is briefly contested and defers.

How long the gaining instance waits depends on how the value was given up. A value released on
termination, or one whose Lease still bears the gaining instance's own name, is taken immediately.
A value simply abandoned — the holder was killed outright — takes **a full lease duration from the
moment the gaining instance first sees the Lease**, because an instance only ever measures a
Lease's age from its own first sighting. A newly started instance has no history to measure against,
so it waits rather than trust the holder's timestamp.

A terminating instance gives up the values it holds, so an instance newly configured with one picks
it up at once. This is best effort: a process killed outright releases nothing, so **lease expiry is
what actually bounds how long a value stays claimed.** Do not design around prompt release.

## What cannot be sharded

### CRDs

A CRD is a single cluster-scoped object, so every instance serves the same schema. There is no way to
give one instance a different version of a type than another.

That makes the schema a **shared contract across every running version of the controller.** During a
rollout, and for as long as a fleet is partly updated, objects written by version N+1 are read by
version N and vice versa. A schema change is only safe if it holds for every version simultaneously
in the cluster:

- **Add fields, do not repurpose them.** An optional field with a zero-value default is invisible to
  an older version. Changing what an existing field means is not.
- **Do not narrow validation** until every instance has stopped writing the wider form, or the older
  version's writes start being rejected mid-rollout.
- **Removing a field takes two releases**: stop reading it, ship that everywhere, then remove it.
- **A new required field breaks the older version outright**, since it does not know to set it.

This is the ordinary rule for rolling out any schema change, but sharding makes it apply for longer
and across more processes: a fleet of several Deployments is updated one at a time, so mixed versions
are the normal state during a deploy rather than a brief transition.

## Stamp the build version

The SDK stamps `infrared.reddit.com/version` onto every object a reconciler applies, from the value
passed to `meta.InitRedditLabels`. **Set it at compile time.** On a sharded fleet this is the only
cheap way to tell which version last wrote an object, which is exactly what you need when several
instances are mid-rollout.

Declare a variable in `main` and set it with a linker flag:

```go
// main.go
var version string

func main() {
    meta.InitRedditLabels("application-controller", version, "workload")
}
```

```makefile
VERSION ?= $(shell git rev-parse --short HEAD)

build:
	go build -ldflags "-X 'main.version=$(VERSION)'" ./...
```

`-X` only works on a package-level `string` variable that is **not** a constant, which is the usual
mistake. Go also stamps VCS information automatically, readable through
`runtime/debug.ReadBuildInfo()` as the `vcs.revision` setting, but that is empty when building from a
tree without `.git` — which is what a Docker build copying source does — so treat it as a fallback
rather than the mechanism.

Choose the granularity deliberately. The version label goes on **every** managed object, so a
per-commit SHA rewrites all of them on every deploy, whereas a release version only changes when you
release. If you want commit-level precision without that churn, stamp the release version on objects
and expose the SHA separately in a log line or metric.

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

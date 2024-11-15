# Applying Objects

This document describes conventions and patterns around performing updates to Kubernetes resources.

## OutputSet

The `achilles-sdk`'s FSM reconciler supplies an [`OutputSet` abstraction](https://github.com/reddit/achilles-sdk/blob/4fe0f620d71a1a988cd05629df5ea4502b5ff2ea/pkg/fsm/types/output.go#L17)
that should satisfy _most_ use resource update use cases.

The following illustrates the typical pattern:

```golang
var state = &state{
	Name:      "some-state",
	Condition: SomeCondition,
	Transition: func(
		ctx context.Context,
		r types.ReconcilerContext,
		ctrlCtx controlplane.Context,
		object *v1alpha1.SomeObject,
		out *sets.OutputSet,
	) (*state, types.Result) {
		// perform some logic and build output objects
		appProject := &argov1alpha1.AppProject{
			ObjectMeta: ctrl.ObjectMeta{
				Name:      AppProjectName(redditWorkload.Name, redditWorkload.Namespace),
				Namespace: ctrlCtx.ArgoCDNamespace,
			}, 
			Spec: argov1alpha1.AppProjectSpec{
				SourceRepos: []string{
					oci.HelmProdRegistryURL, 
					oci.HelmDevRegistryURL,
				},
				SourceNamespaces: []string{redditWorkload.Namespace}, 
				Destinations:     []argov1alpha1.ApplicationDestination{
					{Name: "*", Namespace: redditWorkload.Namespace},
				},
			},
		}
		
		// apply output objects via output set
		out.Apply(appProject)

		return nextState, types.DoneResult()
	},
}
```

Our hypothetical controller builds an ArgoCD AppProject. The developer then simply passes 
them into the `OutputSet` and the achilles-sdk handles applying those resources and their declared state to the Kubernetes
server.

If the output object does not exist, it is created. If the output object already exists, it is updated to match the
configuration (metadata, spec, and status) constructed by your controller.

The update only includes fields specified by the in-memory copy of the object. If using the SDK's `OutputSet` or `ClientApplicator.Apply(...)` abstractions,
we strongly recommend that you build the object from scratch rather than mutate a copy read from the server. This ensures
that you only update fields that you intend to update (and don't accidentally send data that you don't intend on having your 
logic update). More advanced use cases may require a different "mutation-based" approach, discussed more below.

Critically, on updates, only the fields specified by your code are updated on the server. **If you omit fields
whose values are pointer types, maps, or slices, they will not be updated on the server.** More detailed discussion on
apply semantics in below.


## Detailed Apply Semantics

### Data Serialization

There's a surprising amount of complexity in how clients send resource updates to the kube-apiserver.
Controllers are clients of the Kubernetes API Server. They send CRUD requests to the server to control Kubernetes
configuration. `Create` and `Update` request bodies contain the desired object state. These request bodies
are serialized from the underlying in-memory type to YAML (or JSON) before being sent to the server.

For `Patch` update requests, only the fields that appear in the request body (which is determined by the underyling
implementation's serialization behavior) are updated.

As discussed below, the Achilles SDK models updates in a manner that facilitates multiple actors gracefully managing
mutually exclusive fields on the same objects. This is achieved by controllers selectively serializing only the fields
they manage, and omitting fields they do not manage.

### Achilles SDK Conventions

Achilles controllers (and more generally, all Kubernetes controllers) should honor the following assumptions. Violation
of these assumptions leads to interoperability issues and can cause controllers to overwrite each other's updates.

**Assumption 1: One Owner Per Field**

For all Kubernetes objects that our controllers read and/or write, we must make the assumption that
every **field** in `metadata`, `spec`, and `status` has a single owner. "Owner" in this context refers to a program
or human that mutates the field. This constraint is not bespoke to the Achilles SDK; it is a general recommendation
for all Kubernetes controllers. The kube-apiserver can even enforce this constraint through a feature called 
["server-side-apply"](https://kubernetes.io/docs/reference/using-api/server-side-apply/#field-management). We don't
currently use SSA in the Achilles SDK, but it is a feature that can be integrated in the future. For now, the onus of 
following this convention rests on the controllers that manage a given resource.

Violation of this assumption leads to conflicting updates where two owners fight over the same field, which can lead to
a malfunctioning system.

**Assumption 2: For objects with multiple owners, all fields are pointer types, maps, or slices with the `omitempty` JSON tag**

First, a quick primer on how Go serializes structs to JSON or YAML using the `encoding/json` package:

1. Fields that are scalar types (int, string, bool, etc.) are always serialized to their JSON or YAML representation
    i. There is no way to omit these fields from the serialized output.
2. Fields that are pointer types, maps, or slices and marked with the `omitempty` JSON tag[^1] are serialized to their JSON or YAML representation _only if the field is non-nil_
    i. If the field is nil, it is omitted from the serialized output.
    ii. If the field is empty but non-nil (e.g. `[]` for slices, `{}` for maps and structs), it is serialized to its empty JSON representation (`[]` and `{}` respectively).

Given these serialization mechanics, for objects that have multiple owners acting on mutually exclusive fields, we must ensure that
all `spec` and `status` fields are types that allow omission when serializing from Go types (pointer types, maps, or slices).
This means that actors can send updates while _omitting_ fields that they do not own, thus preventing collisions
with other owners.

Following these two assumptions, we can optimistically apply all object updates without utilizing Kubernetes' [resource version](https://github.snooguts.net/reddit/reddit-helm-charts#versioning)
because there is no risk that any actor's update will conflict with or overwrite that of a different actor's.

We also update all objects using [JSON merge patch semantics](https://kubernetes.io/docs/tasks/manage-kubernetes-objects/update-api-object-kubectl-patch/#use-a-json-merge-patch-to-update-a-deployment),
([RFC here](https://tools.ietf.org/html/rfc7386)), which offers the simplest mental model for merging an update
against existing object state.

**Deleting Fields**

**zero value equivalent to field omission:**

This applies in APIs where a field's zero value (which depends on the type) is semantically equivalent to the field
being absent. In these cases, setting the field to the zero value is equivalent to deleting the field.

Using JSON merge patch semantics, deleting a field requires that the request body contain the field with an empty value
(`0` for numerics, `""` for strings, `false` for bools, `[]` for slices and `{}` for maps or structs).

Assuming your field is a pointer type, map, or slice with the `omitempty` JSON tag, you can delete a field by setting it
to the empty but non-nil value for the given type.

**zero value distinct from field omission:**

In APIs where a field's zero value is not semantically equivalent to the field being absent, deleting the field requires
using an `Update` operation (rather than a `Patch` operation) to overwrite the entire object (same approach
as described below under "Deleting key-value pairs from map types").

## Advanced Apply Patterns

The two assumptions above do not always hold, especially when using 3rd party CRDs whose types you do not control,
that are implemented by controllers whose behavior you don't control.

Use the following workarounds when presented with these convention departures.

**Kubernetes Resource Lock**

Kubernetes CRDs are supplied with a resource lock, tracked by the `metadata.resourceVersion` field.
This field is an integer that represents the latest version of the resource that the server has persisted.
If an update or patch request specifies this field, the kube-apiserver will reject the request if the request's field does not match
the server's field. This guarantees that the client is operating on the latest version of the object.
Full details on the design and implementation [can be found here](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#concurrency-control-and-consistency).

When would you want to use this feature?

The backing Kubernetes client cache supplied by controller-runtime [can serve stale (i.e. outdated) data](https://github.com/kubernetes-sigs/controller-runtime/blob/main/FAQ.md#q-my-cache-might-be-stale-if-i-read-from-a-cache-how-should-i-deal-with-that).
If your logic reads data from the kube-apiserver and then uses that data to update the object, you may inadvertently
update the object with stale data. If sending stale data has negative side effects, you should use the resource lock
to guarantee that stale updates are never sent.

For cases where an external controller is managing some fields in the `spec`, whose types _must_ be serialized (meaning that
you cannot avoid having your controller send some value for fields it doesn't manage because you don't control
the API struct definition and because the field is not a pointer type, map, or slice with the `omitempty` JSON tag),
the workaround is for your controller to perform a "read-modify-write" operation while using the Kubernetes resource lock,
which consists of:

1. read the object from the server
2. modify only the fields that your controller manages
3. write the object back to the server while using the resource lock

Usage of the resource lock (i.e. sending the update request with `metadata.resourceVersion` populated) ensures that
the Kubernetes server will reject the update if it's operating on a version of the object that is out of date.
This guarantees that your controller will not overwrite data managed by other controllers.

To use the resource lock, do the following:

```golang
import "github.com/reddit/achilles-sdk/pkg/io"

out.Apply(obj, io.WithOptimisticLock())
```

**Deleting key-value pairs from map types**

Since we're using JSON merge patch semantics by default, the only way to delete a KV pair from fields whose Go type is a
map is to overwrite the entire map.[^2]

To perform a full object update, supply to `AsUpdate()` apply option like so:


```golang
import "github.com/reddit/achilles-sdk/pkg/io"

out.Apply(obj, io.AsUpdate())
```

If this is a 3rd party CRD, you will likely need to pair the usage of `AsUpdate()` with `WithOptimisticLock()` to avoid
overwriting fields your controller does not manage.

**Custom Management of Owner References**

By default, the FSM reconciler adds an owner reference to all managed resources that links back to the reconciled object.
This enables Kubernetes-native garbage collection, whereby all managed resources will be deleted when the reconciled object
gets deleted. This default behavior makes sense in _most_ controller use cases.

If your controller is intentionally managing owner references, you must disable this feature by using the `io.WithoutOwnerRefs()` 
([link](https://github.com/reddit/achilles-sdk/blob/4fe0f620d71a1a988cd05629df5ea4502b5ff2ea/pkg/io/options.go#L45))
apply option.

### Mutation Based Updates

The Achilles SDK's `ClientApplicator` and `OutputSet` assume a "declarative-style" pattern where the update data is
built from scratch rather than mutated from a copy read from the server. However, there is a scenario where a mutation-based
approach is desirable—you want to minimize the performance cost of your controller updating a field with "stale" (i.e. outdated) data.

The backing Kubernetes client cache supplied by controller-runtime [can serve stale (i.e. outdated) data](https://github.com/kubernetes-sigs/controller-runtime/blob/main/FAQ.md#q-my-cache-might-be-stale-if-i-read-from-a-cache-how-should-i-deal-with-that),
which can lead to extra rounds of reconciliation before your controller converges the managed object into a steady (i.e. unchanging) state.

Ideally your controller must implement idempotent reconciliations and should be tolerant of actuation even if they are
caused by stale data. But if performance becomes a concern, or you wish to optimize your controller to reduce the number
of reconciliations, you can use a mutation-based approach.


```golang
	// Create or Update the deployment default/foo
	deploy := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "default"}}

	// NOTE: there's an analogous method for CreateOrPatch
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, deploy, func() error {
		// Deployment selector is immutable so we set this value only if
		// a new object is going to be created
		if deploy.ObjectMeta.CreationTimestamp.IsZero() {
			deploy.Spec.Selector = &metav1.LabelSelector{
				MatchLabels: map[string]string{"foo": "bar"},
			}
		}

		// update the Deployment pod template
		deploy.Spec.Template = corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					"foo": "bar",
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "busybox",
						Image: "busybox",
					},
				},
			},
		}

		return nil
	})

	if err != nil {
		log.Error(err, "Deployment reconcile failed")
	} else {
		log.Info("Deployment successfully reconciled", "operation", op)
	}
```

The implementation of `CreateOrUpdate` and `CreateOrPatch` is as follows:

1. It performs a read from the server (fronted by the cache, so it can still be stale)
2. It executes the supplied mutation function to mutate the object
3. It sends the update or patch

This implementation reduces the chance that stale data is sent to the server because of the initial read. But importantly,
it does not guarantee that stale data is not written. To have this guarantee, you must supply the Kubernetes resource lock
when sending your update (discussed above under "Kubernetes Resource Lock").

## FAQ

1. What happens if my controller sends an update/patch request with stale data?

Consider the following timeline:

```
t0: Controller A reads object from server
t1: Actor B updates object
t2: Controller A updates object with stale data
```

If the resource lock is used, the kube-apiserver will reject the update at `t2` because the resource version in the
update request does not match the server's resource version.

If the resource lock is not used, the kube-apiserver will accept the update at `t2` and the object will be updated with
stale data. If Actor B responds by updating the object again, the object will be updated with Actor B's data, which will
in turn actuate Controller A. This cycle may repeat until Controller A eventually reads non-stale data and thus doesn't 
overwrite Actor B. Cycles of this kind are essentially livelocks, which should be mitigated by using the resource lock.

If Actor B does not react by updating the object again, then the object will remain in the state that Controller A set it to,
i.e. with stale data. There is no livelock in this scenario but the object is in an incorrect state. This can be mitigated
by using the resource lock.

## References

1. The full list of apply options lives under [`/pkg/io/options.go`](https://github.com/reddit/achilles-sdk/blob/4fe0f620d71a1a988cd05629df5ea4502b5ff2ea/pkg/io/options.go)
2. The client abstraction lives under [`/pkg/io/applicator.go`](https://github.com/reddit/achilles-sdk/blob/4fe0f620d71a1a988cd05629df5ea4502b5ff2ea/pkg/io/applicator.go)

[^1]: Read more about [Go serialization here](https://pkg.go.dev/encoding/json#Marshal)
[^2]: We could theoretically implement or use a custom Go JSON marshaller that can output `key: null` to signal deletion of fields.

# What are Finalizers?

Finalizers is a feature in Kubernetes that prevent the deletion of an object until some conditions are met. Finalizers
alert controllers when an object is being deleted, allowing them to perform cleanup tasks before the object is removed.
More information about how finalizers work is available in the [Kubernetes documentation](https://kubernetes.io/docs/concepts/overview/working-with-objects/finalizers/).

## Finalizers in the Achilles SDK

The Achilles SDK has an optional feature to provide a `finalizerState` when creating a new controller. If a `finalizerState` is provided
the sdk automatically manages adding and removing the finalizer on the object being reconciled. The finalizer is added/updated on every reconcile
as long as the object is not being deleted. Once the object is being deleted (i.e. `metadata.DeletionTimestamp` is set), the sdk will call the
`finalizerState` provided by the controller. The finalizer will only be removed once the `finalizerState` returns a `types.DoneResult()`.
Until the finalizer is removed, the object will not be deleted and the sdk will call the `finalizerState` on every reconcile.

Some examples of what the finalizerState can be used for are:
1. Cleaning up state in remote systems like Vault (e.g. deleting all managed Vault entities)
2. Deleting child Kubernetes objects in a particular order (i.e. deleting Crossplane InstanceProfiles before Roles due to a Crossplane limitation)

Example to add a finalizer to an achilles controller:
```go
builder := fsm.NewBuilder(
  ...
).WithFinalizerState(finalizerState)

var finalizerState = &state{
  Name:      "finalizer",
  Condition: apicommon.Deleting(),
  Transition: func(ctx context.Context, r types.ReconcilerContext, ctrlCtx controlplane.Context, secret *appv1alpha1.RedditSecret, out *types.OutputSet) (*state, types.Result) {
     // cleanup logic
     return nil, types.DoneResult()
  },
}

```
### Finalizer for claim/claimed reconciler

For the claim/claimed reconciler, the sdk automatically adds a finalizer to the `claim` object.
This is to ensure the sdk is able to delete the `claimed` object when the `claim` object is deleted ([ref](https://github.com/reddit/achilles-sdk/blob/4fe0f620d71a1a988cd05629df5ea4502b5ff2ea/pkg/fsm/internal/reconciler_claim.go#L24)).
The behaviour of the finalizer on the `claimed` object is controlled by the `finalizerState` provided to the controller as explained above

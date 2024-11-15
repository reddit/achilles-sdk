# FSM Claim/Claimed Reconciler

This guide walks you through the claim/claimed reconciler pattern available in the `achilles-sdk` and when to use it.

## Background

The `achilles-sdk` in addition to the base [FSM reconciler]({{< ref "dev/sdk/sdk-fsm-reconciler" >}}) also provides a claim/claimed reconciler pattern.
This pattern uses the same FSM semantics from a developer perspective. This pattern is built around two types of resources:
1. **Claim**: A claim resource is created by an end user or client claiming access to a resource. An example of this type of resource is [`RedisClusterClaim`](https://github.snooguts.net/reddit/achilles-redis-controllers/blob/main/api/storage/v1alpha1/redisclusterclaim_type.go)
  which claims access to a `RedisCluster` instance.
2. **Claimed**: A claimed resource captures the actual instance of given object. An example of this type of resource is [`RedisCluster`](https://github.snooguts.net/reddit/achilles-redis-controllers/blob/main/api/storage/v1alpha1/rediscluster_type.go)
  which captures the actual resources running in AWS.

**The `claim` object is namespaced whereas the `claimed` object is cluster-scoped**.


## When to use the claim/claimed reconciler pattern?

It's desirable for the `claim` object to be namespace scoped for one of the following reasons:

1. The `claim` object is end-user facing and you want to ensure that users cannot mutate objects owned by other users by leveraging native Kubernetes RBAC, using the namespace as an isolation unit.
2. Your system organizes resources by namespace. For instance, all configuration pertaining to entity Y belongs in namespace Y.
    1. For example, Compute's `orchestration-controller-manager` controller provisions a namespace for each RedditCluster and places all resources pertaining to that RedditCluster in its own namespace. This allows easy inspection and cleanup of all state pertaining to a given RedditCluster.
3. You want to expose a namespace-scoped CR (the `claim` object) whose implementation (the `claimed` object) requires cluster-scoped resources.
    1. For instance, Storage's namespace-scoped `RedisClusterClaim` CR is implemented using cluster-scoped Crossplane managed resources.
    1. The `claimed` object being cluster scoped allows the `claim` object to manage and "own" both cluster-scoped resources like those exposed by Crossplane
  and namespaced resources. Kubernetes explicitly disallows cross-namespace owner references ([ref](https://kubernetes.io/docs/concepts/overview/working-with-objects/owners-dependents/)).
4. You want to decouple the "request" (the `claim` object) from the "fulfillment" of that request (the `claimed` object), similar to Kubernetes' PVC and PV pattern.
   1. Note (April 17th, 2024) that the `achilles-sdk` hasn't implemented this decoupling pattern yet because no concrete use cases have required it.

The `achilles-sdk` creates two independent reconcilers for the `claim` and `claimed` objects.
The `claim` reconciler is entirely managed by the sdk and does not require any work on the user's part ([ref](https://github.com/reddit/achilles-sdk/blob/main/pkg/fsm/internal/reconciler_claim.go#L43)).
The `claim` reconciler is responsible for creating the `claimed` object if it does not exist and cascading a delete call when the `claim` object is deleted.
The developer is responsible for implementing the `claimed` reconciler using the exposed [FSM semantics]({{< ref "dev/sdk/sdk-fsm-reconciler" >}}).
As an example take a look at the [ASGRotator Controller](https://github.snooguts.net/reddit/achilles/blob/master/orchestration-controller-manager/internal/controllers/cloud-resources/asgrotator/controller.go).

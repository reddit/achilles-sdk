# Metrics and Monitoring

This guide describes the metrics provided by the SDK and how to use them to monitor the health and performance of your
system.

## Controller-runtime Metrics

The Achilles SDK
integrates [controller-runtime metrics](https://github.com/kubernetes-sigs/controller-runtime/blob/1ed345090869edc4bd94fe220386cb7fa5df745f/pkg/internal/controller/metrics/metrics.go).
Controller-runtime metrics provide foundational metrics for understanding the performance and health of your controller.

## SDK Metrics

The Achilles SDK provides additional metrics that leverage SDK conventions and structures to provide more detailed
insights into the health and performance of your controller.

### **`achilles_resource_readiness`**

This metric is a gauge that maps to an Achilles object's status conditions. By default, the SDK instruments metrics for the
status condition of type "Ready". Users can instrument additional status conditions by declaring the following when
building their reconciler:

```golang
WithReconcilerOptions(
	fsmtypes.ReconcilerOptions[v1alpha1.Foobar, *v1alpha1.Foobar]{
		MetricsOptions: fsmtypes.MetricsOptions{
			ConditionTypes: []api.ConditionType{
				// user specifies custom status condition types here
				MyCustomStatusCondition.Type,
			},
		},
	},
)
```

This metric is emitted for each Achilles object, allowing operators to monitor the readiness of each API object
in their system.

The metric has the following labels.
```c
achilles_resource_readiness{
  group="app.infrared.reddit.com",    // the Kubernetes group of the resource
  version="v1alpha1",                 // the Kubernetes version of the resource
  kind="FederatedRedditNamespace",    // the Kubernetes kind of the resource
  name="demo-namespace-1",            // the name of the resource
  namespace="",                       // the namespace of the resource (empty for cluster-scoped CRDs)
  status="True",                      // the status condition's "Status" field
  type="Ready",                       // the status condition's "Type" field
} 1                                   // value of 1 means a status condition of the labelled status and type exists, 0 if it doesn't exist
```

### **`achilles_trigger`**

This metric is a counter that provides insight into the events triggering your controller's reconcilers. It allows operators to reason
about the frequency and types of events that are causing the controller to reconcile. This is typically examined when
looking to reduce the frequency of reconciliations or understand suspected extraneous reconciliations.

For a given reconciler, it is emitted for each (triggering object, event type) pair.

The "type" label indicates the type of triggering object:

1. **"self"** triggers happen by nature of controller-runtime's reconciler model, where any event on the reconciled object 
triggers a reconciliation.
2. **"relative"** triggers occur through events on related objects. Related object triggers are wired up
using the `.Watches()` [builder method](https://github.com/reddit/achilles-sdk/blob/4fe0f620d71a1a988cd05629df5ea4502b5ff2ea/pkg/fsm/builder.go#L134).
3. **"child"** triggers occur through events on managed child objects (objects whose `meta.ownerRef` refers to the reconciled object). Child triggers
are wired up using the `.Manages()` [builder method](https://github.com/reddit/achilles-sdk/blob/4fe0f620d71a1a988cd05629df5ea4502b5ff2ea/pkg/fsm/builder.go#L93)

```c
achilles_trigger{
  controller="ASGRotatorClaim",            // the name of the reconciler
  group="component.infrared.reddit.com",   // the Kubernetes group of the triggering object
  version="v1alpha1",                      // the Kubernetes version of the triggering object
  kind="ASGRotator",                       // the Kubernetes kind of the triggering object
  event="create",                          // the event type, one of "create", "update", "delete"
  reqName="asg-rotator-managed",           // the name of the triggering object
  reqNamespace="dpwfeni-test-usva-aws-1",  // the namespace of the triggering object (empty for cluster-scoped objects)
  type="relative",                         // the trigger type, one of "relative", "self", or "child"
} 13                                       // the number of observed trigger events
```

### **`achilles_object_suspended`**

This metric is a gauge that indicates whether an object is suspended. It is emitted for each reconciled object.
This metric is useful for alerting on any long-suspended objects.

```c
achilles_object_suspended{
  group="app.infrared.reddit.com",         // the Kubernetes group of the reconciled object
  version="v1alpha1",                      // the Kubernetes version of the reconciled object
  kind="FederatedRedditNamespace",         // the Kubernetes kind of the reconciled object
  name="achilles-test-apps",               // the name of the reconciled object
  namespace="",                            // the namespace of the reconciled object (empty for cluster-scoped objects)
} 0                                        // value of 1 means the object is suspended, 0 if it is not
```

### **`achilles_state_duration_seconds`**

This metric is a histogram that provides performance insight into the duration of each state in the FSM. It is emitted
for each (reconciler, state) pair.

```c
achilles_state_duration_seconds_bucket{
  group="app.infrared.reddit.com",      // the Kubernetes group of the reconciled object
  version="v1alpha1",                   // the Kubernetes version of the reconciled object
  kind="FederatedRedditNamespace",      // the Kubernetes kind of the reconciled object
  state="check-federation-refs",        // the name of the FSM state
  le="0.99",                            // the percentile of the histogram distribution
} 183                                   // the duration in milliseconds
```

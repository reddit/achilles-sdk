# FSM Reconciler

This guide walks you through the FSM (finite state machine) controller framework, both the
programming mental model and common controller patterns with code examples.

The goal of the FSM framework is to allow software engineers without extensive
experience building Kubernetes controllers to build correct and conventional declarative APIs, abstracting away
internal controller mechanics to allow the developer to focus on automation business logic.

## Background

For a brief conceptual overview of control-loop theory as it pertains to
Kubernetes, [see this document](https://kubernetes.io/docs/concepts/architecture/controller/).

The FSM framework is built on top of [controller-runtime](https://github.com/kubernetes-sigs/controller-runtime/), a
widely used SDK for building Kubernetes controllers.

Controller-runtime reduces complexity of building controllers through simplifying opinions, the most important ones
being:

- a controller is composed of two main parts, the trigger conditions and the reconciliation logic
    - trigger conditions specify _when_ a control loop actuates and _which_ object gets operated upon
    - reconciliation logic specifies _what_ operations to perform for the object being reconciled (henceforth referred
      to as "parent object")
- each logical control loop is responsible for managing a single resource type (i.e. GVK)
- controller triggers are level-based, not edge-based

Controller-runtime describe some of their opinions and
conventions [here](https://github.com/kubernetes-sigs/controller-runtime/blob/main/pkg/doc.go).

The FSM framework extends controller-runtime with additional opinions and structure that further simplify the process
of building controllers. These will be discussed below.

## Finite State Machine Model

The FSM framework provides additional structure over
controller-runtime's [monolithic "Reconcile()" method](https://github.com/kubernetes-sigs/controller-runtime/blob/dca0be70fd22d5200f37d986ec83450a80295e59/pkg/reconcile/reconcile.go#L93)
by modeling reconciliation as a finite state machine. Furthermore, the finite state machine has the additional
constraint
that **it must be a directed acyclic graph**. Cycles _within a single reconciliation invocation_ are detected and reported as runtime errors. Crucially, this does not prevent the controller from continuously reconciling on a periodic interval (especially for use cases that require polling some upstream system).

Additionally, each reconciliation starts from the FSM's initial state rather than starting from the last reached state.
This implies the following:

- all paths through the FSM graph must be idempotent
- all FSM states must be reachable by observing persisted state available to the controller

These design constraints ensure controller correctness by forcing the developer to write their reconciliation logic in
a manner that is both idempotent and dependent on externally persisted state, rather than state internal to the
controller, which can easily diverge from the actual state of the world. The resulting control loop logic is resilient
to controller restarts and any runtime errors.

## States

Every state in the FSM maps to a [status condition](https://maelvls.dev/kubernetes-conditions/) on the parent object,
which is defined by the
developer [for each state](https://github.snooguts.net/reddit/achilles/blob/c0ddc4dadb6a7613552598da773bd77b80b15c0c/lib/fsm/types/transitions.go#L52)
. If the state completes successfully, the status condition's `Status` field will be set to true. Otherwise, in the case
of a
requeue result or error, the status field will be set to false.

The tracking of states via status condition adheres to Kubernetes API best practices by providing an externally
observable
signal to dependencies of the API. Other actors (programs or humans) can treat the status conditions of FSM-backed APIs
as an authoritative source of truth on its status.

## Transitioning Between States

Each state defines the next state to transition to the current state completes successfully. The next state can vary
based on logical conditions, allowing the expression of branching paths.

Each state
defines [a result type](https://github.snooguts.net/reddit/achilles/blob/c0ddc4dadb6a7613552598da773bd77b80b15c0c/lib/fsm/types/transitions.go#L118-L165)
.
Broadly speaking, there are three types of results:

1. **done**—the state has finished successfully, the reconciler can transition to the next state or, in the case of a
   terminal state, simply complete
2. **requeue**—instructs the reconciler to trigger again after a user-specified amount of time. This is used in cases
   where
   a controller is waiting for an external condition to be fulfilled.
3. **error**—the reconciler logs an error message and will retrigger, the delay of which is subject to exponential
   backoff

A requeue result is typically used over an error result when the external condition is expected to be eventually
consistent, and
thus its retrigger should not be subject to exponential delay.

An error result is used when an external condition is not expected to be false.

## Writing and Updating Managed Resources

The majority of controllers involve creating and updating Kubernetes objects, whether they are CRDs or native resources.
The FSM framework provides
an [output object set abstraction](https://github.snooguts.net/reddit/achilles/blob/c0ddc4dadb6a7613552598da773bd77b80b15c0c/lib/fsm/types/transitions.go#L41)
for ensuring outputs. It provides the following functionality:

- output objects are tracked via the parent object's status
- output objects have their owner references updated with the parent object
    - this provides free garbage collection (i.e. the child objects will be deleted if the parent object is deleted) via
      native Kubernetes garbage collection

## Finalizer States

[Kubernetes finalizers](https://kubernetes.io/docs/concepts/overview/working-with-objects/finalizers/) can be used
to ensure the execution of logic that triggers when an object is deleted. Objects with a finalizer will remain in a
terminating state, but not get deleted from Kubernetes state, until the finalizer is removed.

The FSM provides a way to add a separate FSM triggered upon deletion of
objects, [see this example](https://github.snooguts.net/reddit/achilles/blob/36c3aa3bde5a2590f5d914918a8cefdf1ef953a7/lib/fsm/test/test_fsm_reconciler.go#L39)
. The FSM automatically manages the attachment and removal of the finalizer. The finalizer will only be removed if the
finalizer FSM terminates successfully.

## Trigger Conditions

The FSM exposes the same trigger conditions as controller-runtime.

When building a new controller,
use `.Manages` ([source](https://github.snooguts.net/reddit/achilles/blob/e8f58f6d9a66ab799da21ae9eb1cdc373e56e2d2/lib/fsm/controller.go#L76))
to specify the type of object that is being managed by the controller. Each controller can only manage a single object
type.

The FSM automatically wires up triggers for all [managed resources](##Writing and Updating Managed Resources).

Additional trigger conditions can be wired up for arbitrary events via
the [`.Watches` method](https://github.snooguts.net/reddit/achilles/blob/e8f58f6d9a66ab799da21ae9eb1cdc373e56e2d2/lib/fsm/controller.go#L109)
.

## Example FSM Controllers

See [this simple example](https://github.snooguts.net/reddit/achilles/blob/1499fc7d792c9d717572bea58e85ccb597245bb3/lib/fsm/test/test_fsm_reconciler.go)
for reference on how to implement an FSM controller.

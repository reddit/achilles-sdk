package types

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/reddit/achilles-sdk-api/api"
)

// TransitionFunc is a function that transitions a controller from one internal state to the next. The in ObjectSet contains
// the state of all resources managed by the reconciled object. The out ObjectSet is initialized as a copy of in, and the transition function
// is responsible for mutating the out ObjectSet until it is the intended state of resources that the controller should manage.
// Resources deleted from out are also deleted from the cluster. Out is shared across all states, so changes are visible
// to all subsequent states.
//
// TransitionFunc can return nil as the next state to indicate that the controller has reached a terminal state. Any
// non-nil error aborts the controller.
type TransitionFunc[T client.Object] func(
	ctx context.Context,
	obj T,
	out *OutputSet,
) (next *State[T], result Result)

// State represents a state transition function for the reconciler.
type State[T client.Object] struct {
	// Name is the name of this state. Must be unique across all states.
	Name string
	// Transition transitions the state of the reconciler.
	Transition TransitionFunc[T]
	// Condition is an api.Condition that represents the status for this particular FSM state.
	// Only Type and Message should be set, all other fields are managed by the SDK, including the status, which
	// is set to true (if the state has completed successfully) or false,
	// (indicating the state has not completed successfully and will be retried).
	// The condition Type should be exported so they can be consumed by external systems.
	Condition api.Condition
}

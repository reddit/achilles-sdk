package types

import (
	"fmt"
	"time"

	"go.uber.org/zap"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/reddit/achilles-sdk-api/api"
)

const (
	// DefaultErrorReason is the default status condition reason used for reconciler errors.
	DefaultErrorReason = "InternalError"

	// DefaultRequeueReason is the default status condition reason used for reconciler requeues.
	DefaultRequeueReason = "WaitingForCondition"
)

// Result is the result of executing a state transition function.
// If err is populated, the FSM will terminate and requeue with exponential backoff.
// If requeueAfter is populated, the FSM will terminate and requeue with the specified duration.
// If done is true, the FSM will continue to the next transition function.
// The state's corresponding status condition's status will be False if err or requeueAfter is populated,
// and True if done is true. The status condition's message will be populated with the err or requeueMsg string.
type Result struct {
	Err          error
	Reason       api.ConditionReason
	RequeueMsg   string
	RequeueAfter time.Duration
	Done         bool
}

// Get resolves the Result into controller-runtime's reconcile.Result and error.
// If the result contains an error, the controller will log an error message and requeue with exponential backoff.
// Else if the result contains a requeue message without a specified duration, the controller will log an info message and requeue with exponential backoff.
// Else if the result contains a requeue message with a specified duration, the controller will log an info message and requeue after the specified duration.
// Else, the controller will not requeue.
func (r Result) Get(log *zap.SugaredLogger) (reconcile.Result, error) {
	if r.Err != nil {
		return reconcile.Result{}, r.Err
	} else if r.RequeueMsg != "" {
		// requeue after a fixed delay
		if r.RequeueAfter != 0 {
			log.Infof("%s. requeueing in %s", r.RequeueMsg, r.RequeueAfter)
			return reconcile.Result{
				RequeueAfter: r.RequeueAfter,
			}, nil
		}
		// requeue with exponential backoff
		log.Infof("%s. requeueing with exponential backoff", r.RequeueMsg)
		return reconcile.Result{
			Requeue: true,
		}, nil
	}
	return reconcile.Result{}, nil
}

// GetMessageAndReason returns the message and reason for failed states.
func (r Result) GetMessageAndReason() (string, api.ConditionReason) {
	var message, defaultReason string

	// message
	if r.Err != nil {
		message = r.Err.Error()
		defaultReason = DefaultErrorReason
	} else {
		message = r.RequeueMsg + " (requeued)"
		defaultReason = DefaultRequeueReason
	}

	// reason
	if r.Reason == "" {
		r.Reason = api.ConditionReason(defaultReason)
	}

	return message, r.Reason
}

func (r Result) HasRequeue() bool {
	return r.RequeueAfter != 0
}

// IsDone returns true if the result container neither an error nor a requeue.
func (r Result) IsDone() bool {
	return r.Done
}

// WrapError wraps the result's error with the provided message.
// If the result is not an error result, return the result unmodified.
func (r Result) WrapError(msg string) Result {
	if r.Err == nil {
		return r
	}
	return Result{
		Err: fmt.Errorf("%s: %w", msg, r.Err),
	}
}

// ErrorResultWithReason returns a new error result, which will trigger a requeue with rate-limited backoff.
// err is the error itself and reason is a concise upper camel case string summarizing or categorizing the error
func ErrorResultWithReason(err error, reason string) Result {
	return Result{
		Err:    err,
		Reason: api.ConditionReason(reason),
	}
}

// ErrorResult returns a new error result, which will trigger a requeue with rate-limited backoff.
// The error will be logged and surfaced as a status condition message on the reconciled object.
func ErrorResult(err error) Result {
	return ErrorResultWithReason(err, "")
}

// ErrorResultf is the same as ErrorResult but performs error formatting.
func ErrorResultf(format string, args ...any) Result {
	return ErrorResult(fmt.Errorf(format, args...))
}

// RequeueResultWithReason returns a new requeue result, which will trigger a requeue after the specified duration.
// msg is the requeue log message and reason is a concise upper camel case string summarizing or categorizing the message
func RequeueResultWithReason(msg string, reason string, requeueAfter time.Duration) Result {
	return Result{
		RequeueMsg:   msg,
		RequeueAfter: requeueAfter,
		Reason:       api.ConditionReason(reason),
		Done:         false,
	}
}

// RequeueResult returns a new requeue result, which will trigger a requeue after the specified duration.
// The message will be logged and surfaced as a status condition message on the reconciled object.
func RequeueResult(msg string, requeueAfter time.Duration) Result {
	return RequeueResultWithReason(msg, "", requeueAfter)
}

// RequeueResultWithBackoff returns a new requeue result, which will trigger a requeue with exponential backoff.
func RequeueResultWithBackoff(msg string) Result {
	return RequeueResult(msg, 0)
}

// RequeueResultWithReasonAndBackoff returns a new requeue result, which will trigger a requeue with exponential backoff.
// msg is the requeue log message and reason is a concise upper camel case string summarizing or categorizing the message
func RequeueResultWithReasonAndBackoff(msg, reason string) Result {
	return RequeueResultWithReason(msg, reason, 0)
}

// DoneAndRequeueResult returns a new requeue result, which will trigger a requeue after the specified duration.
func DoneAndRequeueResult(msg string, requeueAfter time.Duration) Result {
	return Result{
		RequeueMsg:   msg,
		RequeueAfter: requeueAfter,
		Reason:       api.ConditionReason(msg),
		Done:         true,
	}
}

// DoneResult returns a new result that signals a completed reconciliation. No retry is queued.
func DoneResult() Result {
	return Result{
		Done: true,
	}
}

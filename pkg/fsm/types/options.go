package types

import (
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/reddit/achilles-sdk-api/api"
	"github.com/reddit/achilles-sdk-api/pkg/types"
)

// ReconcilerOptions are options for tuning the behavior of an FSM reconciler.
type ReconcilerOptions[T any, Obj types.FSMResource[T]] struct {

	// CreateIfNotFound, if true, will create the object when queued for reconciliation but not found.
	CreateIfNotFound bool

	// CreateFunc, if populated, and if CreateIfNotFound is true, will be invoked to create the object when queued for reconciliation but not found.
	// If not populated, the object will be created with its name and namespace (if namespace-scoped) set.
	CreateFunc func(req ctrl.Request) Obj

	// DisableReadyCondition, if true, will disable injection of the status condition of type "Ready" that is otherwise
	// provided by default.
	DisableReadyCondition bool

	// MetricsOptions are options for tuning the metrics instrumentation of this reconciler.
	MetricsOptions MetricsOptions
}

// AchillesMetrics represents various achilles metrics.
type AchillesMetrics string

// String returns AchillesMetrics as a string.
func (a AchillesMetrics) String() string {
	return string(a)
}

// AchillesMetrics type.
const (
	// AchillesResourceReadiness represents if the resource is ready or not.
	AchillesResourceReadiness = "ResourceReadiness"
	// AchillesResourceTrigger trigger for the resource.
	AchillesResourceTrigger = "ResourceTrigger"
	// AchillesResourceCondition condition of the resource see api.ConditionType.
	AchillesResourceCondition = "ResourceCondition"
	// AchillesStateDuration duration of the state.
	AchillesStateDuration = "StateDuration"
	// AchillesSuspend suspend reconciliation
	AchillesSuspend = "ResourceSuspend"
)

// MetricsOptions are options for tuning the metrics instrumentation of this reconciler.
type MetricsOptions struct {
	// ConditionTypes is a list of additional condition types for which to instrument status condition metrics.
	ConditionTypes []api.ConditionType
	// DisableMetrics is a list of metrics to be disabled.
	DisableMetrics []AchillesMetrics
}

// IsMetricDisabled check if metric is disabled for recording.
func (m *MetricsOptions) IsMetricDisabled(metric AchillesMetrics) bool {
	for _, v := range m.DisableMetrics {
		if v == metric {
			return true
		}
	}
	return false
}

// DefaultCreateFunc is the default CreateFunc invoked if CreateFunc is not specified.
func DefaultCreateFunc[T any, Obj types.FSMResource[T]](req ctrl.Request) Obj {
	obj := Obj(new(T))
	obj.SetName(req.Name)
	obj.SetNamespace(req.Namespace)
	return obj
}

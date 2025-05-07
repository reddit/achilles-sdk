package metrics

import (
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// requestWithGeneration is a wrapper around reconcile.Request that adds a Generation field.
type requestWithGeneration struct {
	reconcile.Request
	Generation int64
}

func (r requestWithGeneration) String() string {
	return r.Request.String() + " Generation: " + strconv.FormatInt(r.Generation, 10)
}

// NOTE: the ordering of the []string return by `names()` and `values()` _must_ match

type conditionGaugeLabel struct {
	group         string
	version       string
	kind          string
	name          string
	namespace     string
	conditionType string // label name is "type"
	status        string
}

func (c conditionGaugeLabel) names() []string {
	return []string{
		"group",
		"version",
		"kind",
		"name",
		"namespace",
		"type",
		"status",
	}
}

func (c conditionGaugeLabel) values() []string {
	return []string{
		c.group,
		c.version,
		c.kind,
		c.name,
		c.namespace,
		c.conditionType,
		c.status,
	}
}

type triggerCounterLabel struct {
	group        string
	version      string
	kind         string
	reqName      string
	reqNamespace string
	event        string
	triggerType  string // label name is "type"
	controller   string
}

func (c triggerCounterLabel) names() []string {
	return []string{
		"group",
		"version",
		"kind",
		"reqName",
		"reqNamespace",
		"event",
		"type",
		"controller",
	}
}

func (c triggerCounterLabel) values() []string {
	return []string{
		c.group,
		c.version,
		c.kind,
		c.reqName,
		c.reqNamespace,
		c.event,
		c.triggerType,
		c.controller,
	}
}

// partialValues returns the label values for requested object name, namespace, and controller name.
// used for deleting trigger metrics for requested objects that no longer exist.
func (c triggerCounterLabel) partialValues() prometheus.Labels {
	return prometheus.Labels{
		"reqName":      c.reqName,
		"reqNamespace": c.reqNamespace,
		"controller":   c.controller,
	}
}

type stateDurationHistogramLabel struct {
	group   string
	version string
	kind    string
	state   string
}

func (c stateDurationHistogramLabel) names() []string {
	return []string{
		"group",
		"version",
		"kind",
		"state",
	}
}

func (c stateDurationHistogramLabel) values() []string {
	return []string{
		c.group,
		c.version,
		c.kind,
		c.state,
	}
}

type suspendGaugeLabel struct {
	group     string
	version   string
	kind      string
	name      string
	namespace string
}

func (c suspendGaugeLabel) names() []string {
	return []string{
		"group",
		"version",
		"kind",
		"name",
		"namespace",
	}
}

func (c suspendGaugeLabel) values() []string {
	return []string{
		c.group,
		c.version,
		c.kind,
		c.name,
		c.namespace,
	}
}

type processingDurationHistogramLabel struct {
	group   string
	version string
	kind    string
	success string
}

func (c processingDurationHistogramLabel) names() []string {
	return []string{
		"group",
		"version",
		"kind",
		"success",
	}
}

func (c processingDurationHistogramLabel) values() []string {
	return []string{
		c.group,
		c.version,
		c.kind,
		c.success,
	}
}

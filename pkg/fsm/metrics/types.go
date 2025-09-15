package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

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

type eventCounterLabel struct {
	group        string
	version      string
	kind         string
	objName      string
	objNamespace string
	eventType    string
	reason       string
	controller   string
}

func (c eventCounterLabel) names() []string {
	return []string{
		"group",
		"version",
		"kind",
		"objName",
		"objNamespace",
		"eventType",
		"reason",
		"controller",
	}
}

func (c eventCounterLabel) values() []string {
	return []string{
		c.group,
		c.version,
		c.kind,
		c.objName,
		c.objNamespace,
		c.eventType,
		c.reason,
		c.controller,
	}
}

// partialValues returns the label values for requested object name, namespace, and controller name.
// used for deleting event metrics for requested objects that no longer exist.
func (c eventCounterLabel) partialValues() prometheus.Labels {
	return prometheus.Labels{
		"group":        c.group,
		"version":      c.version,
		"kind":         c.kind,
		"objName":      c.objName,
		"objNamespace": c.objNamespace,
	}
}

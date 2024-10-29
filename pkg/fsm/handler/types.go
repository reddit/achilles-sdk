package handler

// TriggerType is the type of event trigger that actuates a reconciler
type TriggerType string

func (t TriggerType) String() string {
	return string(t)
}

const (
	// TriggerTypeSelf are triggers caused by events on the reconciled object itself
	TriggerTypeSelf TriggerType = "self"

	// TriggerTypeChild are triggers caused by events on objects who have an owner reference to the reconciled object
	TriggerTypeChild TriggerType = "child"

	// TriggerTypeRelative are triggers caused by events on related objects, whose relation is explicitly defined by the programmer.
	TriggerTypeRelative TriggerType = "relative" // TODO Does the family analogy work here? If not, find a better name

	// logger fields

	// fieldNameEvent describes the trigger's action type, one of "create", "update", or "delete
	fieldNameEvent = "event"

	// fieldNameTriggerType describes the trigger's event type, either "self", "child", or "relative"
	fieldNameTriggerType = "type"

	fieldNameTriggerGroup   = "group"
	fieldNameTriggerVersion = "version"
	fieldNameTriggerKind    = "kind"

	fieldNameRequestObjKey    = "request"
	fieldNameRequestName      = "reqName"
	fieldNameRequestNamespace = "reqNamespace"

	triggerMessage = "received trigger"
)

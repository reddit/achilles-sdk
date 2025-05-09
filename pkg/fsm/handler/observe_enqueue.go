package handler

import (
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/reddit/achilles-sdk/pkg/fsm/metrics"
	libmeta "github.com/reddit/achilles-sdk/pkg/meta"
)

var _ predicate.Predicate = &ForObservePredicate{}

// ForObservePredicate is a predicate that observes details about event triggers of type `EnqueueRequestForObject`,
// i.e. events on the object being reconciled itself.
// NOTE: this is the only way to wrap the event handler for a controller-runtime controller's primary reconciled GVK invoked via `builder.For`.
type ForObservePredicate struct {
	log            *zap.SugaredLogger
	scheme         *runtime.Scheme
	controllerName string
	metrics        *metrics.Metrics
}

// NewForObservePredicate returns a new ForObservePredicate that uses the
// supplied logger to debug log details about the event trigger.
func NewForObservePredicate(
	log *zap.SugaredLogger,
	scheme *runtime.Scheme,
	controllerName string,
	metrics *metrics.Metrics,
) *ForObservePredicate {
	return &ForObservePredicate{
		log:            log,
		scheme:         scheme,
		controllerName: controllerName,
		metrics:        metrics,
	}
}

func (p *ForObservePredicate) Create(e event.CreateEvent) bool {
	p.observeEvent("create", e.Object)
	return true
}

func (p *ForObservePredicate) Update(e event.UpdateEvent) bool {
	p.observeEvent("update", e.ObjectNew)
	return true
}

func (p *ForObservePredicate) Delete(e event.DeleteEvent) bool {
	p.observeEvent("delete", e.Object)
	return true
}

func (p *ForObservePredicate) Generic(e event.GenericEvent) bool {
	p.observeEvent("generic", e.Object)
	return true
}

// logs an event trigger
func (p *ForObservePredicate) observeEvent(
	eventType string,
	o client.Object,
) {
	ref := client.ObjectKeyFromObject(o)
	gvk := libmeta.MustGVKForObject(o, p.scheme)
	triggerType := TriggerTypeSelf.String()

	// record trigger metric
	p.metrics.RecordTrigger(
		gvk,
		ref,
		eventType,
		triggerType,
		p.controllerName,
	)

	if eventType == "create" || eventType == "update" {
		// record processing metric start time
		p.markProcessingStartTime(ref, o.GetGeneration(), gvk)
	}

	p.log.
		With(fieldNameRequestObjKey, ref.String()).
		With(fieldNameEvent, eventType).
		With(fieldNameTriggerType, triggerType).
		Debug(triggerMessage)
}

func (p *ForObservePredicate) markProcessingStartTime(ref types.NamespacedName, gen int64, gvk schema.GroupVersionKind) {
	if err := p.metrics.RecordProcessingStart(gvk, reconcile.Request{NamespacedName: ref}, gen); err != nil {
		p.log.Errorf("failed to mark processing start time: %s", err)
	}
}

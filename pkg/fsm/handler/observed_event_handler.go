package handler

import (
	"context"

	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/reddit/achilles-sdk/pkg/fsm/metrics"
	libmeta "github.com/reddit/achilles-sdk/pkg/meta"
)

var _ handler.EventHandler = &ObservedEventHandler{}

// ObservedEventHandler wraps the underlying controller-runtime implementation with logging and metrics.
type ObservedEventHandler struct {
	scheme         *runtime.Scheme
	log            *zap.SugaredLogger
	controllerName string
	metrics        *metrics.Metrics

	// underlying handler that requests get forwarded to
	handler     handler.EventHandler
	triggerType TriggerType
}

type observedQueue struct {
	workqueue.TypedRateLimitingInterface[reconcile.Request]
	handler    *ObservedEventHandler
	eventType  string
	trigger    client.Object
	triggerRef types.NamespacedName
	triggerGVK schema.GroupVersionKind
}

// NewObservedEventHandler creates an ObservedEventHandler
// Due to controller runtime upstream changes, we must pass the triggerType in directly.
// For enqueueRequestForOwner, use TriggerTypeChild
// For EnqueueRequestForObject, use TriggerTypeSelf
// For enqueueRequestsFromMapFunc, use TriggerTypeRelative
func NewObservedEventHandler(
	log *zap.SugaredLogger,
	scheme *runtime.Scheme,
	controllerName string,
	metrics *metrics.Metrics,
	origHandler handler.EventHandler,
	triggerType TriggerType,
) *ObservedEventHandler {
	return &ObservedEventHandler{
		log:            log,
		scheme:         scheme,
		controllerName: controllerName,
		metrics:        metrics,
		handler:        origHandler,
		triggerType:    triggerType,
	}
}

func (h *ObservedEventHandler) Create(ctx context.Context, evt event.CreateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	h.handler.Create(ctx, evt, h.observedQueue("create", evt.Object, q))
}

func (h *ObservedEventHandler) Update(ctx context.Context, evt event.UpdateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	h.handler.Update(ctx, evt, h.observedQueue("update", evt.ObjectNew, q))
}

func (h *ObservedEventHandler) Delete(ctx context.Context, evt event.DeleteEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	h.handler.Delete(ctx, evt, h.observedQueue("delete", evt.Object, q))
}

func (h *ObservedEventHandler) Generic(ctx context.Context, evt event.GenericEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	h.handler.Generic(ctx, evt, h.observedQueue("generic", evt.Object, q))
}

func (h *ObservedEventHandler) observedQueue(
	eventType string,
	o client.Object,
	q workqueue.TypedRateLimitingInterface[reconcile.Request],
) *observedQueue {
	return &observedQueue{
		TypedRateLimitingInterface: q,
		handler:                    h,
		eventType:                  eventType,
		// trigger refers to the object that is triggering the reconciler (which differs from the object being reconciled if ObservedEventHandler.triggerType != TriggerTypeSelf)
		trigger:    o,
		triggerRef: client.ObjectKeyFromObject(o),
		triggerGVK: libmeta.MustGVKForObject(o, h.scheme),
	}
}

func (q *observedQueue) Add(item reconcile.Request) {
	q.observeEventTrigger(item)

	// NOTE: we don't need to mark processing start time for AddRateLimited() or AddAfter() because this metric
	// measures duration from a create or update to the object being reconciled, which will always result in a call to Add()
	q.markProcessingStartTime(q.trigger)

	q.TypedRateLimitingInterface.Add(item)
}

// logs an event trigger
func (q *observedQueue) observeEventTrigger(req reconcile.Request) {
	triggerGVK := q.triggerGVK
	triggerType := q.handler.triggerType.String()

	// record trigger metric
	q.handler.metrics.RecordTrigger(
		triggerGVK,
		req.NamespacedName,
		q.eventType,
		triggerType,
		q.handler.controllerName,
	)

	// log trigger metric
	q.handler.log.
		With(fieldNameRequestObjKey, req.String()).
		With(fieldNameEvent, q.eventType).
		With(fieldNameTriggerType, triggerType).
		With(fieldNameTriggerGroup, triggerGVK.Group).
		With(fieldNameTriggerVersion, triggerGVK.Version).
		With(fieldNameTriggerKind, triggerGVK.Kind).
		With(fieldNameRequestName, req.Name).
		With(fieldNameRequestNamespace, req.Namespace).
		Debug(triggerMessage)
}

func (q *observedQueue) markProcessingStartTime(o client.Object) {
	// the processing duration metric only applies to "self" triggers
	// (triggers resulting from creates or updates on the object being reconciled)
	if q.handler.triggerType != TriggerTypeSelf {
		return
	}

	if err := q.handler.metrics.MarkProcessingStart(
		q.triggerGVK,
		reconcile.Request{NamespacedName: q.triggerRef},
		o.GetGeneration(),
	); err != nil {
		q.handler.log.Errorf("failed to mark processing start time: %s", err)
	}
}

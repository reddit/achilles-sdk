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
	workqueue.RateLimitingInterface
	handler    *ObservedEventHandler
	eventType  string
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

func (h *ObservedEventHandler) Create(ctx context.Context, evt event.CreateEvent, q workqueue.RateLimitingInterface) {
	h.handler.Create(ctx, evt, h.observedQueue("create", evt.Object, q))
}

func (h *ObservedEventHandler) Update(ctx context.Context, evt event.UpdateEvent, q workqueue.RateLimitingInterface) {
	h.handler.Update(ctx, evt, h.observedQueue("update", evt.ObjectNew, q))
}

func (h *ObservedEventHandler) Delete(ctx context.Context, evt event.DeleteEvent, q workqueue.RateLimitingInterface) {
	h.handler.Delete(ctx, evt, h.observedQueue("delete", evt.Object, q))
}

func (h *ObservedEventHandler) Generic(ctx context.Context, evt event.GenericEvent, q workqueue.RateLimitingInterface) {
	h.handler.Generic(ctx, evt, h.observedQueue("generic", evt.Object, q))
}

func (h *ObservedEventHandler) observedQueue(
	eventType string,
	trigger client.Object,
	q workqueue.RateLimitingInterface,
) *observedQueue {
	// trigger client.Object
	return &observedQueue{
		RateLimitingInterface: q,
		handler:               h,
		eventType:             eventType,
		// ref to the object being reconciled (which may differ from the triggering object for owner ref based triggers)
		triggerRef: client.ObjectKeyFromObject(trigger),
		triggerGVK: libmeta.MustGVKForObject(trigger, h.scheme),
	}
}

func (q observedQueue) Add(item interface{}) {
	if req, ok := item.(reconcile.Request); ok {
		q.observeEvent(req)
	}
	q.RateLimitingInterface.Add(item)
}

// logs an event trigger
func (q *observedQueue) observeEvent(req reconcile.Request) {
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

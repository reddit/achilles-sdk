package internal

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/reddit/achilles-sdk-api/api"
	"github.com/reddit/achilles-sdk/pkg/fsm/metrics"
	"github.com/reddit/achilles-sdk/pkg/fsm/types"
	"github.com/reddit/achilles-sdk/pkg/meta"
	"github.com/reddit/achilles-sdk/pkg/status"
)

type conditionedObject interface {
	client.Object
	api.Conditioned
}

// observeFirstReady consumes a persisted snapshot, never uncommitted status computed by an FSM.
// It patches a copy so the caller's reconciliation snapshot (including deletion state) is unchanged.
func observeFirstReady(ctx context.Context, c client.Client, obj conditionedObject, m *metrics.Metrics, log *zap.SugaredLogger) error {
	if !m.IsMetricEnabled(types.AchillesResourceFirstReady) {
		return nil
	}

	if value, exists := obj.GetAnnotations()[meta.FirstReadyAtKey]; exists {
		readyAt, err := time.Parse(time.RFC3339Nano, value)
		if err != nil || readyAt.IsZero() {
			m.DeleteFirstReady(obj)
			log.Warnw("ignoring invalid first-ready timestamp", "object", client.ObjectKeyFromObject(obj), "value", value)
			return nil
		}
		m.RecordFirstReady(obj, readyAt)
		return nil
	}

	// The same name may now belong to a replacement object that has never been ready.
	m.DeleteFirstReady(obj)
	if meta.HasSuspendLabel(obj) || meta.WasDeleted(obj) || !status.ResourceReady(obj) {
		return nil
	}

	readyAt := obj.GetCondition(api.TypeReady).LastTransitionTime.Time
	if readyAt.IsZero() {
		readyAt = time.Now()
	}
	updated := obj.DeepCopyObject().(client.Object)
	annotations := updated.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations[meta.FirstReadyAtKey] = readyAt.UTC().Format(time.RFC3339Nano)
	updated.SetAnnotations(annotations)
	patch := client.MergeFromWithOptions(obj, client.MergeFromWithOptimisticLock{})
	if err := c.Patch(ctx, updated, patch); err != nil {
		return client.IgnoreNotFound(fmt.Errorf("persisting first-ready timestamp: %w", err))
	}
	m.RecordFirstReady(updated, readyAt)
	return nil
}

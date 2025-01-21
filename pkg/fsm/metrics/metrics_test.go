package metrics

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	types2 "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/reddit/achilles-sdk/pkg/fsm/types"
)

func initMetrics(scheme *runtime.Scheme, disableMetrics []types.AchillesMetrics) *Metrics {
	scheme.AddKnownTypes(schema.GroupVersion{
		Group:   "v1.Pod",
		Version: "1.0",
	}, &corev1.Pod{})
	metricOptions := types.MetricsOptions{
		DisableMetrics: disableMetrics,
	}
	return MustMakeMetricsWithOptions(scheme, prometheus.NewRegistry(), metricOptions)
}

func addKnownTypes(scheme *runtime.Scheme, obj client.Object) {
	scheme.AddKnownTypes(schema.GroupVersion{
		Group:   "v1.Pod",
		Version: "1.0",
	}, obj)
}

func runTest(t *testing.T, name string, obj client.Object, expected int, metric *Metrics, metricName string, c prometheus.Collector, testFunc func()) {
	t.Run(name, func(t *testing.T) {
		addKnownTypes(runtime.NewScheme(), obj)
		testFunc()
		count := testutil.CollectAndCount(c, metricName)
		assert.Equal(t, expected, count)
	})
}

func TestRecordTrigger(t *testing.T) {
	scheme := runtime.NewScheme()
	metrics := MustMakeMetrics(scheme, prometheus.NewRegistry())
	metricsDisabled := initMetrics(scheme, []types.AchillesMetrics{types.AchillesResourceTrigger})

	tests := []struct {
		name       string
		obj        client.Object
		expected   int
		metric     *Metrics
		metricName string
		collector  prometheus.Collector
	}{
		{
			name:       "record trigger metric is enabled",
			obj:        &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "default"}},
			expected:   1,
			metric:     metrics,
			metricName: "achilles_trigger",
			collector:  metrics.sink.triggerCounter,
		},
		{
			name:       "record trigger metric is disabled",
			obj:        &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "default"}},
			expected:   0,
			metric:     metricsDisabled,
			metricName: "achilles_trigger",
			collector:  metricsDisabled.sink.triggerCounter,
		},
	}

	for _, tt := range tests {
		runTest(t, tt.name, tt.obj, tt.expected, tt.metric, tt.metricName, tt.collector, func() {
			tt.metric.RecordTrigger(schema.GroupVersionKind{
				Group:   "v1.Pod",
				Version: "1.0",
				Kind:    "v1/Pod",
			}, types2.NamespacedName{
				Namespace: "default",
				Name:      "test-pod",
			}, "xx", "yy", "dd")
		})
	}
}

func TestRecordSuspend(t *testing.T) {
	scheme := runtime.NewScheme()
	metrics := MustMakeMetrics(scheme, prometheus.NewRegistry())
	metricsDisabled := initMetrics(scheme, []types.AchillesMetrics{types.AchillesSuspend})

	tests := []struct {
		name       string
		obj        client.Object
		expected   int
		metric     *Metrics
		metricName string
		collector  prometheus.Collector
	}{
		{
			name:       "suspended metric is enabled",
			obj:        &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "default"}},
			expected:   1,
			metric:     metrics,
			metricName: "achilles_object_suspended",
			collector:  metrics.sink.suspendGauge,
		},
		{
			name:       "suspended metric is disabled",
			obj:        &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "default"}},
			expected:   0,
			metric:     metricsDisabled,
			metricName: "achilles_object_suspended",
			collector:  metricsDisabled.sink.suspendGauge,
		},
	}

	for _, tt := range tests {
		runTest(t, tt.name, tt.obj, tt.expected, tt.metric, tt.metricName, tt.collector, func() {
			tt.metric.RecordSuspend(tt.obj, true)
		})
	}
}

func TestRecordStateDuration(t *testing.T) {
	scheme := runtime.NewScheme()
	metrics := MustMakeMetrics(scheme, prometheus.NewRegistry())
	metricsDisabled := initMetrics(scheme, []types.AchillesMetrics{types.AchillesStateDuration})

	tests := []struct {
		name       string
		obj        client.Object
		expected   int
		metric     *Metrics
		metricName string
		collector  prometheus.Collector
	}{
		{
			name:       "record duration metric is enabled",
			obj:        &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "default"}},
			expected:   1,
			metric:     metrics,
			metricName: "achilles_state_duration_seconds",
			collector:  metrics.sink.stateDurationHistogram,
		},
		{
			name:       "record duration metric is disabled",
			obj:        &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "default"}},
			expected:   0,
			metric:     metricsDisabled,
			metricName: "achilles_state_duration_seconds",
			collector:  metricsDisabled.sink.stateDurationHistogram,
		},
	}

	for _, tt := range tests {
		runTest(t, tt.name, tt.obj, tt.expected, tt.metric, tt.metricName, tt.collector, func() {
			tt.metric.RecordStateDuration(schema.GroupVersionKind{
				Group:   "v1.Pod",
				Version: "1.0",
				Kind:    "v1/Pod",
			}, "xxx", time.Second)
		})
	}
}

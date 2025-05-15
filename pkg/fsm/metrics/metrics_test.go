package metrics

import (
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	io_prometheus_client "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ktypes "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/reddit/achilles-sdk/pkg/fsm/types"
	testv1alpha1 "github.com/reddit/achilles-sdk/pkg/internal/tests/api/test/v1alpha1"
	"github.com/reddit/achilles-sdk/pkg/meta"
)

var scheme = runtime.NewScheme()

func init() {
	corev1.AddToScheme(scheme)
	testv1alpha1.AddToScheme(scheme)
}

func runTest(t *testing.T, name string, expected int, metricName string, c prometheus.Collector, testFunc func()) {
	t.Run(name, func(t *testing.T) {
		testFunc()
		count := testutil.CollectAndCount(c, metricName)
		assert.Equal(t, expected, count)
	})
}

func TestRecordTrigger(t *testing.T) {
	scheme := runtime.NewScheme()
	metrics := MustMakeMetrics(scheme, prometheus.NewRegistry())
	metricsDisabled := MustMakeMetricsWithOptions(scheme, prometheus.NewRegistry(), types.MetricsOptions{DisableMetrics: []types.AchillesMetrics{types.AchillesResourceTrigger}})

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
		runTest(t, tt.name, tt.expected, tt.metricName, tt.collector, func() {
			tt.metric.RecordTrigger(schema.GroupVersionKind{
				Group:   "v1.Pod",
				Version: "1.0",
				Kind:    "v1/Pod",
			}, ktypes.NamespacedName{
				Namespace: "default",
				Name:      "test-pod",
			}, "xx", "yy", "dd")
		})
	}
}

func TestRecordSuspend(t *testing.T) {
	metrics := MustMakeMetrics(scheme, prometheus.NewRegistry())
	metricsDisabled := MustMakeMetricsWithOptions(scheme, prometheus.NewRegistry(), types.MetricsOptions{DisableMetrics: []types.AchillesMetrics{types.AchillesSuspend}})

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
		runTest(t, tt.name, tt.expected, tt.metricName, tt.collector, func() {
			tt.metric.RecordSuspend(tt.obj, true)
		})
	}
}

func TestRecordStateDuration(t *testing.T) {
	metrics := MustMakeMetrics(scheme, prometheus.NewRegistry())
	metricsDisabled := MustMakeMetricsWithOptions(scheme, prometheus.NewRegistry(), types.MetricsOptions{DisableMetrics: []types.AchillesMetrics{types.AchillesStateDuration}})

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
		runTest(t, tt.name, tt.expected, tt.metricName, tt.collector, func() {
			tt.metric.RecordStateDuration(schema.GroupVersionKind{
				Group:   "v1.Pod",
				Version: "1.0",
				Kind:    "v1/Pod",
			}, "xxx", time.Second)
		})
	}
}

func Test_RecordProcessingDuration(t *testing.T) {
	testClaimGVK := meta.MustTypedObjectRefFromObject(&testv1alpha1.TestClaim{}, scheme).GroupVersionKind()
	podGVK := meta.MustTypedObjectRefFromObject(&corev1.Pod{}, scheme).GroupVersionKind()

	type input struct {
		gvk       schema.GroupVersionKind
		name      string
		namespace string
		gen       int64
		// succeeded only applies when used as endInputs
		succeeded bool
	}

	type expectedValue struct {
		labels      map[string]string
		sampleCount uint64
	}

	// startInputs are inputs that call "RecordProcessingStart"
	// endInputs are inputs that call "RecordProcessingDuration"
	tests := []struct {
		name        string
		startInputs []input
		endInputs   []input
		expected    []expectedValue
	}{
		{
			name: "success",
			startInputs: []input{
				// (claim-1, default)
				{
					gvk:       testClaimGVK,
					name:      "claim-1",
					namespace: "default",
					gen:       1,
				},
				{
					gvk:       testClaimGVK,
					name:      "claim-1",
					namespace: "default",
					gen:       2,
				},
				// (claim-2, default)
				{
					gvk:       testClaimGVK,
					name:      "claim-2",
					namespace: "default",
					gen:       1,
				},
				// (pod-1, default)
				{
					gvk:       podGVK,
					name:      "pod-1",
					namespace: "default",
					gen:       1,
				},
				{
					gvk:       podGVK,
					name:      "pod-1",
					namespace: "default",
					gen:       2,
				},
				{
					gvk:       podGVK,
					name:      "pod-1",
					namespace: "default",
					gen:       3,
				},
			},
			endInputs: []input{
				// (claim-1, default)
				{
					gvk:       testClaimGVK,
					name:      "claim-1",
					namespace: "default",
					gen:       2,
					succeeded: true,
				},
				// (claim-2, default)
				{
					gvk:       testClaimGVK,
					name:      "claim-2",
					namespace: "default",
					gen:       1,
					succeeded: true,
				},
				// (pod-1, default)
				{
					gvk:       podGVK,
					name:      "pod-1",
					namespace: "default",
					gen:       3,
					succeeded: true,
				},
			},
			expected: []expectedValue{
				{
					labels: map[string]string{
						"group":   testClaimGVK.Group,
						"version": testClaimGVK.Version,
						"kind":    testClaimGVK.Kind,
						"success": "true",
					},
					sampleCount: 3,
				},
				{
					labels: map[string]string{
						"group":   podGVK.Group,
						"version": podGVK.Version,
						"kind":    podGVK.Kind,
						"success": "true",
					},
					sampleCount: 3,
				},
			},
		},
		{
			name: "failures should not be double counted",
			startInputs: []input{
				// (claim-1, default)
				{
					gvk:       testClaimGVK,
					name:      "claim-1",
					namespace: "default",
					gen:       1,
				},
				{
					gvk:       testClaimGVK,
					name:      "claim-1",
					namespace: "default",
					gen:       2,
				},
				{
					gvk:       testClaimGVK,
					name:      "claim-1",
					namespace: "default",
					gen:       3,
				},
			},
			endInputs: []input{
				// (claim-1, default)
				{
					gvk:       testClaimGVK,
					name:      "claim-1",
					namespace: "default",
					gen:       1,
					succeeded: false,
				},
				{
					gvk:       testClaimGVK,
					name:      "claim-1",
					namespace: "default",
					gen:       2,
					succeeded: false,
				},
				{
					gvk:       testClaimGVK,
					name:      "claim-1",
					namespace: "default",
					gen:       3,
					succeeded: false,
				},
			},
			expected: []expectedValue{
				{
					labels: map[string]string{
						"group":   testClaimGVK.Group,
						"version": testClaimGVK.Version,
						"kind":    testClaimGVK.Kind,
						"success": "false",
					},
					sampleCount: 3,
				},
			},
		},
		{
			name: "mix of sucess and failure",
			startInputs: []input{
				// (claim-1, default)
				{
					gvk:       testClaimGVK,
					name:      "claim-1",
					namespace: "default",
					gen:       1,
				},
				{
					gvk:       testClaimGVK,
					name:      "claim-1",
					namespace: "default",
					gen:       2,
				},
				{
					gvk:       testClaimGVK,
					name:      "claim-1",
					namespace: "default",
					gen:       3,
				},
			},
			endInputs: []input{
				// (claim-1, default)
				{
					// emits 1 data point for failure
					gvk:       testClaimGVK,
					name:      "claim-1",
					namespace: "default",
					gen:       1,
					succeeded: false,
				},
				{
					// emits 2 data points for success
					gvk:       testClaimGVK,
					name:      "claim-1",
					namespace: "default",
					gen:       2,
					succeeded: true,
				},
				{
					// emits 1 data point for success
					gvk:       testClaimGVK,
					name:      "claim-1",
					namespace: "default",
					gen:       3,
					succeeded: true,
				},
				{
					// should not emit any data points
					gvk:       testClaimGVK,
					name:      "claim-1",
					namespace: "default",
					gen:       3,
					succeeded: false,
				},
			},
			expected: []expectedValue{
				{
					labels: map[string]string{
						"group":   testClaimGVK.Group,
						"version": testClaimGVK.Version,
						"kind":    testClaimGVK.Kind,
						"success": "false",
					},
					sampleCount: 1,
				},
				{
					labels: map[string]string{
						"group":   testClaimGVK.Group,
						"version": testClaimGVK.Version,
						"kind":    testClaimGVK.Kind,
						"success": "true",
					},
					sampleCount: 3,
				},
			},
		},
	}

	// returns true iff all key-value pairs in labels are present in metricLabelsMap
	labelsMatch := func(labels []*io_prometheus_client.LabelPair, metricLabelsMap map[string]string) bool {
		for _, label := range labels {
			val, ok := metricLabelsMap[*label.Name]
			if !ok || val != *label.Value {
				return false
			}
		}
		return true
	}

	getHistogramMetric := func(
		family *io_prometheus_client.MetricFamily,
		labels map[string]string,
	) (*io_prometheus_client.Metric, error) {
		// loop through the existing metric objects and select the correct one based on labels
		for _, metric := range family.Metric {
			// for every label-value pair in metric.Label, validate that there is a corresponding key-value pair in metricLabelsMap
			if labelsMatch(metric.Label, labels) {
				return metric, nil
			}
		}
		return nil, errors.New("metric not found")
	}

	for _, tt := range tests {
		reg := prometheus.NewRegistry()
		metrics := MustMakeMetrics(scheme, reg)
		metrics.InitializeForGVK(testClaimGVK)
		metrics.InitializeForGVK(podGVK)

		t.Run(tt.name, func(t *testing.T) {
			for _, input := range tt.startInputs {
				err := metrics.RecordProcessingStart(input.gvk, reconcile.Request{
					NamespacedName: ktypes.NamespacedName{
						Name:      input.name,
						Namespace: input.namespace,
					},
				}, input.gen)
				assert.NoError(t, err)
			}

			for _, input := range tt.endInputs {
				err := metrics.RecordProcessingDuration(input.gvk, reconcile.Request{
					NamespacedName: ktypes.NamespacedName{
						Name:      input.name,
						Namespace: input.namespace,
					},
				}, input.gen, input.succeeded)
				assert.NoError(t, err)
			}

			metricFamilies, err := reg.Gather()
			assert.NoError(t, err)

			var metricFamily *io_prometheus_client.MetricFamily
			// find the desired metric family
			for _, family := range metricFamilies {
				if *family.Name == "achilles_processing_duration_seconds" {
					metricFamily = family
					break
				}
			}

			for _, expected := range tt.expected {
				metric, err := getHistogramMetric(metricFamily, expected.labels)
				assert.NoError(t, err)
				assert.Equal(t, expected.sampleCount, metric.GetHistogram().GetSampleCount())
			}
		})
	}
}

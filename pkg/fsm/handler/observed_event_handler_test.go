package handler_test

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/prometheus/client_golang/prometheus"
	ioprometheusclient "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta/testrestmapper"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	fsmhandler "github.com/reddit/achilles-sdk/pkg/fsm/handler"
	"github.com/reddit/achilles-sdk/pkg/fsm/metrics"
	internalscheme "github.com/reddit/achilles-sdk/pkg/internal/scheme"
	"github.com/reddit/achilles-sdk/pkg/ratelimiter"
)

const controllerName = "test"

type expectedLog struct {
	msg string
	kvs map[string]string
}

func TestObserveEnqueueOwner(t *testing.T) {
	cases := []struct {
		name                      string
		expectedLogs              []expectedLog
		expectedMetricLabelValues [][]*ioprometheusclient.LabelPair
		expectedMetricValues      []*float64
		isController              bool
		o                         client.Object
	}{
		{
			name:         "irrelevant child object with no owners",
			isController: true,
			o:            &corev1.Namespace{},
		},
		{
			name:         "irrelevant child object with owner of irrelevant GVK",
			isController: true,
			o: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "child-namespace",
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion:         "v1",
							Kind:               "Secret",
							Name:               "parent",
							Controller:         ptr.To(true),
							BlockOwnerDeletion: nil,
						},
					},
				},
			},
		},
		{
			name:         "relevant child object",
			isController: true,
			expectedLogs: []expectedLog{
				{
					msg: "received trigger",
					kvs: map[string]string{
						"request":      "/parent",
						"event":        "create",
						"type":         fsmhandler.TriggerTypeChild.String(),
						"group":        "",
						"version":      "v1",
						"kind":         "Namespace",
						"reqName":      "parent",
						"reqNamespace": "",
					},
				},
			},
			expectedMetricLabelValues: [][]*ioprometheusclient.LabelPair{
				{
					newLabelPair("group", ""),
					newLabelPair("version", "v1"),
					newLabelPair("kind", "Namespace"),
					newLabelPair("event", "create"),
					newLabelPair("type", "child"),
					newLabelPair("reqName", "parent"),
					newLabelPair("reqNamespace", ""),
					newLabelPair("controller", controllerName),
				},
			},
			expectedMetricValues: []*float64{ptr.To[float64](1)},
			o: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "child-namespace",
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion:         "v1",
							Kind:               "ConfigMap",
							Name:               "parent",
							Controller:         ptr.To(true),
							BlockOwnerDeletion: nil,
						},
						// this shouldn't trigger the object because IsController=True
						{
							APIVersion:         "v1",
							Kind:               "ConfigMap",
							Name:               "uncle",
							Controller:         ptr.To(false),
							BlockOwnerDeletion: nil,
						},
					},
				},
			},
		},
		{
			name:         "multiple relevant child objects",
			isController: false,
			expectedLogs: []expectedLog{
				{
					msg: "received trigger",
					kvs: map[string]string{
						"request":      "/parent",
						"event":        "create",
						"type":         fsmhandler.TriggerTypeChild.String(),
						"group":        "",
						"version":      "v1",
						"kind":         "Namespace",
						"reqName":      "parent",
						"reqNamespace": "",
					},
				},
				{
					msg: "received trigger",
					kvs: map[string]string{
						"request":      "/uncle",
						"event":        "create",
						"type":         fsmhandler.TriggerTypeChild.String(),
						"group":        "",
						"version":      "v1",
						"kind":         "Namespace",
						"reqName":      "uncle",
						"reqNamespace": "",
					},
				},
			},
			expectedMetricLabelValues: [][]*ioprometheusclient.LabelPair{
				{
					newLabelPair("group", ""),
					newLabelPair("version", "v1"),
					newLabelPair("kind", "Namespace"),
					newLabelPair("event", "create"),
					newLabelPair("type", "child"),
					newLabelPair("reqName", "parent"),
					newLabelPair("reqNamespace", ""),
					newLabelPair("controller", controllerName),
				},
				{
					newLabelPair("group", ""),
					newLabelPair("version", "v1"),
					newLabelPair("kind", "Namespace"),
					newLabelPair("event", "create"),
					newLabelPair("type", "child"),
					newLabelPair("reqName", "uncle"),
					newLabelPair("reqNamespace", ""),
					newLabelPair("controller", controllerName),
				},
			},
			expectedMetricValues: []*float64{
				ptr.To[float64](1),
				ptr.To[float64](1),
			},
			o: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "child-namespace",
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion:         "v1",
							Kind:               "ConfigMap",
							Name:               "parent",
							Controller:         ptr.To(true),
							BlockOwnerDeletion: nil,
						},
						{
							APIVersion:         "v1",
							Kind:               "ConfigMap",
							Name:               "uncle",
							Controller:         ptr.To(false),
							BlockOwnerDeletion: nil,
						},
					},
				},
			},
		},
	}

	scheme, err := internalscheme.NewScheme()
	if err != nil {
		t.Fatalf("constructing scheme: %s", err)
	}

	for _, tc := range cases {
		observedZapCore, observedLogs := observer.New(zap.DebugLevel)
		log := zap.New(observedZapCore).Sugar()
		reg := prometheus.NewRegistry()
		m := metrics.MustMakeMetrics(scheme, reg)

		var h *fsmhandler.ObservedEventHandler
		if tc.isController {
			h = fsmhandler.NewObservedEventHandler(
				log,
				scheme,
				controllerName,
				m,
				handler.EnqueueRequestForOwner(scheme, testrestmapper.TestOnlyStaticRESTMapper(scheme), &corev1.ConfigMap{}, handler.OnlyControllerOwner()),
				fsmhandler.TriggerTypeChild,
			)
		} else {
			h = fsmhandler.NewObservedEventHandler(
				log,
				scheme,
				controllerName,
				m,
				handler.EnqueueRequestForOwner(scheme, testrestmapper.TestOnlyStaticRESTMapper(scheme), &corev1.ConfigMap{}),
				fsmhandler.TriggerTypeChild,
			)
		}

		t.Run(tc.name, func(t *testing.T) {
			queue := workqueue.NewTypedRateLimitingQueue(ratelimiter.NewZeroDelayManagedRateLimiter(ratelimiter.NewGlobal(1)))
			h.Create(context.TODO(), event.CreateEvent{Object: tc.o}, queue)
			assertExpectedLogMessages(t, tc.expectedLogs, observedLogs)
			assertExpectedCounterMetrics(t, reg, tc.expectedMetricLabelValues, tc.expectedMetricValues, "achilles_trigger")
			assert.Equal(t, queue.Len(), len(tc.expectedLogs))
		})
	}
}

func TestObserveEnqueueMapped(t *testing.T) {
	cases := []struct {
		name                      string
		expected                  []expectedLog
		o                         client.Object
		expectedMetricLabelValues [][]*ioprometheusclient.LabelPair
		expectedMetricValues      []*float64
		reqs                      []reconcile.Request
	}{
		{
			name: "should log for each mapped request",
			expected: []expectedLog{
				{
					msg: "received trigger",
					kvs: map[string]string{
						"request":      "namespace-a/name-a",
						"event":        "create",
						"type":         fsmhandler.TriggerTypeRelative.String(),
						"group":        "apps",
						"version":      "v1",
						"kind":         "Deployment",
						"reqName":      "name-a",
						"reqNamespace": "namespace-a",
					},
				},
				{
					msg: "received trigger",
					kvs: map[string]string{
						"request":      "namespace-b/name-b",
						"event":        "create",
						"type":         fsmhandler.TriggerTypeRelative.String(),
						"group":        "apps",
						"version":      "v1",
						"kind":         "Deployment",
						"reqName":      "name-b",
						"reqNamespace": "namespace-b",
					},
				},
			},
			expectedMetricLabelValues: [][]*ioprometheusclient.LabelPair{
				{
					newLabelPair("group", "apps"),
					newLabelPair("version", "v1"),
					newLabelPair("kind", "Deployment"),
					newLabelPair("event", "create"),
					newLabelPair("type", "relative"),
					newLabelPair("reqName", "name-a"),
					newLabelPair("reqNamespace", "namespace-a"),
					newLabelPair("controller", controllerName),
				},
				{
					newLabelPair("group", "apps"),
					newLabelPair("version", "v1"),
					newLabelPair("kind", "Deployment"),
					newLabelPair("event", "create"),
					newLabelPair("type", "relative"),
					newLabelPair("reqName", "name-b"),
					newLabelPair("reqNamespace", "namespace-b"),
					newLabelPair("controller", controllerName),
				},
			},
			expectedMetricValues: []*float64{
				ptr.To[float64](1),
				ptr.To[float64](1),
			},
			o: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "foobar",
					Namespace: "foobar-namespace",
				},
			},
			reqs: []reconcile.Request{
				{
					NamespacedName: types.NamespacedName{
						Namespace: "namespace-a",
						Name:      "name-a",
					},
				},
				{
					NamespacedName: types.NamespacedName{
						Namespace: "namespace-b",
						Name:      "name-b",
					},
				},
			},
		},
	}

	scheme, err := internalscheme.NewScheme()
	if err != nil {
		t.Fatalf("constructing scheme: %s", err)
	}

	for _, tc := range cases {
		observedZapCore, observedLogs := observer.New(zap.DebugLevel)
		log := zap.New(observedZapCore).Sugar()
		reg := prometheus.NewRegistry()
		m := metrics.MustMakeMetrics(scheme, reg)

		h := fsmhandler.NewObservedEventHandler(
			log,
			scheme,
			controllerName,
			m,
			handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, object client.Object) []reconcile.Request { return tc.reqs }),
			fsmhandler.TriggerTypeRelative,
		)

		t.Run(tc.name, func(t *testing.T) {
			queue := workqueue.NewTypedRateLimitingQueue(ratelimiter.NewZeroDelayManagedRateLimiter(ratelimiter.NewGlobal(1)))
			h.Create(context.TODO(), event.CreateEvent{Object: tc.o}, queue)
			assertExpectedLogMessages(t, tc.expected, observedLogs)
			assertExpectedCounterMetrics(t, reg, tc.expectedMetricLabelValues, tc.expectedMetricValues, "achilles_trigger")
			assert.Equal(t, queue.Len(), len(tc.expected))
		})
	}
}

func TestObserveEnqueueObject(t *testing.T) {
	cases := []struct {
		name                      string
		expected                  []expectedLog
		expectedMetricLabelValues [][]*ioprometheusclient.LabelPair
		expectedMetricValues      []*float64
		o                         client.Object
	}{
		{
			name: "object",
			expected: []expectedLog{
				{
					msg: "received trigger",
					kvs: map[string]string{
						"request": "/foobar",
						"event":   "create",
						"type":    fsmhandler.TriggerTypeSelf.String(),
					},
				},
			},
			expectedMetricLabelValues: [][]*ioprometheusclient.LabelPair{
				{
					newLabelPair("group", ""),
					newLabelPair("version", "v1"),
					newLabelPair("kind", "Namespace"),
					newLabelPair("event", "create"),
					newLabelPair("type", "self"),
					newLabelPair("reqName", "foobar"),
					newLabelPair("reqNamespace", ""),
					newLabelPair("controller", controllerName),
				},
			},
			expectedMetricValues: []*float64{
				ptr.To[float64](1),
			},
			o: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "foobar",
				},
			},
		},
	}

	scheme, err := internalscheme.NewScheme()
	if err != nil {
		t.Fatalf("constructing scheme: %s", err)
	}

	for _, tc := range cases {
		observedZapCore, observedLogs := observer.New(zap.DebugLevel)
		log := zap.New(observedZapCore).Sugar()
		reg := prometheus.NewRegistry()
		m := metrics.MustMakeMetrics(scheme, reg)

		h := fsmhandler.NewForObservePredicate(
			log,
			scheme,
			controllerName,
			m,
		)

		t.Run(tc.name, func(t *testing.T) {
			h.Create(event.CreateEvent{Object: tc.o})
			assertExpectedLogMessages(t, tc.expected, observedLogs)
		})
	}
}

func assertExpectedLogMessages(
	t *testing.T,
	expected []expectedLog,
	actualLogs *observer.ObservedLogs,
) {
	if len(expected) != actualLogs.Len() {
		t.Errorf("unexpected number of log messages, got=%d want=%d", actualLogs.Len(), len(expected))
		return
	}

	actualLoggedEntries := actualLogs.All()
	// sort actual and expected logs to ignore ordering in our assertion
	sortLogs(actualLoggedEntries)

	for i, expected := range expected {
		actualLog := actualLoggedEntries[i]
		// assert log messages
		if diff := cmp.Diff(actualLog.Message, expected.msg, cmpopts.SortSlices(func(a, b string) bool { return a < b })); diff != "" {
			t.Errorf("log message did not equal expected. got=%s want=%s", actualLog.Message, expected.msg)
		}

		// assert log kv pairs
		if diff := cmp.Diff(extractLogStringPairs(actualLog.Context), expected.kvs); diff != "" {
			t.Errorf("log key-value pairs did not equal expected. (-got +want):\n%s", diff)
		}
	}
}

func extractLogStringPairs(fields []zapcore.Field) map[string]string {
	m := map[string]string{}
	for _, field := range fields {
		m[field.Key] = field.String
	}
	return m
}

func assertExpectedCounterMetrics(
	t *testing.T,
	reg *prometheus.Registry,
	expectedMetricLabelValues [][]*ioprometheusclient.LabelPair,
	expectedMetricValues []*float64,
	expectedMetricName string,
) {
	metricFamilies, err := reg.Gather()
	if err != nil {
		t.Fatalf("gathering metrics: %s", err.Error())
	}

	var metrics []*ioprometheusclient.Metric
	for _, metricFamily := range metricFamilies {
		if metricFamily != nil && *metricFamily.Name == expectedMetricName {
			metrics = metricFamily.Metric
			break
		}
	}

	if metrics == nil {
		// no expected metrics
		if expectedMetricLabelValues == nil {
			return
		}
		t.Fatalf("expected metric with name %s not found", expectedMetricName)
	}

	if diff := cmp.Diff(len(expectedMetricValues), len(metrics)); diff != "" {
		t.Fatalf("unexpected number of metrics (-got +want):\n%s", diff)
	}

	for i, metric := range metrics {
		expectedValue := expectedMetricValues[i]
		expectedLabelValues := expectedMetricLabelValues[i]

		if diff := cmp.Diff(expectedValue, metric.GetCounter().Value); diff != "" {
			t.Errorf("unexpected metric value (-got +want):\n%s", diff)
		}

		if diff := cmp.Diff(expectedLabelValues, metric.Label, cmpopts.IgnoreUnexported(ioprometheusclient.LabelPair{}), cmpopts.SortSlices(func(a, b *ioprometheusclient.LabelPair) bool { return *a.Name < *b.Name })); diff != "" {
			t.Errorf("unexpected metric value (-got +want):\n%s", diff)
		}
	}
}

func newLabelPair(name, value string) *ioprometheusclient.LabelPair {
	return &ioprometheusclient.LabelPair{
		Name:  ptr.To(name),
		Value: ptr.To(value),
	}
}

func sortLogs(logs []observer.LoggedEntry) {
	sort.Slice(logs, func(i, j int) bool {
		a := logs[i]
		b := logs[j]

		aKey := strings.Join([]string{
			a.Message,
			a.ContextMap()["request"].(string),
			a.ContextMap()["event"].(string),
			a.ContextMap()["type"].(string),
			a.ContextMap()["group"].(string),
			a.ContextMap()["version"].(string),
			a.ContextMap()["kind"].(string),
			a.ContextMap()["reqName"].(string),
			a.ContextMap()["reqNamespace"].(string),
		}, "-")

		bKey := strings.Join([]string{
			b.Message,
			b.ContextMap()["request"].(string),
			b.ContextMap()["event"].(string),
			b.ContextMap()["type"].(string),
			b.ContextMap()["group"].(string),
			b.ContextMap()["version"].(string),
			b.ContextMap()["kind"].(string),
			b.ContextMap()["reqName"].(string),
			b.ContextMap()["reqNamespace"].(string),
		}, "-")

		return aKey < bKey
	})
}

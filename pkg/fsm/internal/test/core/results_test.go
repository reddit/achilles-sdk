package core

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/reddit/achilles-sdk-api/api"
	"github.com/reddit/achilles-sdk/pkg/fsm/metrics"
	"github.com/reddit/achilles-sdk/pkg/fsm/types"
	internalscheme "github.com/reddit/achilles-sdk/pkg/internal/scheme"
	testv1alpha1 "github.com/reddit/achilles-sdk/pkg/internal/tests/api/test/v1alpha1"
	"github.com/reddit/achilles-sdk/pkg/io"
	libratelimiter "github.com/reddit/achilles-sdk/pkg/ratelimiter"
	"github.com/reddit/achilles-sdk/pkg/status"
)

var scheme *runtime.Scheme

func init() {
	scheme = internalscheme.MustNewScheme()
	if err := testv1alpha1.AddToScheme(scheme); err != nil {
		panic(err)
	}
}

func Test_DoneResult(t *testing.T) {
	ctx := context.Background()
	log := zaptest.NewLogger(t).Sugar()
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "foo",
			Annotations: map[string]string{"foo": "bar"},
		},
	}
	testClaim := &testv1alpha1.TestClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-claim",
			Namespace: "default",
		},
		Spec: testv1alpha1.TestClaimSpec{
			ConfigMapName: ptr.To("config-map-name"),
		},
	}

	expectedResult := reconcile.Result{}

	result, _, err := reconcileWithObjects(ctx, log, client.ObjectKeyFromObject(testClaim), ns, testClaim)
	assert.NoError(t, err)
	assert.Equal(t, expectedResult, result)
}

func Test_DoneWithCustomStatusCondition(t *testing.T) {
	tcs := []struct {
		name                   string
		resultType             string
		expectedResult         reconcile.Result
		expectedError          error
		expectedStateCondition api.Condition
		expectedReadyCondition api.Condition
		expectedObjects        []client.Object
	}{
		{
			name:           "done",
			resultType:     "done",
			expectedResult: reconcile.Result{},
			expectedStateCondition: api.Condition{
				Type:    "custom-status-condition",
				Status:  corev1.ConditionTrue,
				Reason:  "default reason",
				Message: "default message",
			},
		},
		{
			name:           "done with custom status condition",
			resultType:     "done-with-status-condition",
			expectedResult: reconcile.Result{},
			expectedStateCondition: api.Condition{
				Type:    "custom-status-condition",
				Status:  corev1.ConditionFalse,
				Reason:  "Test custom reason",
				Message: "Test custom message",
			},
		},
		{
			name:       "done and requeue",
			resultType: "done-and-requeue",
			expectedResult: reconcile.Result{
				RequeueAfter: 5 * time.Second,
			},
			expectedStateCondition: api.Condition{
				Type:    "custom-status-condition",
				Status:  corev1.ConditionTrue,
				Message: "default message",
				Reason:  "default reason",
			},
		},
		{
			name:       "requeue with backoff",
			resultType: "requeue-with-backoff",
			expectedResult: reconcile.Result{
				Requeue: true,
			},
			expectedStateCondition: api.Condition{
				Type:    "custom-status-condition",
				Status:  corev1.ConditionFalse,
				Reason:  types.DefaultRequeueReason,
				Message: "Requeue with backoff (requeued)",
			},
		},
		{
			name:       "requeue with reason",
			resultType: "requeue-with-reason",
			expectedResult: reconcile.Result{
				RequeueAfter: 5 * time.Second,
			},
			expectedStateCondition: api.Condition{
				Type:    "custom-status-condition",
				Status:  corev1.ConditionFalse,
				Reason:  "RequeueWithReason",
				Message: "Requeue with reason (requeued)",
			},
		},
		{
			name:       "requeue with reason and backoff",
			resultType: "requeue-with-reason-and-backoff",
			expectedResult: reconcile.Result{
				Requeue: true,
			},
			expectedStateCondition: api.Condition{
				Type:    "custom-status-condition",
				Status:  corev1.ConditionFalse,
				Reason:  "RequeueWithReasonAndBackoff",
				Message: "Requeue with reason and backoff (requeued)",
			},
		},
		{
			name:          "error",
			resultType:    "error",
			expectedError: errors.New("transitioning state \"custom-status-condition-state-name\": error result"),
			expectedStateCondition: api.Condition{
				Type:    "custom-status-condition",
				Status:  corev1.ConditionFalse,
				Reason:  types.DefaultErrorReason,
				Message: "error result",
			},
		},
		{
			name:          "error with reason",
			resultType:    "error-with-reason",
			expectedError: errors.New("transitioning state \"custom-status-condition-state-name\": error result"),
			expectedStateCondition: api.Condition{
				Type:    "custom-status-condition",
				Status:  corev1.ConditionFalse,
				Reason:  "ErrorReason",
				Message: "error result",
			},
		},
		{
			name:       "requeue after completion with backoff",
			resultType: "requeue-after-completion-with-backoff",
			expectedResult: reconcile.Result{
				Requeue: true,
			},
			expectedStateCondition: api.Condition{
				Type:    "custom-status-condition",
				Status:  corev1.ConditionFalse,
				Message: "Done and requeue after completion with backoff (requeued)",
				Reason:  types.DefaultRequeueReason,
			},
			expectedReadyCondition: api.Condition{
				Type:    api.TypeReady,
				Status:  corev1.ConditionFalse,
				Reason:  status.ReasonFailure,
				Message: "Non-successful conditions: custom-status-condition",
			},
			expectedObjects: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name: "noop-state-for-default-test-claim",
					},
				},
			},
		},
		{
			name:       "requeue after completion",
			resultType: "requeue-after-completion",
			expectedResult: reconcile.Result{
				RequeueAfter: 30 * time.Second,
			},
			expectedStateCondition: api.Condition{
				Type:    "custom-status-condition",
				Status:  corev1.ConditionFalse,
				Message: "Done and requeue after completion (requeued)",
				Reason:  types.DefaultRequeueReason,
			},
			expectedReadyCondition: api.Condition{
				Type:    api.TypeReady,
				Status:  corev1.ConditionFalse,
				Reason:  status.ReasonFailure,
				Message: "Non-successful conditions: custom-status-condition",
			},
			expectedObjects: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name: "noop-state-for-default-test-claim",
					},
				},
			},
		},
	}

	for _, tc := range tcs {
		ctx := context.Background()
		log := zaptest.NewLogger(t).Sugar()
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "foo",
				Annotations: map[string]string{"foo": "bar"},
			},
		}
		testClaim := &testv1alpha1.TestClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "test-claim",
				Namespace:   "default",
				Annotations: map[string]string{"result-type": tc.resultType},
			},
			Spec: testv1alpha1.TestClaimSpec{
				ConfigMapName: ptr.To("config-map-name"),
			},
		}

		result, fakeC, err := reconcileWithObjects(ctx, log, client.ObjectKeyFromObject(testClaim), ns, testClaim)
		if tc.expectedError != nil {
			require.Error(t, err, "Test case %q failed: expected error but got none", tc.name)
			require.EqualError(t, err, tc.expectedError.Error(), "Test case %q failed: unexpected error message", tc.name)
		} else {
			require.NoError(t, err, "Test case %q failed: unexpected error during reconciliation", tc.name)
			require.Equal(t, tc.expectedResult, result, "Test case %q failed: expected result mismatch", tc.name)
		}

		if err := fakeC.Get(ctx, client.ObjectKeyFromObject(testClaim), testClaim); err != nil {
			t.Errorf("getting test claim: %v", err)
		}

		// assert state condition
		assertConditionExists(t, tc.name, testClaim, tc.expectedStateCondition)

		// assert ready condition if expectation defined
		if tc.expectedReadyCondition != (api.Condition{}) {
			assertConditionExists(t, tc.name, testClaim, tc.expectedReadyCondition)
		}

		// assert expected objects if expectation defined
		if len(tc.expectedObjects) > 0 {
			for _, obj := range tc.expectedObjects {
				if err := fakeC.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
					t.Errorf("getting expected object %q: %v", client.ObjectKeyFromObject(obj), err)
				}
			}
		}
	}
}

func assertConditionExists(
	t *testing.T,
	testName string,
	claim *testv1alpha1.TestClaim,
	expectedCondition api.Condition,
) {
	var actualStateCondition []api.Condition
	for _, condition := range claim.Status.Conditions {
		// accumulate status conditions with type == "done-with-status-condition"
		if condition.Type == expectedCondition.Type {
			actualStateCondition = append(actualStateCondition, condition)
		}
	}
	require.Len(t, actualStateCondition, 1, "Test case %q failed: expected only one status condition with type %q", testName, expectedCondition.Type)

	if diff := cmp.Diff(expectedCondition, actualStateCondition[0], cmpopts.IgnoreFields(api.Condition{}, "LastTransitionTime")); diff != "" {
		t.Errorf("Test case %q failed: status condition mismatch (-want +got):\n%s", testName, diff)
	}
}

func Test_RequeueResultWithReason(t *testing.T) {
	ctx := context.Background()
	log := zaptest.NewLogger(t).Sugar()
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "foo",
		},
	}
	testClaim := &testv1alpha1.TestClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-claim",
			Namespace: "default",
		},
		Spec: testv1alpha1.TestClaimSpec{
			ConfigMapName: ptr.To("config-map-name"),
		},
	}

	expectedResult := reconcile.Result{
		RequeueAfter: 5 * time.Second,
	}

	result, _, err := reconcileWithObjects(ctx, log, client.ObjectKeyFromObject(testClaim), ns, testClaim)
	assert.NoError(t, err)
	assert.Equal(t, expectedResult, result)
}

func reconcileWithObjects(
	ctx context.Context,
	log *zap.SugaredLogger,
	req client.ObjectKey,
	objs ...client.Object,
) (reconcile.Result, client.Client, error) {
	fakeC := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(objs...).
		Build()

	r, err := initReconciler(log, fakeC)
	if err != nil {
		return reconcile.Result{}, nil, fmt.Errorf("initializing reconciler: %v", err)
	}

	result, err := r.Reconcile(ctx, reconcile.Request{
		NamespacedName: req,
	})
	return result, fakeC, err
}

func initReconciler(log *zap.SugaredLogger, fakeC client.Client) (reconcile.Reconciler, error) {
	mgr, err := manager.New(&rest.Config{}, manager.Options{
		Scheme: scheme,

		NewClient: func(config *rest.Config, options client.Options) (client.Client, error) {
			return fakeC, nil
		},
	})
	if err != nil {
		return nil, fmt.Errorf("creating manager: %v", err)
	}

	clientApplicator := &io.ClientApplicator{
		Client:     fakeC,
		Applicator: io.NewAPIPatchingApplicator(fakeC),
	}

	rl := libratelimiter.NewDefaultProviderRateLimiter(libratelimiter.DefaultProviderRPS)
	var reconciler = new(reconcile.Reconciler)
	if err = SetupController(
		log,
		mgr,
		rl,
		clientApplicator,
		metrics.MustMakeMetrics(scheme, prometheus.NewRegistry()),
		new(atomic.Bool),
		reconciler,
	); err != nil {
		return nil, fmt.Errorf("setting up controller: %v", err)
	}

	return *reconciler, nil
}

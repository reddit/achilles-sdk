package types

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zaptest"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/reddit/achilles-sdk-api/api"
	intscheme "github.com/reddit/achilles-sdk/pkg/internal/scheme"
	testv1alpha1 "github.com/reddit/achilles-sdk/pkg/internal/tests/api/test/v1alpha1"
	"github.com/reddit/achilles-sdk/pkg/io"
	"github.com/reddit/achilles-sdk/pkg/meta"
	"github.com/reddit/achilles-sdk/pkg/status"
)

var successState = &State[*testv1alpha1.TestClaimed]{}

func Test_GetUnreadyResources(t *testing.T) {
	log := zaptest.NewLogger(t).Sugar()

	scheme, err := intscheme.NewScheme()
	assert.NoError(t, err)

	readyCustomChild := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "child-ready",
		},
	}
	unreadyCustomChild := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "child-unready",
		},
	}
	readyCustomChildInterface := client.Object(&corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "interface-child-ready",
		},
	})

	tcs := []struct {
		name                     string
		parent                   *testv1alpha1.TestClaimed
		childObjs                []client.Object
		expectedUnreadyResources []client.Object
	}{
		{
			name: "ready with custom ready functions",
			parent: &testv1alpha1.TestClaimed{
				ObjectMeta: metav1.ObjectMeta{
					Name: "foobar",
				},
				Status: testv1alpha1.TestClaimedStatus{
					Resources: []api.TypedObjectRef{
						*meta.MustTypedObjectRefFromObject(readyCustomChild, scheme),
						*meta.MustTypedObjectRefFromObject(unreadyCustomChild, scheme),
						*meta.MustTypedObjectRefFromObject(readyCustomChildInterface, scheme),
					},
				},
			},
			childObjs:                []client.Object{readyCustomChild, unreadyCustomChild, readyCustomChildInterface},
			expectedUnreadyResources: []client.Object{unreadyCustomChild},
		},
	}

	for _, tc := range tcs {
		ctx := context.Background()
		fakeC := fake.NewClientBuilder().
			WithObjects(tc.childObjs...).
			WithStatusSubresource(tc.childObjs...).
			WithObjects(tc.parent).
			WithStatusSubresource(tc.parent).
			WithScheme(scheme).
			Build()

		c := &io.ClientApplicator{
			Client:     fakeC,
			Applicator: io.NewAPIPatchingApplicator(fakeC),
		}

		// Create a new instance of the TransitionWhenReady function
		unreadyResources, err := GetUnreadyResources(
			ctx,
			c,
			scheme,
			log,
			tc.parent,
			WithCustomReadyFuncs(
				MakeCustomReadyFunc(func(o client.Object) bool {
					return o.GetName() == "interface-child-ready"
				}),
				MakeCustomReadyFunc(func(o *corev1.Secret) bool {
					return o.GetName() == "child-ready"
				}),
			),
		)

		assert.NoError(t, err, tc.name)

		assert.ElementsMatch(t, tc.expectedUnreadyResources, unreadyResources, tc.name)
	}
}

func Test_TransitionWhenReady(t *testing.T) {
	requeueDuration := 10 * time.Second
	log := zaptest.NewLogger(t).Sugar()
	scheme, err := intscheme.NewScheme()
	assert.NoError(t, err)

	unreadyChild := &testv1alpha1.TestClaimed{
		ObjectMeta: metav1.ObjectMeta{
			Name: "child-unready",
		},
		Status: testv1alpha1.TestClaimedStatus{
			ConditionedStatus: api.ConditionedStatus{
				Conditions: []api.Condition{status.NewUnreadyCondition(0)},
			},
		},
	}
	anotherUnreadyChild := &testv1alpha1.TestClaimed{
		ObjectMeta: metav1.ObjectMeta{
			Name: "child-unready-2",
		},
		Status: testv1alpha1.TestClaimedStatus{
			ConditionedStatus: api.ConditionedStatus{
				Conditions: []api.Condition{status.NewUnreadyCondition(0)},
			},
		},
	}
	readyChild := &testv1alpha1.TestClaimed{
		ObjectMeta: metav1.ObjectMeta{
			Name: "child-ready",
		},
		Status: testv1alpha1.TestClaimedStatus{
			ConditionedStatus: api.ConditionedStatus{
				Conditions: []api.Condition{status.NewReadyCondition(0)},
			},
		},
	}

	tcs := []struct {
		name              string
		parentObj         *testv1alpha1.TestClaimed
		fakeObjects       []client.Object
		resourcesToCheck  []client.Object
		expectedNextState *State[*testv1alpha1.TestClaimed]
		expectedResult    Result
	}{
		{
			name: "check all resources, should fail due to unready resource",
			parentObj: &testv1alpha1.TestClaimed{
				ObjectMeta: metav1.ObjectMeta{
					Name: "foobar",
				},
				Status: testv1alpha1.TestClaimedStatus{
					Resources: []api.TypedObjectRef{
						*meta.MustTypedObjectRefFromObject(unreadyChild, scheme),
						*meta.MustTypedObjectRefFromObject(readyChild, scheme),
						*meta.MustTypedObjectRefFromObject(anotherUnreadyChild, scheme),
					},
				},
			},
			fakeObjects: []client.Object{
				unreadyChild,
				anotherUnreadyChild,
				readyChild,
			},
			expectedNextState: nil,
			expectedResult: Result{
				RequeueAfter: requeueDuration,
				RequeueMsg:   "some managed resources are not ready. First three:\ntest.infrared.reddit.com/v1alpha1, Kind=TestClaimed: /child-unready,\ntest.infrared.reddit.com/v1alpha1, Kind=TestClaimed: /child-unready-2",
			},
		},
		{
			name: "check specific resource, should succeed despite unready resource",
			parentObj: &testv1alpha1.TestClaimed{
				ObjectMeta: metav1.ObjectMeta{
					Name: "foobar",
				},
				Status: testv1alpha1.TestClaimedStatus{
					Resources: []api.TypedObjectRef{
						*meta.MustTypedObjectRefFromObject(unreadyChild, scheme),
						*meta.MustTypedObjectRefFromObject(readyChild, scheme),
					},
				},
			},
			resourcesToCheck: []client.Object{
				readyChild,
			},
			fakeObjects: []client.Object{
				unreadyChild,
				readyChild,
			},
			expectedNextState: successState,
			expectedResult: Result{
				Done: true,
			},
		},
	}

	for _, tc := range tcs {
		ctx := context.Background()
		fakeC := fake.NewClientBuilder().
			WithObjects(tc.fakeObjects...).
			WithStatusSubresource(tc.fakeObjects...).
			WithObjects(tc.parentObj).
			WithStatusSubresource(tc.parentObj).
			WithScheme(scheme).
			Build()

		c := &io.ClientApplicator{
			Client:     fakeC,
			Applicator: io.NewAPIPatchingApplicator(fakeC),
		}

		// Create a new instance of the TransitionWhenReady function
		actualNextState, actualResult := TransitionWhenReady[*testv1alpha1.TestClaimed](
			c,
			scheme,
			log,
			successState,
			WithRequeueAfter(requeueDuration),
			WithResources(tc.resourcesToCheck...),
		)(
			ctx,
			tc.parentObj,
			nil,
		)

		assert.Equal(t, tc.expectedNextState, actualNextState)
		assert.Equal(t, tc.expectedResult, actualResult)
	}

}

func Test_DeleteChildrenForeground(t *testing.T) {
	log := zaptest.NewLogger(t).Sugar()
	scheme, err := intscheme.NewScheme()
	assert.NoError(t, err)

	childA := &testv1alpha1.TestClaimed{
		ObjectMeta: metav1.ObjectMeta{
			Name: "child-a",
		},
	}
	childB := &testv1alpha1.TestClaimed{
		ObjectMeta: metav1.ObjectMeta{
			Name: "child-b",
		},
	}

	tests := []struct {
		name              string
		parent            *testv1alpha1.TestClaimed
		children          []client.Object
		expectedNextState *State[*testv1alpha1.TestClaimed]
		expectedResult    Result
	}{
		{
			name: "no children",
			parent: &testv1alpha1.TestClaimed{
				ObjectMeta: metav1.ObjectMeta{
					Name: "parent",
				},
			},
			expectedNextState: successState,
			expectedResult:    DoneResult(),
		},
		{
			name: "with children that are not deleted",
			parent: &testv1alpha1.TestClaimed{
				ObjectMeta: metav1.ObjectMeta{
					Name: "parent",
				},
				Status: testv1alpha1.TestClaimedStatus{
					Resources: []api.TypedObjectRef{
						*meta.MustTypedObjectRefFromObject(childA, scheme),
						*meta.MustTypedObjectRefFromObject(childB, scheme),
					},
				},
			},
			children: []client.Object{
				childA,
				childB,
			},
			expectedNextState: nil,
			expectedResult: Result{
				RequeueMsg: `waiting for child resources to be deleted, first three:
test.infrared.reddit.com/v1alpha1, Kind=TestClaimed: /child-a,
test.infrared.reddit.com/v1alpha1, Kind=TestClaimed: /child-b`,
				Reason: "WaitingForChildDeletion",
				Done:   false,
			},
		},
		{
			name: "with children that are all deleted",
			parent: &testv1alpha1.TestClaimed{
				ObjectMeta: metav1.ObjectMeta{
					Name: "parent",
				},
				Status: testv1alpha1.TestClaimedStatus{
					Resources: []api.TypedObjectRef{
						*meta.MustTypedObjectRefFromObject(childA, scheme),
						*meta.MustTypedObjectRefFromObject(childB, scheme),
					},
				},
			},
			children:          []client.Object{},
			expectedNextState: successState,
			expectedResult:    DoneResult(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			fakeC := fake.NewClientBuilder().
				WithObjects(tt.children...).
				WithStatusSubresource(tt.children...).
				WithObjects(tt.parent).
				WithStatusSubresource(tt.parent).
				WithScheme(scheme).
				Build()
			c := &io.ClientApplicator{
				Client:     fakeC,
				Applicator: io.NewAPIPatchingApplicator(fakeC),
			}

			actualNextState, actualResult := DeleteChildrenForeground[*testv1alpha1.TestClaimed](c, scheme, log, successState)(
				ctx,
				tt.parent,
				nil,
			)

			assert.Equal(t, tt.expectedNextState, actualNextState)
			assert.Equal(t, tt.expectedResult, actualResult)
		})
	}
}

func Test_ErrorResultf(t *testing.T) {
	type args struct {
		format string
		args   []any
	}
	tests := []struct {
		name string
		args args
		want error
	}{
		{
			name: "no args",
			args: args{
				format: "this is an error",
				args:   nil,
			},
			want: errors.New("this is an error"),
		},
		{
			name: "with args",
			args: args{
				format: "this is an error with %d args, %w",
				args:   []any{2, errors.New("some error")},
			},
			want: errors.New("this is an error with 2 args, some error"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ErrorResultf(tt.args.format, tt.args.args...)
			assert.Equal(t, tt.want.Error(), got.Err.Error())
		})
	}
}

package internal

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"go.uber.org/zap/zaptest"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/reddit/achilles-sdk-api/api"
	internalscheme "github.com/reddit/achilles-sdk/pkg/internal/scheme"
	"github.com/reddit/achilles-sdk/pkg/internal/tests/api/test/v1alpha1"
	"github.com/reddit/achilles-sdk/pkg/io"
	"github.com/reddit/achilles-sdk/pkg/meta"
	"github.com/reddit/achilles-sdk/pkg/test"
)

const (
	testClaimName = "test-claim"
)

var (
	now    = metav1.NewTime(time.Now().Round(time.Second))
	scheme = internalscheme.MustNewScheme()
)

func init() {
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		panic(fmt.Sprintf("failed to initialize test scheme: %s", err))
	}
}

func TestReconciler_Claim(t *testing.T) {
	cases := []struct {
		name    string
		in      []client.Object
		out     []client.Object
		missing []client.Object
		err     error
	}{
		{
			name: "success/creates_claimed",
			in: []client.Object{
				newTestClaim(),
			},
			out: []client.Object{
				apply(newTestClaim(), withFinalizer, withGeneratedClaimRef),
				apply(&v1alpha1.TestClaimed{}, withGeneratedName, withRedditLabels, withClaimRef),
			},
		},
		{
			name: "success/exposes_claimed_status",
			in: []client.Object{
				apply(newTestClaim(), withFinalizer, withGeneratedClaimRef),
				apply(&v1alpha1.TestClaimed{}, withGeneratedName, withClaimRef, withSuccessfulConditions[*v1alpha1.TestClaimed]),
			},
			out: []client.Object{
				apply(newTestClaim(), withFinalizer, withGeneratedClaimRef, withSuccessfulConditions[*v1alpha1.TestClaim]),
				apply(&v1alpha1.TestClaimed{}, withGeneratedName, withRedditLabels, withClaimRef, withSuccessfulConditions[*v1alpha1.TestClaimed]),
			},
		},
		{
			name: "success/deletes_claimed",
			in: []client.Object{
				apply(newTestClaim(), withFinalizer, withGeneratedClaimRef, withDeletedTimestamp[*v1alpha1.TestClaim]),
				apply(&v1alpha1.TestClaimed{}, withGeneratedName, withClaimRef, withCreatedTimestamp[*v1alpha1.TestClaimed]),
			},
			out: []client.Object{
				apply(newTestClaim(), withFinalizer, withGeneratedClaimRef, withDeletedTimestamp[*v1alpha1.TestClaim]),
			},
			missing: []client.Object{
				apply(&v1alpha1.TestClaimed{}, withGeneratedName),
			},
		},
		{
			name: "success/removes_finalizer",
			in: []client.Object{
				apply(newTestClaim(), withFinalizer, withGeneratedClaimRef, withDeletedTimestamp[*v1alpha1.TestClaim]),
			},
			missing: []client.Object{
				newTestClaim(),
			},
		},
		{
			name: "failure/broken_claim_ref",
			in: []client.Object{
				apply(newTestClaim(), withFinalizer, withGeneratedClaimRef, withDeletedTimestamp[*v1alpha1.TestClaim]),
				apply(&v1alpha1.TestClaimed{}, withGeneratedName, withCreatedTimestamp[*v1alpha1.TestClaimed]),
			},
			err: errors.New("claimed not owned by claim"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			log := zaptest.NewLogger(t).Sugar()
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tc.in...).
				WithStatusSubresource(tc.in...).
				Build()

			fakeClient = test.NewFilteringClient(fakeClient, test.FilterFn(generateNameClientFilter))

			c := testApplicator(fakeClient)

			r := NewClaimReconciler(&v1alpha1.TestClaimed{}, &v1alpha1.TestClaim{}, c, scheme, log, nil)

			ctx := context.Background()
			req := reconcile.Request{NamespacedName: types.NamespacedName{Name: testClaimName}}
			if _, err := r.Reconcile(ctx, req); err != nil && tc.err != nil {
				if errors.Is(err, tc.err) {
					return
				}
				t.Fatalf("error running reconciler did not match expected\ngot: %s\nwant: %s", err, tc.err)
			} else if err != nil {
				t.Fatalf("running reconciler: %s", err)
			}

			for _, m := range tc.missing {
				if err := c.Get(ctx, client.ObjectKeyFromObject(m), m); !k8serrors.IsNotFound(err) {
					if err != nil {
						t.Fatalf("unexpected error testing missing key %s: %s", client.ObjectKeyFromObject(m), err)
					} else {
						t.Errorf("object %s was expected to be missing, was found", client.ObjectKeyFromObject(m))
					}
				}
			}

			for _, o := range tc.out {
				got, err := meta.NewObjectForGVK(scheme, meta.MustGVKForObject(o, scheme))
				if err != nil {
					t.Fatalf("constructing expected object: %s", err)
				}

				if err := c.Get(ctx, client.ObjectKeyFromObject(o), got); err != nil {
					t.Errorf("getting expected object: %s", err)
					continue
				}

				if diff := cmp.Diff(got, o,
					cmpopts.EquateEmpty(),
					cmpopts.IgnoreFields(metav1.ObjectMeta{}, "ResourceVersion"),
					cmpopts.IgnoreTypes(metav1.TypeMeta{})); diff != "" {
					t.Errorf("object differs from expected: (-got +want)\n%s", diff)
				}
			}
		})
	}
}

// helpers
func newTestClaim() *v1alpha1.TestClaim {
	return &v1alpha1.TestClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name: testClaimName,
		},
		Spec: v1alpha1.TestClaimSpec{
			TestField: "default",
		},
		Status: v1alpha1.TestClaimStatus{
			ConditionedStatus: api.ConditionedStatus{
				Conditions: []api.Condition{
					api.Creating(),
				},
			},
		},
	}
}

func withFinalizer(t *v1alpha1.TestClaim) {
	t.Finalizers = []string{finalizer}
}

func withGeneratedClaimRef(t *v1alpha1.TestClaim) {
	t.Spec.ClaimedRef = &api.TypedObjectRef{
		Group:   v1alpha1.TestClaimedGroupVersionKind.Group,
		Version: v1alpha1.TestClaimedGroupVersionKind.Version,
		Kind:    v1alpha1.TestClaimedKind,
		Name:    testClaimName + "-abcde",
	}
}

func withDeletedTimestamp[T client.Object](t T) {
	t.SetDeletionTimestamp(&now)
}

func withCreatedTimestamp[T client.Object](t T) {
	t.SetCreationTimestamp(now)
}

func withGeneratedName(t *v1alpha1.TestClaimed) {
	t.GenerateName = testClaimName + "-"
	t.Name = testClaimName + "-abcde"
}

func withRedditLabels(t *v1alpha1.TestClaimed) {
	meta.SetRedditLabels(t, v1alpha1.TestClaimKind)
}

func withClaimRef(t *v1alpha1.TestClaimed) {
	t.Spec.ClaimRef = &api.TypedObjectRef{
		Group:   v1alpha1.TestClaimGroupVersionKind.Group,
		Version: v1alpha1.TestClaimGroupVersionKind.Version,
		Kind:    v1alpha1.TestClaimKind,
		Name:    testClaimName,
	}
}

func withSuccessfulConditions[T api.Conditioned](t T) {
	t.SetConditions(api.Available())
}

func testApplicator(c client.Client) *io.ClientApplicator {
	return &io.ClientApplicator{
		Client:     c,
		Applicator: io.NewAPIPatchingApplicator(c),
	}
}

func generateNameClientFilter(_ string, obj client.Object) error {
	if gn := obj.GetGenerateName(); gn != "" {
		obj.SetName(gn + "abcde")
	}
	return nil
}

func apply[T client.Object](obj T, fns ...func(obj T)) T {
	for _, fn := range fns {
		fn(obj)
	}
	return obj
}

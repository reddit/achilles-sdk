package meta_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/reddit/achilles-sdk/pkg/meta"
)

const (
	testFinalizer  = "test.reddit.com/finalizer"
	otherFinalizer = "foregroundDeletion"
)

var cmGR = schema.GroupResource{Group: "", Resource: "configmaps"}

func newCM(finalizers ...string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "cm",
			Namespace:  "default",
			Finalizers: finalizers,
		},
	}
}

func newClient(funcs interceptor.Funcs, objs ...client.Object) client.Client {
	return fake.NewClientBuilder().
		WithScheme(scheme.Scheme).
		WithObjects(objs...).
		WithInterceptorFuncs(funcs).
		Build()
}

func getCM(t *testing.T, c client.Client) *corev1.ConfigMap {
	t.Helper()
	cm := &corev1.ConfigMap{}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(newCM()), cm); err != nil {
		t.Fatalf("getting configmap: %v", err)
	}
	return cm
}

func assertFinalizers(t *testing.T, cm *corev1.ConfigMap, want ...string) {
	t.Helper()
	got := cm.GetFinalizers()
	if len(got) != len(want) {
		t.Fatalf("finalizers = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("finalizers = %v, want %v", got, want)
		}
	}
}

func TestRemoveFinalizer(t *testing.T) {
	ctx := context.Background()

	t.Run("object not found on get is success", func(t *testing.T) {
		var patches atomic.Int32
		c := newClient(interceptor.Funcs{
			Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				patches.Add(1)
				return c.Patch(ctx, obj, patch, opts...)
			},
		})

		if err := meta.RemoveFinalizer(ctx, c, newCM(), testFinalizer); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if patches.Load() != 0 {
			t.Fatalf("expected no patch, got %d", patches.Load())
		}
	})

	t.Run("object deleted between get and patch is success", func(t *testing.T) {
		c := newClient(interceptor.Funcs{
			Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				return apierrors.NewNotFound(cmGR, obj.GetName())
			},
		}, newCM(testFinalizer))

		if err := meta.RemoveFinalizer(ctx, c, newCM(), testFinalizer); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("finalizer absent issues no patch", func(t *testing.T) {
		var patches atomic.Int32
		c := newClient(interceptor.Funcs{
			Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				patches.Add(1)
				return c.Patch(ctx, obj, patch, opts...)
			},
		}, newCM(otherFinalizer))

		if err := meta.RemoveFinalizer(ctx, c, newCM(), testFinalizer); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if patches.Load() != 0 {
			t.Fatalf("expected no patch, got %d", patches.Load())
		}
		assertFinalizers(t, getCM(t, c), otherFinalizer)
	})

	t.Run("concurrent modification between get and patch is retried", func(t *testing.T) {
		// The stored object has both finalizers. On the first Get, hand back a stale copy that predates the
		// other actor's finalizer, so a naive merge patch would strip it. The fake client's resourceVersion
		// check turns the stale patch into a Conflict, and the retry recomputes against the live object.
		var gets, patches atomic.Int32
		c := newClient(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if err := c.Get(ctx, key, obj, opts...); err != nil {
					return err
				}
				if gets.Add(1) == 1 {
					obj.SetFinalizers([]string{testFinalizer})
					obj.SetResourceVersion("1")
				}
				return nil
			},
			Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				patches.Add(1)
				return c.Patch(ctx, obj, patch, opts...)
			},
		}, newCM(testFinalizer, otherFinalizer))

		if err := meta.RemoveFinalizer(ctx, c, newCM(), testFinalizer); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if patches.Load() != 2 {
			t.Fatalf("expected 2 patches (conflict then success), got %d", patches.Load())
		}
		assertFinalizers(t, getCM(t, c), otherFinalizer)
	})

	t.Run("persistent conflict returns error", func(t *testing.T) {
		c := newClient(interceptor.Funcs{
			Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				return apierrors.NewConflict(cmGR, obj.GetName(), errors.New("always stale"))
			},
		}, newCM(testFinalizer))

		err := meta.RemoveFinalizer(ctx, c, newCM(), testFinalizer)
		if !apierrors.IsConflict(err) {
			t.Fatalf("expected conflict error, got %v", err)
		}
	})

	t.Run("non-retryable error is returned", func(t *testing.T) {
		c := newClient(interceptor.Funcs{
			Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				return apierrors.NewForbidden(cmGR, obj.GetName(), errors.New("nope"))
			},
		}, newCM(testFinalizer))

		err := meta.RemoveFinalizer(ctx, c, newCM(), testFinalizer)
		if !apierrors.IsForbidden(err) {
			t.Fatalf("expected forbidden error, got %v", err)
		}
	})

	t.Run("removes finalizer and updates passed object", func(t *testing.T) {
		c := newClient(interceptor.Funcs{}, newCM(testFinalizer, otherFinalizer))

		cm := newCM()
		if err := meta.RemoveFinalizer(ctx, c, cm, testFinalizer); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertFinalizers(t, cm, otherFinalizer)
		assertFinalizers(t, getCM(t, c), otherFinalizer)
	})
}

func TestAddFinalizer(t *testing.T) {
	ctx := context.Background()

	t.Run("object not found is an error", func(t *testing.T) {
		c := newClient(interceptor.Funcs{})
		err := meta.AddFinalizer(ctx, c, newCM(), testFinalizer)
		if !apierrors.IsNotFound(err) {
			t.Fatalf("expected not found error, got %v", err)
		}
	})

	t.Run("already present issues no patch", func(t *testing.T) {
		var patches atomic.Int32
		c := newClient(interceptor.Funcs{
			Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				patches.Add(1)
				return c.Patch(ctx, obj, patch, opts...)
			},
		}, newCM(testFinalizer))

		if err := meta.AddFinalizer(ctx, c, newCM(), testFinalizer); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if patches.Load() != 0 {
			t.Fatalf("expected no patch, got %d", patches.Load())
		}
	})

	t.Run("conflict then success preserves other finalizers", func(t *testing.T) {
		var gets atomic.Int32
		c := newClient(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if err := c.Get(ctx, key, obj, opts...); err != nil {
					return err
				}
				if gets.Add(1) == 1 {
					obj.SetFinalizers(nil)
					obj.SetResourceVersion("1")
				}
				return nil
			},
		}, newCM(otherFinalizer))

		cm := newCM()
		if err := meta.AddFinalizer(ctx, c, cm, testFinalizer); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertFinalizers(t, cm, otherFinalizer, testFinalizer)
		assertFinalizers(t, getCM(t, c), otherFinalizer, testFinalizer)
	})
}

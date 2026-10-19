package test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type subResourceCall struct {
	event       string
	ctx         context.Context
	obj         client.Object
	subResource client.Object
	patch       client.Patch
	opts        interface{}
}

type recordingSubResourceClient struct {
	client.SubResourceClient
	record func(subResourceCall) error
}

func (c *recordingSubResourceClient) Get(ctx context.Context, obj, subResource client.Object, opts ...client.SubResourceGetOption) error {
	return c.record(subResourceCall{event: "get", ctx: ctx, obj: obj, subResource: subResource, opts: opts})
}

func (c *recordingSubResourceClient) Create(ctx context.Context, obj, subResource client.Object, opts ...client.SubResourceCreateOption) error {
	return c.record(subResourceCall{event: "create", ctx: ctx, obj: obj, subResource: subResource, opts: opts})
}

func (c *recordingSubResourceClient) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	return c.record(subResourceCall{event: "update", ctx: ctx, obj: obj, opts: opts})
}

func (c *recordingSubResourceClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	return c.record(subResourceCall{event: "patch", ctx: ctx, obj: obj, patch: patch, opts: opts})
}

func TestFilteringSubResourceClient(t *testing.T) {
	ctx := context.Background()
	obj, subResource := &corev1.Pod{}, &corev1.Pod{}
	patch := client.MergeFrom(obj.DeepCopy())
	getOpts := []client.SubResourceGetOption{&client.SubResourceGetOptions{}}
	createOpts := []client.SubResourceCreateOption{&client.SubResourceCreateOptions{}}
	updateOpts := []client.SubResourceUpdateOption{&client.SubResourceUpdateOptions{}}
	patchOpts := []client.SubResourcePatchOption{&client.SubResourcePatchOptions{}}

	for _, tc := range []struct {
		want subResourceCall
		call func(*filteringSubResourceClient) error
	}{
		{
			want: subResourceCall{event: "get", ctx: ctx, obj: obj, subResource: subResource, opts: getOpts},
			call: func(c *filteringSubResourceClient) error { return c.Get(ctx, obj, subResource, getOpts...) },
		},
		{
			want: subResourceCall{event: "create", ctx: ctx, obj: obj, subResource: subResource, opts: createOpts},
			call: func(c *filteringSubResourceClient) error { return c.Create(ctx, obj, subResource, createOpts...) },
		},
		{
			want: subResourceCall{event: "update", ctx: ctx, obj: obj, opts: updateOpts},
			call: func(c *filteringSubResourceClient) error { return c.Update(ctx, obj, updateOpts...) },
		},
		{
			want: subResourceCall{event: "patch", ctx: ctx, obj: obj, patch: patch, opts: patchOpts},
			call: func(c *filteringSubResourceClient) error { return c.Patch(ctx, obj, patch, patchOpts...) },
		},
	} {
		t.Run(tc.want.event, func(t *testing.T) {
			t.Run("forward", func(t *testing.T) {
				delegateErr := errors.New("delegate failed")
				calls := 0
				filtersRun := 0
				c := &filteringSubResourceClient{
					SubResourceClient: &recordingSubResourceClient{record: func(got subResourceCall) error {
						calls++
						require.Equal(t, 2, filtersRun)
						require.Equal(t, tc.want, got)
						require.Same(t, obj, got.obj)
						return delegateErr
					}},
					filters: []ClientFilter{
						FilterFn(func(event string, got client.Object) error {
							require.Equal(t, tc.want.event, event)
							require.Same(t, obj, got)
							filtersRun++
							return nil
						}),
						FilterFn(func(string, client.Object) error { filtersRun++; return nil }),
					},
				}
				require.ErrorIs(t, tc.call(c), delegateErr)
				require.Equal(t, 1, calls)
			})
			t.Run("reject", func(t *testing.T) {
				filterErr := errors.New("filter rejected")
				c := &filteringSubResourceClient{
					SubResourceClient: &recordingSubResourceClient{record: func(subResourceCall) error {
						t.Fatal("delegate called after filter rejection")
						return nil
					}},
					filters: []ClientFilter{
						FilterFn(func(string, client.Object) error { return filterErr }),
						FilterFn(func(string, client.Object) error {
							t.Fatal("later filter called after rejection")
							return nil
						}),
					},
				}
				err := tc.call(c)
				require.ErrorIs(t, err, filterErr)
				require.EqualError(t, err, "running filter: filter rejected")
			})
		})
	}
}

type statusRecordingClient struct {
	client.WithWatch
	subResource client.SubResourceClient
	name        string
}

func (c *statusRecordingClient) SubResource(name string) client.SubResourceClient {
	c.name = name
	return c.subResource
}

func TestFilteringClientStatus(t *testing.T) {
	obj := &corev1.Pod{}
	calls := 0
	underlying := &statusRecordingClient{
		subResource: &recordingSubResourceClient{record: func(got subResourceCall) error {
			calls++
			require.Equal(t, "update", got.event)
			require.Same(t, obj, got.obj)
			return nil
		}},
	}
	c := NewFilteringClient(underlying, FilterFn(func(string, client.Object) error {
		t.Fatal("Status unexpectedly ran parent filters")
		return nil
	}))
	require.NoError(t, c.Status().Update(context.Background(), obj))
	require.Equal(t, "status", underlying.name)
	require.Equal(t, 1, calls)
}

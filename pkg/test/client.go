package test

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ClientFilter interface {
	Filter(event string, obj client.Object) error
}

type FilterFn func(event string, obj client.Object) error

func (f FilterFn) Filter(event string, obj client.Object) error {
	return f(event, obj)
}

func NewFilteringClient(c client.WithWatch, f ...ClientFilter) *FilteringClient {
	return &FilteringClient{
		WithWatch: c,
		filters:   f,
	}
}

type FilteringClient struct {
	client.WithWatch
	filters []ClientFilter
}

func (c *FilteringClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	for _, f := range c.filters {
		if err := f.Filter("create", obj); err != nil {
			return fmt.Errorf("running filter: %w", err)
		}
	}

	return c.WithWatch.Create(ctx, obj, opts...)
}

func (c *FilteringClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	for _, f := range c.filters {
		if err := f.Filter("update", obj); err != nil {
			return fmt.Errorf("running filter: %w", err)
		}
	}
	return c.WithWatch.Update(ctx, obj, opts...)
}

func (c *FilteringClient) Status() client.SubResourceWriter {
	return &filteringSubResourceClient{SubResourceClient: c.SubResource("status")}
}

// ensure filteringSubResourceClient implements client.SubResourceClient.
var _ client.SubResourceClient = &filteringSubResourceClient{}

type filteringSubResourceClient struct {
	// Forward additional subresource methods introduced by newer client versions.
	client.SubResourceClient
	filters []ClientFilter
}

func (c *filteringSubResourceClient) Get(ctx context.Context, obj, subResource client.Object, opts ...client.SubResourceGetOption) error {
	for _, f := range c.filters {
		if err := f.Filter("get", obj); err != nil {
			return fmt.Errorf("running filter: %w", err)
		}
	}

	return c.SubResourceClient.Get(ctx, obj, subResource, opts...)
}

func (c *filteringSubResourceClient) Create(ctx context.Context, obj, subResource client.Object, opts ...client.SubResourceCreateOption) error {
	for _, f := range c.filters {
		if err := f.Filter("create", obj); err != nil {
			return fmt.Errorf("running filter: %w", err)
		}
	}

	return c.SubResourceClient.Create(ctx, obj, subResource, opts...)
}

func (c *filteringSubResourceClient) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	for _, f := range c.filters {
		if err := f.Filter("update", obj); err != nil {
			return fmt.Errorf("running filter: %w", err)
		}
	}

	return c.SubResourceClient.Update(ctx, obj, opts...)
}

func (c *filteringSubResourceClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	for _, f := range c.filters {
		if err := f.Filter("patch", obj); err != nil {
			return fmt.Errorf("running filter: %w", err)
		}
	}

	return c.SubResourceClient.Patch(ctx, obj, patch, opts...)
}

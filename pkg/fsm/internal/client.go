package internal

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// apiReadClient lets finalizer retries read from the API server while using
// the manager client for writes.
type apiReadClient struct {
	client.Client
	reader client.Reader
}

func (c apiReadClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	return c.reader.Get(ctx, key, obj, opts...)
}

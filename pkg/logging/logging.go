package logging

import (
	"context"
	"errors"

	"go.uber.org/zap"
)

// contextKey is how we find *zap.SugaredLogger in a context.Context.
type contextKey struct{}

// NewContext returns a new Context, derived from ctx, which carries the
// provided *zap.SugaredLogger.
func NewContext(ctx context.Context, logger *zap.SugaredLogger) context.Context {
	return context.WithValue(ctx, contextKey{}, logger)
}

// FromContext returns a Logger from ctx or an error if no Logger is found.
func FromContext(ctx context.Context) (*zap.SugaredLogger, error) {
	if v, ok := ctx.Value(contextKey{}).(*zap.SugaredLogger); ok && v != nil {
		return v, nil
	}

	return nil, errors.New("no *zap.SugaredLogger was present")
}

// helper for building controller's child context and logger
func ControllerCtx(ctx context.Context, controllerName string) (context.Context, *zap.SugaredLogger, error) {
	logger, err := FromContext(ctx)
	if err != nil {
		return nil, nil, err
	}

	logger = logger.Named(controllerName)

	return ctx, logger, nil
}

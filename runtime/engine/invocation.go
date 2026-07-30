package engine

import (
	"context"
	"errors"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
)

var ErrInvalidInvocationRef = errors.New("invalid Host invocation reference")

type invocationRefContextKey struct{}

func WithInvocationRef(ctx context.Context, ref runtimeconfig.InvocationRef) (context.Context, error) {
	if ctx == nil || ref.Validate() != nil {
		return nil, ErrInvalidInvocationRef
	}
	return context.WithValue(ctx, invocationRefContextKey{}, ref), nil
}

func InvocationRefFromContext(ctx context.Context) (runtimeconfig.InvocationRef, bool) {
	if ctx == nil {
		return runtimeconfig.InvocationRef{}, false
	}
	ref, ok := ctx.Value(invocationRefContextKey{}).(runtimeconfig.InvocationRef)
	return ref, ok && ref.Validate() == nil
}

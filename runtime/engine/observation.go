package engine

import (
	"context"
	"errors"

	"github.com/bkmashiro/agent-python-runtime/runtime/observe"
)

var ErrInvalidObservationSession = errors.New("invalid Host observation session")

type observationSessionContextKey struct{}

func WithObservationSession(ctx context.Context, session *observe.Session) (context.Context, error) {
	if ctx == nil || session == nil {
		return nil, ErrInvalidObservationSession
	}
	return context.WithValue(ctx, observationSessionContextKey{}, session), nil
}

func ObservationSessionFromContext(ctx context.Context) (*observe.Session, bool) {
	if ctx == nil {
		return nil, false
	}
	session, ok := ctx.Value(observationSessionContextKey{}).(*observe.Session)
	return session, ok && session != nil
}

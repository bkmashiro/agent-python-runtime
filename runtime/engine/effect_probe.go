package engine

import (
	"context"
	"sync/atomic"
)

type effectProbeContextKey struct{}

// EffectProbe records shareability-breaking Host boundary attempts made by one
// Guest execution. It is evidence for admission, not a capability grant.
type EffectProbe struct {
	hostCall atomic.Bool
}

func WithEffectProbe(ctx context.Context) (context.Context, *EffectProbe) {
	probe := &EffectProbe{}
	return context.WithValue(ctx, effectProbeContextKey{}, probe), probe
}

func MarkHostCallAttempt(ctx context.Context) {
	if probe, ok := ctx.Value(effectProbeContextKey{}).(*EffectProbe); ok && probe != nil {
		probe.hostCall.Store(true)
	}
}

func (probe *EffectProbe) HostCallAttempted() bool {
	return probe != nil && probe.hostCall.Load()
}

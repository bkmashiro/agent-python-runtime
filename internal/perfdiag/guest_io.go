package perfdiag

import "context"

// GuestIO is a per-execution diagnostic sink. Do not reuse its context across
// concurrent executions. Raw bytes preserve non-UTF-8 output as well.
type GuestIO struct{ Stdout, Stderr []byte }
type guestIOKey struct{}

func WithGuestIO(ctx context.Context) (context.Context, *GuestIO) {
	sink := &GuestIO{}
	return context.WithValue(ctx, guestIOKey{}, sink), sink
}

// CaptureGuestIO is a no-op unless the embedding diagnostic enabled a sink.
func CaptureGuestIO(ctx context.Context, stdout, stderr string) {
	if sink, ok := ctx.Value(guestIOKey{}).(*GuestIO); ok {
		sink.Stdout = append(sink.Stdout[:0], []byte(stdout)...)
		sink.Stderr = append(sink.Stderr[:0], []byte(stderr)...)
	}
}

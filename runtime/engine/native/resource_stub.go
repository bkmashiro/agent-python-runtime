//go:build !linux

package native

import "context"

type resourceAggregate struct {
	Samples           uint64
	MemoryCurrentPeak uint64
	PSSPeak           uint64
	PrivateDirtyPeak  uint64
	ReadBytes         uint64
	WriteBytes        uint64
	PidsPeak          uint64
}

func sampleCgroup(ctx context.Context, _ string) <-chan resourceAggregate {
	result := make(chan resourceAggregate, 1)
	go func() { <-ctx.Done(); result <- resourceAggregate{}; close(result) }()
	return result
}

package engine

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalidAttemptIdentity      = errors.New("invalid Host attempt identity")
	ErrInvalidFootprintObservation = errors.New("invalid memory footprint observation")
	ErrInvalidReclaimObservation   = errors.New("invalid memory reclaim observation")
)

type attemptIdentityContextKey struct{}

func WithAttemptIdentity(ctx context.Context, attemptID string) (context.Context, error) {
	if ctx == nil || !boundedRuntimeIdentifier(attemptID, 128) {
		return nil, ErrInvalidAttemptIdentity
	}
	return context.WithValue(ctx, attemptIdentityContextKey{}, attemptID), nil
}

func AttemptIdentityFromContext(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	attemptID, ok := ctx.Value(attemptIdentityContextKey{}).(string)
	return attemptID, ok && boundedRuntimeIdentifier(attemptID, 128)
}

type FootprintStatus string

const (
	FootprintObserved    FootprintStatus = "observed"
	FootprintUnavailable FootprintStatus = "unavailable"
	FootprintFailed      FootprintStatus = "failed"
)

type MemoryFootprint struct {
	MappingCount      uint32
	VirtualBytes      uint64
	RSSBytes          uint64
	PSSBytes          uint64
	PrivateCleanBytes uint64
	PrivateDirtyBytes uint64
	AnonymousBytes    uint64
	SwapBytes         uint64
}

type FootprintObservation struct {
	AttemptID string
	Backend   string
	Strategy  ExecutionStrategy
	Status    FootprintStatus
	SampledAt time.Time
	Memory    MemoryFootprint
	ErrorCode string
}

// FootprintSink decides whether a served attempt should pay the Linux smaps
// sampling cost and receives the resulting bounded observation. Implementations
// must be safe for concurrent Runner.Run calls and must not retain secrets.
type FootprintSink interface {
	ShouldSample(attemptID string) bool
	Observe(FootprintObservation)
}

type ReclaimStatus string

const (
	ReclaimReleased    ReclaimStatus = "released"
	ReclaimStillMapped ReclaimStatus = "still_mapped"
	ReclaimUnavailable ReclaimStatus = "unavailable"
	ReclaimFailed      ReclaimStatus = "failed"
)

type MemoryReclaimObservation struct {
	AttemptID     string
	ObservedAt    time.Time
	Backend       string
	Strategy      ExecutionStrategy
	Status        ReclaimStatus
	CloseDuration time.Duration
	ErrorCode     string
}

func (observation MemoryReclaimObservation) Validate() error {
	if !boundedRuntimeIdentifier(observation.AttemptID, 128) || !boundedRuntimeIdentifier(observation.Backend, 64) ||
		!validStrategy(observation.Strategy) || observation.ObservedAt.IsZero() || observation.CloseDuration < 0 {
		return ErrInvalidReclaimObservation
	}
	switch observation.Status {
	case ReclaimReleased:
		if observation.ErrorCode != "" {
			return ErrInvalidReclaimObservation
		}
	case ReclaimStillMapped, ReclaimUnavailable, ReclaimFailed:
		if !boundedErrorCode(observation.ErrorCode) {
			return ErrInvalidReclaimObservation
		}
	default:
		return ErrInvalidReclaimObservation
	}
	return nil
}

// ReclaimSink receives close-complete verification for the same Host attempt.
// Implementations must be safe for concurrent Runner.Run calls.
type ReclaimSink interface {
	ShouldObserve(attemptID string) bool
	ObserveReclaim(MemoryReclaimObservation)
}

func (observation FootprintObservation) Validate() error {
	if !boundedRuntimeIdentifier(observation.AttemptID, 128) || !boundedRuntimeIdentifier(observation.Backend, 64) ||
		!validStrategy(observation.Strategy) || observation.SampledAt.IsZero() {
		return ErrInvalidFootprintObservation
	}
	switch observation.Status {
	case FootprintObserved:
		if observation.ErrorCode != "" || !observation.Memory.valid() {
			return ErrInvalidFootprintObservation
		}
	case FootprintUnavailable, FootprintFailed:
		if observation.Memory != (MemoryFootprint{}) || !boundedErrorCode(observation.ErrorCode) {
			return ErrInvalidFootprintObservation
		}
	default:
		return ErrInvalidFootprintObservation
	}
	return nil
}

func (footprint MemoryFootprint) valid() bool {
	if footprint.MappingCount == 0 || footprint.VirtualBytes == 0 || footprint.RSSBytes > footprint.VirtualBytes ||
		footprint.PSSBytes > footprint.RSSBytes || footprint.PrivateCleanBytes > footprint.RSSBytes ||
		footprint.PrivateDirtyBytes > footprint.RSSBytes || footprint.AnonymousBytes > footprint.RSSBytes ||
		footprint.SwapBytes > footprint.VirtualBytes {
		return false
	}
	if footprint.PrivateCleanBytes > ^uint64(0)-footprint.PrivateDirtyBytes ||
		footprint.PrivateCleanBytes+footprint.PrivateDirtyBytes > footprint.RSSBytes {
		return false
	}
	if footprint.RSSBytes > ^uint64(0)-footprint.SwapBytes || footprint.RSSBytes+footprint.SwapBytes > footprint.VirtualBytes {
		return false
	}
	return true
}

func boundedRuntimeIdentifier(value string, maximum int) bool {
	if len(value) == 0 || len(value) > maximum {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '_' && character != '-' && character != '.' && character != ':' {
			return false
		}
	}
	return true
}

func boundedErrorCode(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return true
}

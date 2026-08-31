package semanticspeculation

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

type TreatmentOutcome struct {
	FinalProgramOutcome    string
	FinalPythonStarted     bool
	PrefixPythonExecutions uint32
	ResultSHA256           string
	ErrorClass             string
	LogicalCalls           uint32
	PhysicalAttempts       uint32
	PhysicalResultBytes    uint64
	ProviderCostUnits      uint64
	ReadyBeforeFinalize    uint32
	PhysicalDispositions   PhysicalDispositions
	AuthorityDisposition   string
	WorkspaceDisposition   string
}

type ScheduledTreatment interface {
	Begin(context.Context, json.RawMessage) error
	ObserveChunk(context.Context, string) error
	Finalize(context.Context) (TreatmentOutcome, error)
	Cancel(context.Context) error
}

type ScheduledTreatmentResult struct {
	StartedNanos      uint64
	FinalizeNanos     uint64
	EndedNanos        uint64
	ReleaseDriftNanos []uint64
	Outcome           TreatmentOutcome
}

func RunScheduledTreatment(ctx context.Context, fixture SyntheticCase, treatment ScheduledTreatment) (ScheduledTreatmentResult, error) {
	if ctx == nil || treatment == nil || fixture.Validate() != nil {
		return ScheduledTreatmentResult{}, errors.New("invalid scheduled treatment")
	}
	inputs := append(json.RawMessage(nil), fixture.Inputs...)
	if err := treatment.Begin(ctx, inputs); err != nil {
		_ = treatment.Cancel(ctx)
		return ScheduledTreatmentResult{}, err
	}
	started := time.Now()
	result := ScheduledTreatmentResult{StartedNanos: 1, ReleaseDriftNanos: make([]uint64, 0, len(fixture.Chunks))}
	for _, chunk := range fixture.Chunks {
		due := started.Add(time.Duration(chunk.ReleaseAfterMilliseconds) * time.Millisecond)
		if delay := time.Until(due); delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				_ = treatment.Cancel(ctx)
				return ScheduledTreatmentResult{}, ctx.Err()
			case <-timer.C:
			}
		}
		released := time.Since(due)
		if released < 0 {
			released = 0
		}
		result.ReleaseDriftNanos = append(result.ReleaseDriftNanos, uint64(released))
		if err := treatment.ObserveChunk(ctx, chunk.Source); err != nil {
			_ = treatment.Cancel(ctx)
			return ScheduledTreatmentResult{}, err
		}
	}
	result.FinalizeNanos = elapsedNanos(started)
	outcome, err := treatment.Finalize(ctx)
	result.EndedNanos = elapsedNanos(started)
	if err != nil {
		_ = treatment.Cancel(ctx)
		return ScheduledTreatmentResult{}, err
	}
	result.Outcome = outcome
	return result, nil
}

func elapsedNanos(started time.Time) uint64 {
	elapsed := time.Since(started)
	if elapsed < 0 {
		return 1
	}
	return uint64(elapsed) + 1
}

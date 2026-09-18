package wazero

import (
	"context"
	"errors"
)

const ColdIOEvidenceSchemaVersion = "pysolate.cold-io.v0"

const (
	coldAdviceFailed    = "madvise_cold_failed"
	pageOutAdviceFailed = "madvise_pageout_failed"
)

type ColdIOState string

const (
	ColdIODisabled ColdIOState = "disabled"
	ColdIORunning  ColdIOState = "running"
	ColdIOWaiting  ColdIOState = "waiting"
	ColdIOCold     ColdIOState = "cold"
	ColdIOPageOut  ColdIOState = "pageout"
	ColdIOTerminal ColdIOState = "terminal"
)

var errColdIOState = errors.New("cold I/O continuation state violation")

// ColdIOEvidence is bounded, body-free evidence for the most recent one-shot slot.
type ColdIOEvidence struct {
	SchemaVersion    string      `json:"schema_version"`
	Selected         bool        `json:"selected"`
	State            ColdIOState `json:"state"`
	Waits            uint64      `json:"waits"`
	ColdAttempts     uint64      `json:"cold_attempts"`
	ColdSucceeded    uint64      `json:"cold_succeeded"`
	PageOutAttempts  uint64      `json:"pageout_attempts"`
	PageOutSucceeded uint64      `json:"pageout_succeeded"`
	Resumes          uint64      `json:"resumes"`
	AdvisedBytes     uint64      `json:"advised_bytes"`
	AdviceFailures   uint64      `json:"advice_failures"`
	PressureChecks   uint64      `json:"pressure_checks"`
	PressureHits     uint64      `json:"pressure_hits"`
	PressureErrors   uint64      `json:"pressure_errors"`
	Blockers         []string    `json:"blockers"`
}

func (evidence ColdIOEvidence) Validate() error {
	if evidence.SchemaVersion != ColdIOEvidenceSchemaVersion || evidence.Blockers == nil {
		return errColdIOState
	}
	if !evidence.Selected {
		if evidence.State != ColdIODisabled || evidence.Waits != 0 || evidence.ColdAttempts != 0 ||
			evidence.ColdSucceeded != 0 || evidence.PageOutAttempts != 0 || evidence.PageOutSucceeded != 0 ||
			evidence.Resumes != 0 || evidence.AdvisedBytes != 0 || evidence.AdviceFailures != 0 ||
			evidence.PressureChecks != 0 || evidence.PressureHits != 0 || evidence.PressureErrors != 0 ||
			len(evidence.Blockers) != 0 {
			return errColdIOState
		}
		return nil
	}
	if evidence.State != ColdIORunning && evidence.State != ColdIOWaiting && evidence.State != ColdIOCold &&
		evidence.State != ColdIOPageOut && evidence.State != ColdIOTerminal {
		return errColdIOState
	}
	if evidence.ColdAttempts > evidence.Waits || evidence.PageOutAttempts > evidence.ColdAttempts ||
		evidence.ColdSucceeded > evidence.ColdAttempts || evidence.PageOutSucceeded > evidence.PageOutAttempts ||
		evidence.Resumes > evidence.Waits || evidence.AdviceFailures > evidence.ColdAttempts+evidence.PageOutAttempts ||
		evidence.PressureHits > evidence.PressureChecks || evidence.PressureErrors > evidence.PressureChecks ||
		evidence.ColdSucceeded+evidence.PageOutSucceeded+evidence.AdviceFailures != evidence.ColdAttempts+evidence.PageOutAttempts ||
		(evidence.ColdSucceeded+evidence.PageOutSucceeded == 0) != (evidence.AdvisedBytes == 0) ||
		len(evidence.Blockers) > 2 {
		return errColdIOState
	}
	for index, blocker := range evidence.Blockers {
		if blocker != coldAdviceFailed && blocker != pageOutAdviceFailed {
			return errColdIOState
		}
		if index > 0 && evidence.Blockers[index-1] >= blocker {
			return errColdIOState
		}
	}
	return nil
}

type coldIOCallResult struct {
	payload []byte
	err     error
}

type coldIOContinuation interface {
	wait(context.Context, func(context.Context) ([]byte, error)) ([]byte, error)
	finish() ColdIOEvidence
}

func awaitColdIO(ctx context.Context, continuation coldIOContinuation, call func(context.Context) ([]byte, error)) ([]byte, error) {
	if continuation != nil {
		return continuation.wait(ctx, call)
	}
	return call(ctx)
}

type coldIOContextKey struct{}

func withColdIOContinuation(ctx context.Context, continuation coldIOContinuation) context.Context {
	if continuation == nil {
		return ctx
	}
	return context.WithValue(ctx, coldIOContextKey{}, continuation)
}

func coldIOContinuationFromContext(ctx context.Context) coldIOContinuation {
	continuation, _ := ctx.Value(coldIOContextKey{}).(coldIOContinuation)
	return continuation
}

func (engine *Engine) ColdIOEvidence() ColdIOEvidence {
	if engine == nil {
		return ColdIOEvidence{SchemaVersion: ColdIOEvidenceSchemaVersion, State: ColdIODisabled, Blockers: []string{}}
	}
	engine.coldEvidenceMu.Lock()
	defer engine.coldEvidenceMu.Unlock()
	if engine.coldEvidence.SchemaVersion != "" {
		copy := engine.coldEvidence
		copy.Blockers = append([]string{}, engine.coldEvidence.Blockers...)
		return copy
	}
	state := ColdIODisabled
	if engine.config.Mechanisms.ColdIOContinuation {
		state = ColdIORunning
	}
	return ColdIOEvidence{SchemaVersion: ColdIOEvidenceSchemaVersion, Selected: engine.config.Mechanisms.ColdIOContinuation, State: state, Blockers: []string{}}
}

func (engine *Engine) setColdIOEvidence(evidence ColdIOEvidence) {
	if engine == nil {
		return
	}
	engine.coldEvidenceMu.Lock()
	engine.coldEvidence = evidence
	engine.coldEvidenceMu.Unlock()
}

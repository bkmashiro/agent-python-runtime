package wazero

import (
	"context"
	"errors"
	"sync"
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
	Blockers         []string    `json:"blockers"`
}

func (evidence ColdIOEvidence) Validate() error {
	if evidence.SchemaVersion != ColdIOEvidenceSchemaVersion || evidence.Blockers == nil {
		return errColdIOState
	}
	if !evidence.Selected {
		if evidence.State != ColdIODisabled || evidence.Waits != 0 || evidence.ColdAttempts != 0 ||
			evidence.PageOutAttempts != 0 || evidence.Resumes != 0 || len(evidence.Blockers) != 0 {
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
	return engine.coldEvidence.get(engine.config.Mechanisms.ColdIOContinuation)
}

type coldEvidenceStore struct {
	mu   sync.Mutex
	last ColdIOEvidence
}

func (store *coldEvidenceStore) set(evidence ColdIOEvidence) {
	store.mu.Lock()
	store.last = evidence
	store.mu.Unlock()
}

func (store *coldEvidenceStore) get(selected bool) ColdIOEvidence {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.last.SchemaVersion != "" {
		copy := store.last
		copy.Blockers = append([]string{}, store.last.Blockers...)
		return copy
	}
	state := ColdIODisabled
	if selected {
		state = ColdIORunning
	}
	return ColdIOEvidence{SchemaVersion: ColdIOEvidenceSchemaVersion, Selected: selected, State: state, Blockers: []string{}}
}

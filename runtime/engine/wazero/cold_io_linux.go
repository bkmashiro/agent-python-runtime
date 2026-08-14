//go:build linux

package wazero

import (
	"context"
	"sync"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"golang.org/x/sys/unix"
)

type linuxColdIOContinuation struct {
	mu       sync.Mutex
	memory   *cowLinearMemory
	policy   runtimeconfig.ColdIOPolicy
	evidence ColdIOEvidence
}

func newColdIOContinuation(memory *cowLinearMemory, policy runtimeconfig.ColdIOPolicy) (*linuxColdIOContinuation, error) {
	if memory == nil || policy.ColdAfter <= 0 || policy.PageOutAfter < 0 ||
		(policy.PageOutAfter != 0 && policy.PageOutAfter <= policy.ColdAfter) {
		return nil, errColdIOState
	}
	return &linuxColdIOContinuation{
		memory: memory,
		policy: policy,
		evidence: ColdIOEvidence{
			SchemaVersion: ColdIOEvidenceSchemaVersion,
			Selected:      true,
			State:         ColdIORunning,
			Blockers:      []string{},
		},
	}, nil
}

func (continuation *linuxColdIOContinuation) beginWait() error {
	continuation.mu.Lock()
	defer continuation.mu.Unlock()
	if continuation.evidence.State != ColdIORunning {
		return errColdIOState
	}
	continuation.evidence.State = ColdIOWaiting
	continuation.evidence.Waits++
	return nil
}

func (continuation *linuxColdIOContinuation) wait(ctx context.Context, call func(context.Context) ([]byte, error)) ([]byte, error) {
	if call == nil {
		return nil, errColdIOState
	}
	if err := continuation.beginWait(); err != nil {
		return nil, err
	}
	results := make(chan coldIOCallResult, 1)
	go func() {
		payload, err := call(ctx)
		results <- coldIOCallResult{payload: payload, err: err}
	}()
	coldTimer := time.NewTimer(continuation.policy.ColdAfter)
	defer coldTimer.Stop()
	var pageOutTimer *time.Timer
	var pageOut <-chan time.Time
	defer func() {
		if pageOutTimer != nil {
			pageOutTimer.Stop()
		}
	}()
	cold := coldTimer.C
	for {
		select {
		case result := <-results:
			continuation.resume()
			return result.payload, result.err
		case <-ctx.Done():
			// The request was copied before wait; release the slot even if a
			// misbehaving Host handler ignores cancellation. Its late result is
			// discarded and can never resume or replay this execution.
			continuation.resume()
			return nil, ctx.Err()
		case <-cold:
			continuation.advise(unix.MADV_COLD, ColdIOCold, coldAdviceFailed)
			cold = nil
			if continuation.policy.PageOutAfter != 0 {
				pageOutTimer = time.NewTimer(continuation.policy.PageOutAfter - continuation.policy.ColdAfter)
				pageOut = pageOutTimer.C
			}
		case <-pageOut:
			continuation.advise(unix.MADV_PAGEOUT, ColdIOPageOut, pageOutAdviceFailed)
			pageOut = nil
		}
	}
}

func (continuation *linuxColdIOContinuation) advise(advice int, state ColdIOState, blocker string) {
	bytes, err := continuation.memory.advise(advice)
	continuation.mu.Lock()
	defer continuation.mu.Unlock()
	if state == ColdIOCold {
		continuation.evidence.ColdAttempts++
	} else {
		continuation.evidence.PageOutAttempts++
	}
	if err != nil {
		continuation.evidence.AdviceFailures++
		if !containsString(continuation.evidence.Blockers, blocker) {
			continuation.evidence.Blockers = append(continuation.evidence.Blockers, blocker)
		}
		return
	}
	if state == ColdIOCold {
		continuation.evidence.ColdSucceeded++
	} else {
		continuation.evidence.PageOutSucceeded++
	}
	continuation.evidence.AdvisedBytes += bytes
	continuation.evidence.State = state
}

func (continuation *linuxColdIOContinuation) resume() {
	continuation.mu.Lock()
	defer continuation.mu.Unlock()
	if continuation.evidence.State == ColdIOTerminal {
		return
	}
	continuation.evidence.State = ColdIORunning
	continuation.evidence.Resumes++
}

func (continuation *linuxColdIOContinuation) finish() ColdIOEvidence {
	continuation.mu.Lock()
	defer continuation.mu.Unlock()
	continuation.evidence.State = ColdIOTerminal
	copy := continuation.evidence
	copy.Blockers = append([]string{}, continuation.evidence.Blockers...)
	return copy
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

var _ coldIOContinuation = (*linuxColdIOContinuation)(nil)

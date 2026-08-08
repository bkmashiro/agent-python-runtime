package hermesbridge

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/bkmashiro/agent-python-runtime/agenttrace"
	"github.com/bkmashiro/agent-python-runtime/claimmanifest"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
)

type TraceManager struct {
	mu        sync.Mutex
	store     *agenttrace.SQLiteStore
	plugin    agenttrace.Plugin
	recorders map[string]*agenttrace.Recorder
}

func NewTraceManager(store *agenttrace.SQLiteStore) (*TraceManager, error) {
	if store == nil {
		return nil, agenttrace.ErrInvalidPlugin
	}
	return &TraceManager{
		store: store, plugin: agenttrace.Plugin{Mode: agenttrace.ModeRequired, Sink: store},
		recorders: map[string]*agenttrace.Recorder{},
	}, nil
}

func (manager *TraceManager) Observe(ctx context.Context, request ObserveRequest) (agenttrace.Event, error) {
	if manager == nil || request.Validate() != nil {
		return agenttrace.Event{}, agenttrace.ErrInvalidEvent
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	recorder, err := manager.recorderLocked(ctx, request.AgentRunID)
	if err != nil {
		return agenttrace.Event{}, err
	}
	return recorder.Record(
		ctx,
		request.EventType,
		request.ParentEventID,
		request.Payload,
		request.StateFingerprint,
	)
}

func (manager *TraceManager) RuntimeStarted(ctx context.Context, ref runtimeconfig.InvocationRef, requestDigest string) (string, error) {
	if manager == nil || ref.Validate() != nil || !validDigest(requestDigest) {
		return "", agenttrace.ErrInvalidEvent
	}
	payload, err := json.Marshal(map[string]any{
		"invocation_id": ref.InvocationID, "invocation_attempt": ref.InvocationAttempt,
		"execution_id": ref.ExecutionID, "request_digest": requestDigest,
		"turn_seq": ref.TurnSeq, "output_item_seq": ref.OutputItemSeq, "segment_seq": ref.SegmentSeq,
	})
	if err != nil {
		return "", agenttrace.ErrInvalidEvent
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	recorder, err := manager.recorderLocked(ctx, ref.AgentRunID)
	if err != nil {
		return "", err
	}
	event, err := recorder.Record(ctx, agenttrace.EventRuntimeStarted, "", payload, "")
	if err != nil {
		return "", err
	}
	return event.EventID, nil
}

func (manager *TraceManager) RuntimeCompleted(ctx context.Context, parentEventID string, ref runtimeconfig.ExecutionRef, status, resultDigest string) error {
	if manager == nil || ref.Validate() != nil || !boundedIdentifier(parentEventID, 160) ||
		!boundedIdentifier(status, 64) || (resultDigest != "" && !validDigest(resultDigest)) {
		return agenttrace.ErrInvalidEvent
	}
	payloadDocument := map[string]any{
		"invocation_id": ref.InvocationID, "invocation_attempt": ref.InvocationAttempt,
		"execution_id": ref.ExecutionID, "executed_code_sha256": ref.ExecutedCodeSHA256, "status": status,
		"turn_seq": ref.TurnSeq, "output_item_seq": ref.OutputItemSeq, "segment_seq": ref.SegmentSeq,
	}
	if resultDigest != "" {
		payloadDocument["result_digest"] = resultDigest
	}
	payload, err := json.Marshal(payloadDocument)
	if err != nil {
		return agenttrace.ErrInvalidEvent
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	recorder, err := manager.recorderLocked(ctx, ref.AgentRunID)
	if err != nil {
		return err
	}
	_, err = recorder.Record(ctx, agenttrace.EventRuntimeCompleted, parentEventID, payload, "")
	return err
}

// ClaimManifest projects the persisted metadata trace into claim-scoped
// verification results. It never upgrades metadata-only evidence beyond R0.
func (manager *TraceManager) ClaimManifest(ctx context.Context, ref runtimeconfig.ExecutionRef) (claimmanifest.Manifest, error) {
	if manager == nil || manager.store == nil || ref.Validate() != nil {
		return claimmanifest.Manifest{}, claimmanifest.ErrExecutionNotObserved
	}
	playback, err := manager.store.LoadPlayback(ctx, ref.AgentRunID)
	if err != nil {
		return claimmanifest.Manifest{}, err
	}
	return claimmanifest.FromMetadataPlayback(ref, playback)
}

func (manager *TraceManager) recorderLocked(ctx context.Context, agentRunID string) (*agenttrace.Recorder, error) {
	if recorder := manager.recorders[agentRunID]; recorder != nil {
		return recorder, nil
	}
	playback, err := manager.store.LoadPlayback(ctx, agentRunID)
	var recorder *agenttrace.Recorder
	switch {
	case err == nil:
		recorder, err = manager.plugin.Resume(playback, nil)
	case errors.Is(err, agenttrace.ErrInvalidEvent):
		recorder, err = manager.plugin.Begin(agentRunID, nil)
	default:
		return nil, err
	}
	if err != nil {
		return nil, err
	}
	manager.recorders[agentRunID] = recorder
	return recorder, nil
}

func validDigest(value string) bool {
	if len(value) != len("sha256:")+64 || value[:len("sha256:")] != "sha256:" {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

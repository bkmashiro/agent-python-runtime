package agentic

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/bkmashiro/agent-python-runtime/agenttrace"
)

type trialTrace struct {
	recorder *agenttrace.Recorder
	parentID string
}

func beginTrialTrace(ctx context.Context, result TrialResult) (*trialTrace, error) {
	plugin, ok := agenttrace.PluginFromContext(ctx)
	if !ok {
		return nil, nil
	}
	recorder, err := plugin.Begin(result.TrialID, nil)
	if err != nil {
		return nil, err
	}
	trace := &trialTrace{recorder: recorder}
	if err := trace.record(ctx, agenttrace.EventRunStarted, map[string]any{
		"version": result.Version, "spec_digest": result.SpecDigest, "task_digest": result.TaskDigest,
		"condition": result.Condition, "model": result.Model, "replicate": result.Replicate,
		"prompt_digest": result.PromptDigest, "surface_digest": result.SurfaceDigest,
	}, ""); err != nil {
		return nil, err
	}
	return trace, nil
}

func (trace *trialTrace) record(ctx context.Context, eventType agenttrace.EventType, payload map[string]any, stateFingerprint string) error {
	if trace == nil || trace.recorder == nil {
		return nil
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return ErrAgenticRun
	}
	event, err := trace.recorder.Record(ctx, eventType, trace.parentID, encoded, stateFingerprint)
	if err != nil {
		return err
	}
	if event.EventID != "" {
		trace.parentID = event.EventID
	}
	return nil
}

func (trace *trialTrace) complete(ctx context.Context, result TrialResult, runErr error) error {
	if trace == nil {
		return nil
	}
	status := "ok"
	if runErr != nil || result.ErrorCode != "" || !result.Passed {
		status = "error"
	}
	return trace.record(ctx, agenttrace.EventRunCompleted, map[string]any{
		"status": status, "error_code": result.ErrorCode, "passed": result.Passed,
		"provider_calls": result.ProviderCalls, "tool_calls": result.ToolCalls, "python_runs": result.PythonRuns,
		"final_state_digest": result.FinalStateDigest, "trace_dropped": trace.recorder.Dropped(),
		"run_error": runErr != nil,
	}, result.FinalStateDigest)
}

func traceDigest(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return digest(encoded), nil
}

func joinTraceError(runErr, traceErr error) error {
	if traceErr == nil {
		return runErr
	}
	if runErr == nil {
		return traceErr
	}
	return errors.Join(runErr, traceErr)
}

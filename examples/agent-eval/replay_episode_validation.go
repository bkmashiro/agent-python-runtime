package main

import (
	"bytes"
	"encoding/json"
	"errors"
)

func validateRecordedEpisode(ep privateEpisodeRecording, header privateRecordingHeader, task taskCase) error {
	if ep.Partial || ep.Seed == "" || ep.FixtureVersion != header.FixtureVersion || ep.MaxTurns != header.MaxTurns || ep.MaxTokens != maxTokensPerRequest {
		return errors.New("partial or incompatible episode")
	}
	if ep.Row.CompletionStatus == "timeout" {
		return errors.New("wall-clock timeout capture retained but not supported for deterministic replay")
	}
	if ep.ReserveRequests < 0 || ep.ReserveRequests > 1 {
		return errors.New("invalid recorded request reservation")
	}
	if ep.Arm != "direct" && ep.Arm != "code" {
		return errors.New("unknown recorded arm")
	}
	if ep.Task != ep.Row.Task || ep.Arm != ep.Row.Arm || ep.Repeat != ep.Row.Repeat || ep.Sequence != ep.Row.Sequence {
		return errors.New("episode identity mismatch")
	}
	if ep.Row.CompletionStatus == "skipped" {
		if len(ep.ProviderCalls)+len(ep.DomainCalls)+len(ep.Executions) != 0 {
			return errors.New("skipped episode contains execution")
		}
		return nil
	}
	if ep.Row.ModelRequests != len(ep.ProviderCalls) || ep.Row.ExecutionAttempts != len(ep.Executions) {
		return errors.New("recording counts differ")
	}
	fixture := task.NewEpisode()
	bindings := make([]privateToolBinding, 0, len(fixture.Domains))
	for _, d := range fixture.Domains {
		bindings = append(bindings, privateToolBinding{Name: d.Name, PythonPath: d.PythonPath, Description: d.Description, Schema: d.Schema})
	}
	want, _ := json.Marshal(bindings)
	got, _ := json.Marshal(ep.DomainBindings)
	if !bytes.Equal(want, got) {
		return errors.New("tool bindings differ from recorded environment")
	}
	for _, call := range ep.ProviderCalls {
		if err := validateProviderExchange(call, header.RequestedModel); err != nil {
			return err
		}
	}
	if len(ep.ModelToolCalls) != len(ep.Row.Trace) {
		return errors.New("model tool trace count differs")
	}
	for i, call := range ep.ModelToolCalls {
		trace := ep.Row.Trace[i]
		if call.Turn != trace.Turn || call.Tool != trace.Tool || call.Arguments != trace.Arguments || call.Result != trace.Result {
			return errors.New("model tool trace differs")
		}
		if call.Turn < 1 || call.Turn > len(ep.ProviderCalls) {
			return errors.New("tool call has no provider response")
		}
		found := false
		for _, tool := range ep.ProviderCalls[call.Turn-1].Response.Message.ToolCalls {
			if tool.ID == call.CallID && tool.Function.Name == call.Tool && tool.Function.Arguments == call.Arguments {
				found = true
			}
		}
		if !found {
			return errors.New("tool call not bound to provider response")
		}
	}
	for i, call := range ep.DomainCalls {
		if !call.Completed || call.Scope != "direct" || call.Sequence != i || len(call.Result) == 0 {
			return errors.New("domain history incomplete")
		}
	}
	if ep.Arm == "direct" && len(ep.Executions) != 0 {
		return errors.New("direct recording has Python execution")
	}
	if ep.Arm == "code" && len(ep.DomainCalls) != 0 {
		return errors.New("code recording has direct domain calls")
	}
	for i, execution := range ep.Executions {
		if !execution.Completed || execution.Attempt != i+1 || len(execution.Source) > maxSourceBytes || !bytes.Equal(execution.Inputs, []byte("null")) {
			return errors.New("execution incomplete or unsupported")
		}
		for j, call := range execution.ToolCalls {
			if !call.Completed || call.Scope != "python" || call.Attempt != execution.Attempt || call.Sequence != j || len(call.Result) == 0 {
				return errors.New("execution tool history incomplete")
			}
		}
	}
	return nil
}

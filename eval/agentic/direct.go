package agentic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/bkmashiro/agent-python-runtime/eval/provider"
)

var ErrAgenticRun = errors.New("external agentic run failed")

const (
	maxDirectOutputTokens = 8192
	maxFunctionCalls      = 128
	maxArgumentsBytes     = 64 * 1024
)

type DirectResult struct {
	TaskID         string          `json:"task_id"`
	Model          string          `json:"model"`
	Passed         bool            `json:"passed"`
	ErrorCode      string          `json:"error_code,omitempty"`
	CallCount      int             `json:"call_count"`
	StatusCode     int             `json:"status_code"`
	RequestDigest  string          `json:"request_digest"`
	ResponseDigest string          `json:"response_digest"`
	Usage          *provider.Usage `json:"usage,omitempty"`
	UsageUnknown   bool            `json:"usage_unknown"`
}

func RunDirectStateless(ctx context.Context, adapter provider.Adapter, task Task, model string, maxOutputTokens uint64) (DirectResult, error) {
	if adapter == nil || adapter.Protocol() != provider.LinkAPIResponsesProtocol || task.Split != "dev" ||
		task.Track != "stateless_function_calling" || task.Interaction.Mode != "single_turn" || len(task.Interaction.Turns) != 1 ||
		len(task.Tools) == 0 || len(task.Tools) > maxFunctionCalls ||
		model == "" || maxOutputTokens == 0 || maxOutputTokens > maxDirectOutputTokens {
		return DirectResult{}, ErrAgenticRun
	}
	var input any
	if decodeUseNumber(task.Interaction.Turns[0], &input) != nil {
		return DirectResult{}, ErrAgenticRun
	}
	tools := make([]map[string]any, 0, len(task.Tools))
	seenNames := map[string]struct{}{}
	for _, tool := range task.Tools {
		name, err := ProviderToolName(tool.Name)
		if err != nil {
			return DirectResult{}, ErrAgenticRun
		}
		if _, exists := seenNames[name]; exists {
			return DirectResult{}, ErrAgenticRun
		}
		seenNames[name] = struct{}{}
		var parameters any
		if decodeUseNumber(tool.Parameters, &parameters) != nil {
			return DirectResult{}, ErrAgenticRun
		}
		tools = append(tools, map[string]any{
			"type": "function", "name": name, "description": tool.Description,
			"parameters": parameters, "strict": false,
		})
	}
	payload, err := json.Marshal(map[string]any{
		"model": model, "input": input, "tools": tools, "tool_choice": "required",
		"parallel_tool_calls": true, "max_output_tokens": maxOutputTokens,
		"stream": false, "background": false,
	})
	if err != nil {
		return DirectResult{}, ErrAgenticRun
	}
	response, err := adapter.Exchange(ctx, provider.Request{Model: model, Payload: payload})
	if err != nil {
		return DirectResult{}, fmt.Errorf("%w: provider exchange", ErrAgenticRun)
	}
	calls, err := parseResponseFunctionCalls(response.Body)
	if err != nil {
		return DirectResult{}, err
	}
	score := ScoreStatelessCalls(task, calls)
	result := DirectResult{
		TaskID: task.ID, Model: model, Passed: score.Passed, ErrorCode: score.ErrorCode,
		CallCount: len(calls), StatusCode: response.StatusCode,
		RequestDigest: response.RequestDigest, ResponseDigest: response.ResponseDigest,
		Usage: cloneUsage(response.Usage), UsageUnknown: response.Usage == nil,
	}
	return result, nil
}

func parseResponseFunctionCalls(body json.RawMessage) ([]FunctionCall, error) {
	var envelope struct {
		Output []json.RawMessage `json:"output"`
	}
	if decodeUseNumber(body, &envelope) != nil || len(envelope.Output) == 0 || len(envelope.Output) > maxFunctionCalls*2 {
		return nil, ErrAgenticRun
	}
	calls := make([]FunctionCall, 0)
	callIDs := map[string]struct{}{}
	for _, raw := range envelope.Output {
		var header struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &header) != nil {
			return nil, ErrAgenticRun
		}
		if header.Type != "function_call" {
			continue
		}
		var item struct {
			Type      string `json:"type"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}
		if json.Unmarshal(raw, &item) != nil || item.CallID == "" || len(item.CallID) > 256 || !providerToolNamePattern.MatchString(item.Name) ||
			len(item.Arguments) == 0 || len(item.Arguments) > maxArgumentsBytes {
			return nil, ErrAgenticRun
		}
		if _, exists := callIDs[item.CallID]; exists {
			return nil, ErrAgenticRun
		}
		callIDs[item.CallID] = struct{}{}
		var arguments map[string]any
		decoder := json.NewDecoder(bytes.NewReader([]byte(item.Arguments)))
		decoder.UseNumber()
		if decoder.Decode(&arguments) != nil || arguments == nil {
			return nil, ErrAgenticRun
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			return nil, ErrAgenticRun
		}
		calls = append(calls, FunctionCall{Name: item.Name, Arguments: json.RawMessage(item.Arguments)})
		if len(calls) > maxFunctionCalls {
			return nil, ErrAgenticRun
		}
	}
	if len(calls) == 0 {
		return nil, ErrAgenticRun
	}
	return calls, nil
}

func cloneUsage(usage *provider.Usage) *provider.Usage {
	if usage == nil {
		return nil
	}
	copy := *usage
	return &copy
}

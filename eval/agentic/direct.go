package agentic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

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
	providerToCanonical := make(map[string]string, len(task.Tools))
	for _, tool := range task.Tools {
		name, err := ProviderToolName(tool.Name)
		if err != nil {
			return DirectResult{}, ErrAgenticRun
		}
		if _, exists := seenNames[name]; exists {
			return DirectResult{}, ErrAgenticRun
		}
		seenNames[name] = struct{}{}
		providerToCanonical[name] = tool.Name
		var parameters any
		if decodeUseNumber(tool.Parameters, &parameters) != nil {
			return DirectResult{}, ErrAgenticRun
		}
		tools = append(tools, map[string]any{
			"type": "function", "name": name, "description": tool.Description,
			"parameters": parameters, "strict": false,
		})
	}
	payload, err := marshalLinkAPIWirePayload(model, input, tools, "required", true, maxOutputTokens)
	if err != nil {
		return DirectResult{}, ErrAgenticRun
	}
	response, err := adapter.Exchange(ctx, provider.Request{Model: model, Payload: payload})
	if err != nil {
		return DirectResult{}, fmt.Errorf("%w: provider exchange", ErrAgenticRun)
	}
	parsed, err := ParseResponsesOutput(response.Body, providerToCanonical)
	if err != nil || len(parsed.Calls) == 0 {
		return DirectResult{}, ErrAgenticRun
	}
	calls := make([]FunctionCall, len(parsed.Calls))
	for index, call := range parsed.Calls {
		calls[index] = FunctionCall{Name: call.CanonicalName, Arguments: append(json.RawMessage(nil), call.Arguments...)}
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

func cloneUsage(usage *provider.Usage) *provider.Usage {
	if usage == nil {
		return nil
	}
	copy := *usage
	return &copy
}

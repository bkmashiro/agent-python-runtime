package agentic

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/bkmashiro/agent-python-runtime/eval/provider"
)

type HybridRoute string
type HybridRouteReason string

const (
	HybridRouteDirect HybridRoute = "direct"
	HybridRoutePython HybridRoute = "python"
)

const (
	HybridReasonKnownArguments   HybridRouteReason = "known_arguments"
	HybridReasonOutputDependency HybridRouteReason = "output_dependency"
	HybridReasonIteration        HybridRouteReason = "iteration"
	HybridReasonBranching        HybridRouteReason = "branching"
	HybridReasonAggregation      HybridRouteReason = "aggregation"
	HybridReasonTransformation   HybridRouteReason = "transformation"
)

type HybridRouteDecision struct {
	Route                  HybridRoute       `json:"route"`
	ReasonCode             HybridRouteReason `json:"reason_code"`
	RouterPromptDigest     string            `json:"router_prompt_digest"`
	RouterSurfaceDigest    string            `json:"router_surface_digest"`
	ExecutionPromptDigest  string            `json:"execution_prompt_digest"`
	ExecutionSurfaceDigest string            `json:"execution_surface_digest"`
	RouterUsage            provider.Usage    `json:"router_usage"`
	ExecutionUsage         provider.Usage    `json:"execution_usage"`
}

func DecideHybridRoute(ctx context.Context, session *ResponsesSession, task Task) (HybridRouteDecision, error) {
	if session == nil {
		return HybridRouteDecision{}, ErrAgenticRun
	}
	prompt, surface, promptDigest, surfaceDigest, err := buildHybridRouterContract(task)
	if err != nil {
		return HybridRouteDecision{}, err
	}
	messages, err := decodeBenchmarkTurn(task.Interaction.Turns[0])
	if err != nil {
		return HybridRouteDecision{}, err
	}
	history := []any{map[string]any{"role": "developer", "content": prompt}}
	for _, message := range messages {
		history = append(history, map[string]any{"role": message.Role, "content": message.Content})
	}
	parsed, err := session.Exchange(ctx, history, surface, "required", false, map[string]string{"select_execution_surface": "select_execution_surface"})
	if err != nil {
		return HybridRouteDecision{}, err
	}
	if parsed.Refused || parsed.TextDigest != "" || len(parsed.Calls) != 1 || parsed.Calls[0].CanonicalName != "select_execution_surface" {
		return HybridRouteDecision{}, ErrAgenticRun
	}
	var arguments struct {
		Surface    HybridRoute       `json:"surface"`
		ReasonCode HybridRouteReason `json:"reason_code"`
	}
	if decodeStrict(parsed.Calls[0].Arguments, &arguments) != nil ||
		(arguments.Surface != HybridRouteDirect && arguments.Surface != HybridRoutePython) || !arguments.ReasonCode.valid() {
		return HybridRouteDecision{}, ErrAgenticRun
	}
	return HybridRouteDecision{
		Route: arguments.Surface, ReasonCode: arguments.ReasonCode,
		RouterPromptDigest: promptDigest, RouterSurfaceDigest: surfaceDigest,
		RouterUsage: session.Usage(),
	}, nil
}

func buildHybridRouterContract(task Task) (string, []map[string]any, string, string, error) {
	if task.Split != "dev" || len(task.Interaction.Turns) == 0 || len(task.Tools) == 0 {
		return "", nil, "", "", ErrAgenticRun
	}
	toolNames := make([]string, len(task.Tools))
	for index, tool := range task.Tools {
		if tool.Name == "" {
			return "", nil, "", "", ErrAgenticRun
		}
		toolNames[index] = tool.Name
	}
	prompt := "Choose exactly one execution surface for this trial using only the first user turn. Choose direct when required Host calls are independent or their arguments are already known. Choose python only when a later argument or control-flow decision depends on a Host-tool result, or bounded local computation materially simplifies the workflow. Cost alone is not a reason to choose python. Return one bounded reason code. Available Host tools: " + strings.Join(toolNames, ", ")
	surface := []map[string]any{{
		"type": "function", "name": "select_execution_surface", "description": "Select the only execution surface that will be exposed for this trial.",
		"strict": true,
		"parameters": map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{
				"surface":     map[string]any{"type": "string", "enum": []string{"direct", "python"}},
				"reason_code": map[string]any{"type": "string", "enum": []string{"known_arguments", "output_dependency", "iteration", "branching", "aggregation", "transformation"}},
			},
			"required": []string{"surface", "reason_code"},
		},
	}}
	surfaceDigest, err := digestJSON(surface)
	if err != nil {
		return "", nil, "", "", ErrAgenticRun
	}
	return prompt, surface, digest([]byte(prompt)), surfaceDigest, nil
}

func (reason HybridRouteReason) valid() bool {
	switch reason {
	case HybridReasonKnownArguments, HybridReasonOutputDependency, HybridReasonIteration, HybridReasonBranching, HybridReasonAggregation, HybridReasonTransformation:
		return true
	default:
		return false
	}
}

func digestJSON(value any) (string, error) {
	bytes, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return digest(bytes), nil
}

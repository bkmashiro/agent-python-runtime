package agentic

import (
	"context"
	"encoding/json"
	"strings"
)

type HybridRoute string

const (
	HybridRouteDirect HybridRoute = "direct"
	HybridRoutePython HybridRoute = "python"
)

type HybridRouteDecision struct {
	Route         HybridRoute `json:"route"`
	PromptDigest  string      `json:"prompt_digest"`
	SurfaceDigest string      `json:"surface_digest"`
}

func DecideHybridRoute(ctx context.Context, session *ResponsesSession, task Task) (HybridRouteDecision, error) {
	if session == nil || task.Split != "dev" || len(task.Interaction.Turns) == 0 || len(task.Tools) == 0 {
		return HybridRouteDecision{}, ErrAgenticRun
	}
	toolNames := make([]string, len(task.Tools))
	for index, tool := range task.Tools {
		if tool.Name == "" {
			return HybridRouteDecision{}, ErrAgenticRun
		}
		toolNames[index] = tool.Name
	}
	prompt := "Choose exactly one execution surface for the current user turn. Choose direct when the required Host calls are independent or their arguments are already known. Choose python only when later arguments or control flow depend on earlier Host results, or bounded local computation materially simplifies the workflow. Cost alone is not a reason to choose python. Available Host tools: " + strings.Join(toolNames, ", ")
	surface := []map[string]any{{
		"type": "function", "name": "select_execution_surface", "description": "Select the only execution surface that will be exposed for this trial.",
		"strict": true,
		"parameters": map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{"surface": map[string]any{"type": "string", "enum": []string{"direct", "python"}}},
			"required":   []string{"surface"},
		},
	}}
	messages, err := decodeBenchmarkTurn(task.Interaction.Turns[0])
	if err != nil {
		return HybridRouteDecision{}, err
	}
	history := []any{map[string]any{"role": "developer", "content": prompt}}
	for _, message := range messages {
		history = append(history, map[string]any{"role": message.Role, "content": message.Content})
	}
	parsed, err := session.Exchange(ctx, history, surface, "required", false, map[string]string{"select_execution_surface": "select_execution_surface"})
	if err != nil || parsed.Refused || parsed.TextDigest != "" || len(parsed.Calls) != 1 || parsed.Calls[0].CanonicalName != "select_execution_surface" {
		return HybridRouteDecision{}, ErrAgenticRun
	}
	var arguments struct {
		Surface HybridRoute `json:"surface"`
	}
	if decodeStrict(parsed.Calls[0].Arguments, &arguments) != nil || (arguments.Surface != HybridRouteDirect && arguments.Surface != HybridRoutePython) {
		return HybridRouteDecision{}, ErrAgenticRun
	}
	surfaceBytes, err := json.Marshal(surface)
	if err != nil {
		return HybridRouteDecision{}, ErrAgenticRun
	}
	return HybridRouteDecision{Route: arguments.Surface, PromptDigest: digest([]byte(prompt)), SurfaceDigest: digest(surfaceBytes)}, nil
}

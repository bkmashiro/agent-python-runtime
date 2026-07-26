package agentic

import (
	"encoding/json"
)

const (
	maxHostContextBytes         = 4 * 1024
	maxHostContextArgumentBytes = 128
	maxHostContextEffects       = 16
)

type HostEffect struct {
	Tool            string          `json:"tool"`
	Arguments       json.RawMessage `json:"arguments,omitempty"`
	ArgumentsDigest string          `json:"arguments_digest,omitempty"`
}

type HostContext struct {
	Version           string       `json:"version"`
	Turn              int          `json:"turn"`
	CWD               string       `json:"cwd"`
	SuccessfulEffects []HostEffect `json:"successful_effects"`
	OmittedEffects    int          `json:"omitted_effects"`
}

func BuildHostContext(runtime *ToolRuntime, turn int) (HostContext, json.RawMessage, error) {
	if runtime == nil || runtime.filesystem == nil || turn <= 0 || turn >= len(runtime.trace) {
		return HostContext{}, nil, ErrAgenticRun
	}
	cwd, err := runtime.filesystem.CurrentWorkingDirectory()
	if err != nil {
		return HostContext{}, nil, ErrAgenticRun
	}
	rawTrace := runtime.RawTrace()
	effects := make([]HostEffect, 0, maxHostContextEffects)
	for priorTurn := 0; priorTurn < turn; priorTurn++ {
		for _, call := range rawTrace[priorTurn] {
			if call.Error == "" && reversibleFilesystemTools[call.Name] {
				effects = append(effects, hostEffectFromCall(call))
			}
		}
	}
	omitted := 0
	if len(effects) > maxHostContextEffects {
		omitted = len(effects) - maxHostContextEffects
		first := append([]HostEffect(nil), effects[:maxHostContextEffects/2]...)
		effects = append(first, effects[len(effects)-maxHostContextEffects/2:]...)
	}
	projection := HostContext{
		Version: "agentic-host-context/v1", Turn: turn, CWD: cwd,
		SuccessfulEffects: effects, OmittedEffects: omitted,
	}
	encoded, err := json.Marshal(projection)
	if err != nil || len(encoded) > maxHostContextBytes {
		return HostContext{}, nil, ErrAgenticRun
	}
	return projection, json.RawMessage(encoded), nil
}

func hostEffectFromCall(call RawToolCall) HostEffect {
	effect := HostEffect{Tool: call.Name}
	var arguments any
	if decodeUseNumber(call.Arguments, &arguments) != nil {
		effect.ArgumentsDigest = digest(call.Arguments)
		return effect
	}
	canonical, err := json.Marshal(arguments)
	if err != nil || len(canonical) > maxHostContextArgumentBytes {
		effect.ArgumentsDigest = digest(canonical)
		return effect
	}
	effect.Arguments = json.RawMessage(canonical)
	return effect
}

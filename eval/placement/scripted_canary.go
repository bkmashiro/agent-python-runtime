package placement

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/bkmashiro/agent-python-runtime/eval/agentic"
)

var ErrScriptedCanaryCase = errors.New("invalid placement scripted canary case")

type ScriptedCanaryCase struct {
	Tools              []agentic.ScriptedTool
	Calls              []agentic.ScriptedExpectedCall
	PythonSource       string
	JavaScriptSource   string
	InitialFiles       map[string]string
	OutputFiles        []string
	ExpectedResult     json.RawMessage
	ObservedFinalState json.RawMessage
}

type scriptedEffectContract struct {
	Kind          string         `json:"kind"`
	Required      []SemanticCall `json:"required"`
	Forbidden     []string       `json:"forbidden"`
	OrderingEdges [][]int        `json:"ordering_edges"`
}

type scriptedEnvironment struct {
	Kind    string            `json:"kind"`
	Files   map[string]string `json:"files"`
	Inputs  json.RawMessage   `json:"inputs"`
	Fixture *struct {
		InitialStateDigest string `json:"initial_state_digest"`
		Seed               int64  `json:"seed"`
		SetupID            string `json:"setup_id"`
		FaultPlanID        any    `json:"fault_plan_id"`
	} `json:"fixture"`
}

type scriptedFinalState struct {
	Kind                        string                     `json:"kind"`
	Files                       map[string]string          `json:"files"`
	ExpectedBusinessStateDigest string                     `json:"expected_business_state_digest"`
	ExpectedResultDigest        string                     `json:"expected_result_digest"`
	ExpectedTerminalState       string                     `json:"expected_terminal_state"`
	OutputSchema                map[string]json.RawMessage `json:"output_schema"`
	SafetyAssertions            []string                   `json:"safety_assertions"`
}

func CompileScriptedCanaryCase(task Task) (ScriptedCanaryCase, error) {
	if task.Split != "development" || task.ID == "" {
		return ScriptedCanaryCase{}, ErrScriptedCanaryCase
	}
	admitted := false
	for _, arm := range []string{"direct", "pysolate", "computer"} {
		admitted = admitted || task.Admission[arm].Status == "admitted"
	}
	if !admitted {
		return ScriptedCanaryCase{}, nil
	}
	var contract scriptedEffectContract
	var environment scriptedEnvironment
	var final scriptedFinalState
	if decodeStrict(task.Oracle.EffectContract, &contract) != nil || contract.Kind != "exact_semantic_calls" || len(contract.Required) == 0 ||
		decodeStrict(task.Environment, &environment) != nil || decodeStrict(task.Oracle.FinalState, &final) != nil {
		return ScriptedCanaryCase{}, ErrScriptedCanaryCase
	}
	compiled := ScriptedCanaryCase{InitialFiles: cloneStringMap(environment.Files)}
	if environment.Kind == "fixture_state" {
		result, err := fixtureResult(final)
		if err != nil || environment.Fixture == nil || environment.Fixture.InitialStateDigest != final.ExpectedBusinessStateDigest {
			return ScriptedCanaryCase{}, ErrScriptedCanaryCase
		}
		compiled.ExpectedResult = result
	}
	seenTools := map[string]bool{}
	finalFiles := cloneStringMap(environment.Files)
	for index, call := range contract.Required {
		if call.Name == "" || !scriptedJSONObject(call.Arguments) {
			return ScriptedCanaryCase{}, ErrScriptedCanaryCase
		}
		result, err := scriptedCallResult(call, environment, compiled.ExpectedResult, index == len(contract.Required)-1)
		if err != nil {
			return ScriptedCanaryCase{}, err
		}
		compiled.Calls = append(compiled.Calls, agentic.ScriptedExpectedCall{Name: call.Name, Arguments: append(json.RawMessage(nil), call.Arguments...), Result: result})
		if !seenTools[call.Name] {
			schema, err := schemaForArguments(call.Arguments)
			if err != nil {
				return ScriptedCanaryCase{}, err
			}
			effectClass := "read_only"
			if call.Name == "workspace.write_text" || strings.Contains(call.Name, "prepare") {
				effectClass = "reversible"
			}
			compiled.Tools = append(compiled.Tools, agentic.ScriptedTool{ToolID: call.Name, InputSchema: schema, EffectClass: effectClass})
			seenTools[call.Name] = true
		}
		if call.Name == "workspace.write_text" {
			var arguments struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			}
			if decodeStrict(call.Arguments, &arguments) != nil || arguments.Path == "" {
				return ScriptedCanaryCase{}, ErrScriptedCanaryCase
			}
			finalFiles[arguments.Path] = arguments.Content
		}
	}
	if final.Kind == "exact_files" {
		compiled.ObservedFinalState = mustJSON(map[string]any{"kind": "exact_files", "files": finalFiles})
		for path := range finalFiles {
			if _, existed := environment.Files[path]; !existed {
				compiled.OutputFiles = append(compiled.OutputFiles, path)
			}
		}
		sort.Strings(compiled.OutputFiles)
	} else if final.Kind == "canonical_result_and_business_state" {
		if digest(bytes.TrimSpace(compiled.ExpectedResult)) != final.ExpectedResultDigest {
			return ScriptedCanaryCase{}, ErrScriptedCanaryCase
		}
		compiled.ObservedFinalState = append(json.RawMessage(nil), task.Oracle.FinalState...)
	} else {
		return ScriptedCanaryCase{}, ErrScriptedCanaryCase
	}
	compiled.PythonSource = pythonScript(compiled.Calls, environment.Kind)
	compiled.JavaScriptSource = javaScript(compiled.Calls, environment.Kind)
	return compiled, nil
}

func scriptedCallResult(call SemanticCall, environment scriptedEnvironment, finalResult json.RawMessage, last bool) (json.RawMessage, error) {
	switch call.Name {
	case "workspace.read_text":
		var arguments struct {
			Path string `json:"path"`
		}
		if decodeStrict(call.Arguments, &arguments) != nil {
			return nil, ErrScriptedCanaryCase
		}
		content, ok := environment.Files[arguments.Path]
		if !ok {
			return nil, ErrScriptedCanaryCase
		}
		return mustJSON(map[string]any{"content": content}), nil
	case "workspace.write_text":
		return json.RawMessage(`{"status":"written"}`), nil
	default:
		if last && len(finalResult) > 0 {
			return append(json.RawMessage(nil), finalResult...), nil
		}
		return json.RawMessage(`{"items":[{"id":"fixture-record"}]}`), nil
	}
}

func fixtureResult(final scriptedFinalState) (json.RawMessage, error) {
	propertiesRaw, ok := final.OutputSchema["properties"]
	if !ok {
		return nil, ErrScriptedCanaryCase
	}
	var properties map[string]struct {
		Const json.RawMessage `json:"const"`
	}
	if decodeStrict(propertiesRaw, &properties) != nil || len(properties) == 0 {
		return nil, ErrScriptedCanaryCase
	}
	value := make(map[string]any, len(properties))
	for name, property := range properties {
		var item any
		if len(property.Const) == 0 || decodeScriptedNumber(property.Const, &item) != nil {
			return nil, ErrScriptedCanaryCase
		}
		value[name] = item
	}
	return mustJSON(value), nil
}

func schemaForArguments(raw json.RawMessage) (json.RawMessage, error) {
	var arguments map[string]any
	if decodeScriptedNumber(raw, &arguments) != nil || arguments == nil {
		return nil, ErrScriptedCanaryCase
	}
	properties := make(map[string]any, len(arguments))
	required := make([]string, 0, len(arguments))
	for name, value := range arguments {
		kind := ""
		switch value.(type) {
		case string:
			kind = "string"
		case json.Number:
			kind = "integer"
		case bool:
			kind = "boolean"
		default:
			return nil, ErrScriptedCanaryCase
		}
		properties[name] = map[string]any{"type": kind}
		required = append(required, name)
	}
	sort.Strings(required)
	return mustJSON(map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}), nil
}

func pythonScript(calls []agentic.ScriptedExpectedCall, environmentKind string) string {
	var lines []string
	for index, call := range calls {
		name := strings.NewReplacer(".", "_", "-", "_").Replace(call.Name)
		arguments := pythonArguments(call.Arguments)
		prefix := ""
		if environmentKind == "fixture_state" && index == len(calls)-1 {
			prefix = "result = "
		}
		lines = append(lines, prefix+name+"("+arguments+")")
	}
	if environmentKind != "fixture_state" {
		lines = append(lines, `result = {"status": "completed"}`)
	}
	return strings.Join(lines, "\n") + "\n"
}

func pythonArguments(raw json.RawMessage) string {
	var arguments map[string]json.RawMessage
	_ = decodeStrict(raw, &arguments)
	names := make([]string, 0, len(arguments))
	for name := range arguments {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name+"="+string(arguments[name]))
	}
	return strings.Join(parts, ", ")
}

func javaScript(calls []agentic.ScriptedExpectedCall, environmentKind string) string {
	var body []string
	if environmentKind == "workspace" {
		body = append(body, `import { mkdir, readFile, writeFile } from "node:fs/promises";`, `import { dirname } from "node:path";`)
	}
	body = append(body, `import { call } from "ws:tools";`, `export default async () => {`, `  let result = { status: "completed" };`)
	for _, expected := range calls {
		args := string(bytes.TrimSpace(expected.Arguments))
		body = append(body, fmt.Sprintf("  result = await call(\"invoke\", %q, %s);", expected.Name, args))
		if environmentKind == "workspace" {
			var arguments struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			}
			_ = decodeStrict(expected.Arguments, &arguments)
			switch expected.Name {
			case "workspace.read_text":
				body = append(body, fmt.Sprintf("  await readFile(%q, \"utf8\");", "/workspace/"+arguments.Path))
			case "workspace.write_text":
				path := "/workspace/" + arguments.Path
				body = append(body, fmt.Sprintf("  await mkdir(dirname(%q), { recursive: true });", path), fmt.Sprintf("  await writeFile(%q, %q);", path, arguments.Content))
			}
		}
	}
	body = append(body, `  return result;`, `};`)
	return strings.Join(body, "\n")
}

func mustJSON(value any) json.RawMessage {
	result, _ := json.Marshal(value)
	return result
}

func cloneStringMap(input map[string]string) map[string]string {
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func scriptedJSONObject(raw json.RawMessage) bool {
	var value map[string]any
	return decodeScriptedNumber(raw, &value) == nil && value != nil
}

func decodeScriptedNumber(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(new(any)) == nil {
		return ErrScriptedCanaryCase
	}
	return nil
}

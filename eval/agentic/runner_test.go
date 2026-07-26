package agentic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/eval/provider"
	"github.com/bkmashiro/agent-python-runtime/runtime/engine"
)

func adapterForStatefulOracle(t *testing.T, task Task, providerName func(string) string) *scriptedAdapter {
	t.Helper()
	var oracle StatefulOracle
	if decodeStrict(task.Oracle, &oracle) != nil {
		t.Fatal("decode oracle")
	}
	responses := make([]provider.Response, 0, len(oracle.Turns)*2)
	for turnIndex, turn := range oracle.Turns {
		items := make([]map[string]any, len(turn))
		for callIndex, call := range turn {
			items[callIndex] = map[string]any{
				"type": "function_call", "status": "completed", "call_id": fmt.Sprintf("provider-private-%d-%d", turnIndex, callIndex),
				"name": providerName(call.Name), "arguments": string(call.Arguments),
			}
		}
		body, _ := json.Marshal(map[string]any{"id": "response-private", "status": "completed", "output": items})
		responses = append(responses, responseFixture(string(body), 20, 5))
		responses = append(responses, responseFixture(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"turn complete"}]}]}`, 20, 5))
	}
	return &scriptedAdapter{responses: responses}
}

func developmentTrialLimits(turns int) TrialLimits {
	return TrialLimits{
		MaxProviderCalls: uint32(turns * 3), MaxToolCalls: 32, MaxPythonRuns: uint32(turns),
		MaxInputTokens: 10_000, MaxOutputTokens: 2_000, MaxTotalTokens: 12_000, MaxOutputTokensPerCall: 512,
	}
}

func TestRunDevelopmentTrialDirectUsesBoundedResponsesLoopAndScores(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	adapter := adapterForStatefulOracle(t, task, func(name string) string { return name })
	result, err := RunDevelopmentTrial(context.Background(), adapter, task, ConditionDirect, developmentTrialLimits(len(task.Interaction.Turns)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed || result.ErrorCode != "" || result.ProviderCalls != 6 || result.ToolCalls != 4 || result.PythonRuns != 0 || result.StatefulScore == nil || !result.StatefulScore.Passed {
		t.Fatalf("result=%+v", result)
	}
	if len(adapter.requests) != len(task.Interaction.Turns)*2 {
		t.Fatalf("requests=%d", len(adapter.requests))
	}
	if !strings.Contains(string(adapter.requests[0].Payload), "Continue after tool output only when a later call requires returned data") ||
		!strings.Contains(string(adapter.requests[0].Payload), "Emit all calls whose arguments are already known together") {
		t.Fatalf("direct request does not expose its bounded continuation contract: %s", adapter.requests[0].Payload)
	}
	if !strings.Contains(string(adapter.requests[0].Payload), "returns an error if the file already exists; do not pre-check existence") {
		t.Fatalf("direct request omits the Host touch error contract: %s", adapter.requests[0].Payload)
	}
	if !strings.Contains(string(adapter.requests[0].Payload), "the target file must already exist; echo does not create missing files") {
		t.Fatalf("direct request omits the Host echo error contract: %s", adapter.requests[0].Payload)
	}
	if !strings.Contains(string(adapter.requests[1].Payload), "function_call_output") {
		t.Fatal("next turn omitted prior Host tool outputs")
	}
	encoded, _ := json.Marshal(result)
	for _, forbidden := range []string{"summary.txt", "quantum computing", "provider-private", "response-private", "Pop on over"} {
		if containsBytes(encoded, []byte(forbidden)) {
			t.Fatalf("serialized result leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestRunDevelopmentTrialForModelBindsGPT41RequestAndArtifact(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateless-function-calling-parallel_multiple_112")
	response := responseFixture(`{"model":"gpt-4.1","status":"completed","output":[]}`, 10, 2)
	adapter := &scriptedAdapter{responses: []provider.Response{response}}
	identity := ExecutionIdentity{
		RepositoryCommit: strings.Repeat("a", 40), HostArtifactDigest: "sha256:" + strings.Repeat("a", 64),
		DatasetManifestDigest: "sha256:" + strings.Repeat("b", 64), ProviderCatalogDigest: "sha256:" + strings.Repeat("d", 64),
		ProviderCatalogObservedAt: "2026-07-26T11:00:00Z",
	}
	result, err := RunDevelopmentTrialForModelWithIdentity(context.Background(), adapter, task, ConditionDirect, "gpt-4.1", 0, developmentTrialLimits(1), identity, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Model != "gpt-4.1" || len(adapter.requests) != 1 || !strings.Contains(string(adapter.requests[0].Payload), `"model":"gpt-4.1"`) {
		t.Fatalf("result=%+v request=%s", result, adapter.requests[0].Payload)
	}
	if err := ValidateTrialResult(result); err != nil {
		t.Fatalf("gpt-4.1 result is not artifact-safe: %v", err)
	}
}

func TestRunDevelopmentTrialForModelRejectsUnapprovedModelBeforeProviderCall(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateless-function-calling-parallel_multiple_112")
	adapter := &scriptedAdapter{}
	_, err := RunDevelopmentTrialForModelWithIdentity(context.Background(), adapter, task, ConditionDirect, "arbitrary-provider-alias", 0, developmentTrialLimits(1), ExecutionIdentity{}, nil)
	if !errors.Is(err, ErrAgenticRun) || len(adapter.requests) != 0 {
		t.Fatalf("err=%v requests=%d", err, len(adapter.requests))
	}
}

func TestRunDevelopmentTrialStatefulRejectsNoCallsOnInitialExchange(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	adapter := &scriptedAdapter{responses: []provider.Response{
		responseFixture(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"done"}]}]}`, 20, 5),
	}}
	result, err := RunDevelopmentTrial(context.Background(), adapter, task, ConditionDirect, developmentTrialLimits(len(task.Interaction.Turns)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.ErrorCode != "no_tool_calls" || result.ProviderCalls != 1 || result.ToolCalls != 0 {
		t.Fatalf("result=%+v", result)
	}
}

func TestRunDevelopmentTrialStatefulFailsClosedAtTurnProviderCap(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	call := func(id, name, arguments string) provider.Response {
		return responseFixture(fmt.Sprintf(`{"status":"completed","output":[{"type":"function_call","id":%q,"call_id":%q,"name":%q,"arguments":%q,"status":"completed"}]}`, id, id, name, arguments), 20, 5)
	}
	adapter := &scriptedAdapter{responses: []provider.Response{
		call("call-cap-1", "cd", `{"folder":"Documents"}`),
		call("call-cap-2", "touch", `{"file_name":"summary.txt"}`),
		call("call-cap-3", "pwd", `{}`),
	}}
	result, err := RunDevelopmentTrial(context.Background(), adapter, task, ConditionDirect, developmentTrialLimits(len(task.Interaction.Turns)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.ErrorCode != "provider_turn_budget_exceeded" || result.ProviderCalls != 3 || result.ToolCalls != 3 {
		t.Fatalf("result=%+v", result)
	}
}

type oracleWorkflow struct {
	tools  *ToolRuntime
	oracle StatefulOracle
	turn   int
	codes  []string
}

func (workflow *oracleWorkflow) Execute(ctx context.Context, _ string, code string, _ uint32) (PythonRunResult, error) {
	workflow.codes = append(workflow.codes, code)
	before := countStatefulCalls(workflow.tools.Trace())
	for index, call := range workflow.oracle.Turns[workflow.turn] {
		if _, err := workflow.tools.InvokeDirect(ctx, fmt.Sprintf("fake-python-%d-%d", workflow.turn, index), fmt.Sprintf("fake:python:%d:%d", workflow.turn, index), call.Name, call.Arguments); err != nil {
			return PythonRunResult{}, err
		}
	}
	after := countStatefulCalls(workflow.tools.Trace())
	workflow.turn++
	return PythonRunResult{
		Success: true, CapabilityCalls: uint32(after - before), RequestDigest: digest([]byte(code)),
		ResponseDigest: digest([]byte("{}")), ResultDigest: digest([]byte("{}")), Backend: "oracle-test",
		ResetMode: engine.ResetModeFreshInstance, Observation: json.RawMessage(`{}`),
	}, nil
}
func (*oracleWorkflow) Close(context.Context) error { return nil }

func TestDevelopmentReplicateChangesSpecIdentity(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	limits := developmentTrialLimits(len(task.Interaction.Turns))
	first, err := RunDevelopmentTrialReplicate(context.Background(), adapterForStatefulOracle(t, task, func(name string) string { return name }), task, ConditionDirect, 0, limits, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RunDevelopmentTrialReplicate(context.Background(), adapterForStatefulOracle(t, task, func(name string) string { return name }), task, ConditionDirect, 1, limits, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.TrialID == second.TrialID || first.SpecDigest == second.SpecDigest || first.TaskDigest != second.TaskDigest || first.PromptDigest != second.PromptDigest || second.Replicate != 1 {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
}

func TestRunDevelopmentTrialPythonUsesUnderlyingHostTrace(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	responses := make([]provider.Response, len(task.Interaction.Turns))
	for index := range responses {
		body := fmt.Sprintf(`{"status":"completed","output":[{"type":"function_call","status":"completed","call_id":"python-private-%d","name":"run_python","arguments":"{\"code\":\"private code turn %d\"}"}]}`, index, index)
		responses[index] = responseFixture(body, 20, 5)
	}
	adapter := &scriptedAdapter{responses: responses}
	var workflow *oracleWorkflow
	factory := func(tools *ToolRuntime) (PythonWorkflow, error) {
		var oracle StatefulOracle
		if decodeStrict(task.Oracle, &oracle) != nil {
			return nil, ErrDataset
		}
		workflow = &oracleWorkflow{tools: tools, oracle: oracle}
		return workflow, nil
	}
	identity := ExecutionIdentity{
		RepositoryCommit: strings.Repeat("a", 40), HostArtifactDigest: "sha256:" + strings.Repeat("a", 64),
		DatasetManifestDigest: "sha256:" + strings.Repeat("b", 64), ProviderCatalogDigest: "sha256:" + strings.Repeat("d", 64),
		ProviderCatalogObservedAt: "2026-07-26T11:00:00Z", GuestArtifactDigest: "sha256:" + strings.Repeat("c", 64), GuestProfile: "core",
	}
	result, err := RunDevelopmentDiagnosticTrialForModelWithIdentity(context.Background(), adapter, task, ConditionPython, developmentModel, 0, developmentTrialLimits(len(task.Interaction.Turns)), identity, factory)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed || result.ProviderCalls != 3 || result.PythonRuns != 3 || result.ToolCalls != 4 || len(result.PythonEvidence) != 3 || workflow == nil || len(workflow.codes) != 3 {
		t.Fatalf("result=%+v workflow=%+v", result, workflow)
	}
	if result.RawDebug == nil || len(result.RawDebug.ProviderExchanges) != 3 || len(result.RawDebug.PythonRuns) != 3 ||
		len(result.RawDebug.ToolCalls) != 3 || len(result.RawDebug.ToolCalls[0]) != 2 || result.RawDebug.DeveloperPrompt == "" || len(result.RawDebug.ToolSurface) == 0 {
		t.Fatalf("raw debug=%+v", result.RawDebug)
	}
	if !strings.Contains(string(result.RawDebug.ProviderExchanges[0].Request), "Pop on over") ||
		!strings.Contains(string(result.RawDebug.ProviderExchanges[0].Response), "private code turn 0") ||
		result.RawDebug.PythonRuns[0].Code != "private code turn 0" || string(result.RawDebug.PythonRuns[0].Observation) != `{}` ||
		result.RawDebug.ToolCalls[0][0].Name != "cd" || len(result.RawDebug.ToolCalls[0][0].Arguments) == 0 || len(result.RawDebug.ToolCalls[0][0].Output) == 0 {
		t.Fatalf("raw debug omitted execution data: %+v", result.RawDebug)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "Pop on over") || strings.Contains(string(encoded), "private code turn") {
		t.Fatalf("formal artifact serialized raw debug: %s", encoded)
	}
	if strings.Contains(string(adapter.requests[0].Payload), `"parallel_tool_calls":true`) ||
		!strings.Contains(string(adapter.requests[0].Payload), "exactly one run_python call") {
		t.Fatalf("Python request contradicts its one-run-per-turn budget: %s", adapter.requests[0].Payload)
	}
	if err := ValidateTrialResult(result); err != nil {
		t.Fatalf("successful Python trial is not artifact-safe: %v", err)
	}
	encoded, _ = json.Marshal(result)
	if containsBytes(encoded, []byte("private code")) || containsBytes(encoded, []byte("python-private")) {
		t.Fatalf("serialized Python result leaked: %s", encoded)
	}
}

func TestRunDevelopmentTrialRejectsSecondPythonRunInSameTurn(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	body := `{"status":"completed","output":[` +
		`{"type":"function_call","status":"completed","call_id":"python-first","name":"run_python","arguments":"{\"code\":\"first\"}"},` +
		`{"type":"function_call","status":"completed","call_id":"python-second","name":"run_python","arguments":"{\"code\":\"second\"}"}` +
		`]}`
	adapter := &scriptedAdapter{responses: []provider.Response{responseFixture(body, 20, 5)}}
	var workflow *oracleWorkflow
	factory := func(tools *ToolRuntime) (PythonWorkflow, error) {
		var oracle StatefulOracle
		if decodeStrict(task.Oracle, &oracle) != nil {
			return nil, ErrDataset
		}
		workflow = &oracleWorkflow{tools: tools, oracle: oracle}
		return workflow, nil
	}
	result, err := RunDevelopmentTrial(context.Background(), adapter, task, ConditionPython, developmentTrialLimits(len(task.Interaction.Turns)), factory)
	if err != nil {
		t.Fatal(err)
	}
	if result.ErrorCode != "python_run_budget_exceeded" || result.ProviderCalls != 1 || result.PythonAttempts != 1 || result.PythonRuns != 1 || workflow == nil || len(workflow.codes) != 1 {
		t.Fatalf("result=%+v workflow=%+v", result, workflow)
	}
}

func TestDevelopmentTrialRejectsEvaluationBeforeProviderOrGuest(t *testing.T) {
	dataset, err := Load(datasetRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	var evaluation Task
	for _, task := range dataset.Tasks {
		if task.Split == "evaluation" {
			evaluation = task
			break
		}
	}
	adapter := &scriptedAdapter{}
	factoryCalled := false
	factory := func(*ToolRuntime) (PythonWorkflow, error) {
		factoryCalled = true
		return nil, nil
	}
	if _, err := RunDevelopmentTrial(context.Background(), adapter, evaluation, ConditionPython, developmentTrialLimits(1), factory); err == nil {
		t.Fatal("evaluation trial was accepted")
	}
	if len(adapter.requests) != 0 || factoryCalled {
		t.Fatalf("evaluation caused I/O: requests=%d factory=%v", len(adapter.requests), factoryCalled)
	}
}

func TestHybridSurfaceContainsDirectAndPythonWithoutCollision(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	tools, err := NewToolRuntime(task)
	if err != nil {
		t.Fatal(err)
	}
	surface, mapping, prompt, err := buildConditionSurface(tools, ConditionHybrid, true)
	if err != nil {
		t.Fatal(err)
	}
	sdk := compactPythonSDK(tools)
	description, _ := surface[len(surface)-1]["description"].(string)
	if len(surface) != len(task.Tools)+1 || mapping["run_python"] != "run_python" || mapping["pwd"] != "pwd" ||
		strings.Contains(prompt, sdk) || strings.Count(description, sdk) != 1 ||
		!strings.Contains(prompt, "at most one run_python call") || !strings.Contains(prompt, "Continue after tool output only when a later call requires returned data") ||
		!strings.Contains(prompt, "every required argument is known before any tool runs") ||
		!strings.Contains(prompt, "a later argument or control-flow decision depends on a Host-tool result") ||
		!strings.Contains(prompt, "Do not choose run_python merely because there are multiple calls") ||
		!strings.Contains(description, "touch(file_name: str) [returns an error if the file already exists; do not pre-check existence]") ||
		!strings.Contains(description, "echo(content: str, file_name: str=...) [when file_name is provided, the target file must already exist; echo does not create missing files]") {
		t.Fatalf("surface=%d mapping=%v prompt=%q description=%q", len(surface), mapping, prompt, description)
	}
}

func TestPythonSurfaceExplainsMinimalWorkflowWithoutRepeatingSDK(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	runtime, err := NewToolRuntime(task)
	if err != nil {
		t.Fatal(err)
	}
	surface, _, prompt, err := buildConditionSurface(runtime, ConditionPython, false)
	if err != nil {
		t.Fatal(err)
	}
	sdk := compactPythonSDK(runtime)
	if strings.Contains(prompt, sdk) || !strings.Contains(prompt, "Do not add exploratory, precondition, or verification calls") {
		t.Fatalf("prompt=%q", prompt)
	}
	if !strings.Contains(prompt, "Host-tool state persists across user turns") ||
		!strings.Contains(prompt, "Do not replay state-changing setup") {
		t.Fatalf("Python prompt omits the Guest/Host state boundary: %s", prompt)
	}
	if len(surface) != 1 {
		t.Fatalf("surface=%v", surface)
	}
	description, _ := surface[0]["description"].(string)
	if strings.Count(description, sdk) != 1 ||
		!strings.Contains(description, "Use returned JSON values in later calls") ||
		!strings.Contains(description, "current working directory") ||
		!strings.Contains(description, "do not repeat setup") ||
		!strings.Contains(description, "Only call Host tools required by the user") {
		t.Fatalf("description=%q", description)
	}
}

func TestCompactPythonSDKPreservesBoundedParameterValueHints(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	runtime, err := NewToolRuntime(task)
	if err != nil {
		t.Fatal(err)
	}
	sdk := compactPythonSDK(runtime)
	if !strings.Contains(sdk, "mode: Mode of operation ('l' for lines, 'w' for words, 'c' for characters).") {
		t.Fatalf("SDK omitted the bounded mode-value hint: %s", sdk)
	}
}

func TestCompactPythonSDKPreservesParameterTypes(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateless-function-calling-parallel_multiple_147")
	runtime, err := NewToolRuntime(task)
	if err != nil {
		t.Fatal(err)
	}
	sdk := compactPythonSDK(runtime)
	if !strings.Contains(sdk, "find_restaurants(food_type: str, location: str, number: int, dietary_requirements: list[str]=...)") {
		t.Fatalf("SDK omitted the array parameter type: %s", sdk)
	}
}

func TestFrozenDevelopmentPythonSurfacesStayWithinPromptBound(t *testing.T) {
	plan, dataset, err := LoadDevelopmentPilotPlan("v1/development-pilot-plan.json", "v1")
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]Task, len(dataset.Tasks))
	for _, task := range dataset.Tasks {
		byID[task.ID] = task
	}
	for _, taskID := range plan.TaskIDs {
		task := byID[taskID]
		runtime, err := NewToolRuntime(task)
		if err != nil {
			t.Fatalf("task=%s runtime: %v", taskID, err)
		}
		for _, condition := range []Condition{ConditionPython, ConditionHybrid} {
			_, _, prompt, err := buildConditionSurface(runtime, condition, task.Track == "stateful_local_tools")
			if err != nil || len(prompt) > maxPythonPromptBytes {
				t.Fatalf("task=%s condition=%s bytes=%d err=%v", taskID, condition, len(prompt), err)
			}
		}
	}
}

func TestPythonSurfaceRejectsOversizedTypedSDKPrompt(t *testing.T) {
	tools := make([]Tool, maxFunctionCalls)
	for index := range tools {
		properties := make(map[string]any, 16)
		required := make([]string, 16)
		for parameter := range required {
			name := fmt.Sprintf("parameter_%02d", parameter)
			properties[name] = map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
			required[parameter] = name
		}
		parameters, err := json.Marshal(map[string]any{"type": "object", "additionalProperties": false, "properties": properties, "required": required})
		if err != nil {
			t.Fatal(err)
		}
		tools[index] = Tool{Name: fmt.Sprintf("oversized_tool_%03d", index), Parameters: parameters}
	}
	runtime, err := NewToolRuntime(Task{Split: "dev", Track: "stateless_function_calling", Interaction: Interaction{Mode: "single_turn", Turns: []json.RawMessage{json.RawMessage(`{"role":"user","content":"test"}`)}}, Tools: tools})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := buildConditionSurface(runtime, ConditionPython, false); !errors.Is(err, ErrAgenticRun) {
		t.Fatalf("oversized typed SDK prompt err=%v", err)
	}
	if _, _, _, err := buildConditionSurface(runtime, ConditionDirect, false); err != nil {
		t.Fatalf("direct surface was incorrectly bound by Python SDK size: %v", err)
	}
}

func TestClassifyTrialErrorPreservesProviderIdentityMismatch(t *testing.T) {
	if got := classifyTrialError(ErrProviderIdentityMismatch); got != "provider_identity_mismatch" {
		t.Fatalf("error code=%q", got)
	}
}

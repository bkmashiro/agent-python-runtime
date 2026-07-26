package agentic

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/eval/provider"
)

func adapterForStatefulOracle(t *testing.T, task Task, providerName func(string) string) *scriptedAdapter {
	t.Helper()
	var oracle StatefulOracle
	if decodeStrict(task.Oracle, &oracle) != nil {
		t.Fatal("decode oracle")
	}
	responses := make([]provider.Response, len(oracle.Turns))
	for turnIndex, turn := range oracle.Turns {
		items := make([]map[string]any, len(turn))
		for callIndex, call := range turn {
			items[callIndex] = map[string]any{
				"type": "function_call", "status": "completed", "call_id": fmt.Sprintf("provider-private-%d-%d", turnIndex, callIndex),
				"name": providerName(call.Name), "arguments": string(call.Arguments),
			}
		}
		body, _ := json.Marshal(map[string]any{"id": "response-private", "status": "completed", "output": items})
		responses[turnIndex] = responseFixture(string(body), 20, 5)
	}
	return &scriptedAdapter{responses: responses}
}

func developmentTrialLimits(turns int) TrialLimits {
	return TrialLimits{
		MaxProviderCalls: uint32(turns), MaxToolCalls: 32, MaxPythonRuns: uint32(turns),
		MaxInputTokens: 10_000, MaxOutputTokens: 2_000, MaxTotalTokens: 12_000, MaxOutputTokensPerCall: 512,
	}
}

func TestRunDevelopmentTrialDirectUsesOneExchangePerTurnAndScores(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	adapter := adapterForStatefulOracle(t, task, func(name string) string { return name })
	result, err := RunDevelopmentTrial(context.Background(), adapter, task, ConditionDirect, developmentTrialLimits(len(task.Interaction.Turns)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed || result.ErrorCode != "" || result.ProviderCalls != 3 || result.ToolCalls != 4 || result.PythonRuns != 0 || result.StatefulScore == nil || !result.StatefulScore.Passed {
		t.Fatalf("result=%+v", result)
	}
	if len(adapter.requests) != len(task.Interaction.Turns) {
		t.Fatalf("requests=%d", len(adapter.requests))
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
		ResponseDigest: digest([]byte("{}")), ResultDigest: digest([]byte("{}")), Observation: json.RawMessage(`{}`),
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
	result, err := RunDevelopmentTrial(context.Background(), adapter, task, ConditionPython, developmentTrialLimits(len(task.Interaction.Turns)), factory)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed || result.ProviderCalls != 3 || result.PythonRuns != 3 || result.ToolCalls != 4 || len(result.PythonEvidence) != 3 || workflow == nil || len(workflow.codes) != 3 {
		t.Fatalf("result=%+v workflow=%+v", result, workflow)
	}
	encoded, _ := json.Marshal(result)
	if containsBytes(encoded, []byte("private code")) || containsBytes(encoded, []byte("python-private")) {
		t.Fatalf("serialized Python result leaked: %s", encoded)
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
	surface, mapping, prompt, err := buildConditionSurface(tools, ConditionHybrid)
	if err != nil {
		t.Fatal(err)
	}
	if len(surface) != len(task.Tools)+1 || mapping["run_python"] != "run_python" || mapping["pwd"] != "pwd" || !strings.Contains(prompt, "host_tools") {
		t.Fatalf("surface=%d mapping=%v prompt=%q", len(surface), mapping, prompt)
	}
}

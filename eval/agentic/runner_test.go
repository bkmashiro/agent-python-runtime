package agentic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/agenttrace"
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

func TestHybridTwoStageTreatmentExposesOnlySelectedExecutionSurface(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	treatment, err := LoadDevelopmentTreatment(filepath.Join(datasetRoot(t), "treatments", "hybrid-two-stage-router-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	identity := ExecutionIdentity{
		RepositoryCommit: strings.Repeat("a", 40), HostArtifactDigest: "sha256:" + strings.Repeat("a", 64),
		DatasetManifestDigest: "sha256:" + strings.Repeat("b", 64), ProviderCatalogDigest: "sha256:" + strings.Repeat("d", 64),
		ProviderCatalogObservedAt: "2026-07-26T11:00:00Z", GuestArtifactDigest: "sha256:" + strings.Repeat("c", 64), GuestProfile: "core",
	}
	for _, route := range []HybridRoute{HybridRouteDirect, HybridRoutePython} {
		t.Run(string(route), func(t *testing.T) {
			routerBody := `{"status":"completed","output":[{"type":"function_call","status":"completed","call_id":"route","name":"select_execution_surface","arguments":"{\"surface\":\"` + string(route) + `\",\"reason_code\":\"known_arguments\"}"}]}`
			responses := []provider.Response{responseFixture(routerBody, 5, 2)}
			if route == HybridRouteDirect {
				responses = append(responses, adapterForStatefulOracle(t, task, func(name string) string { return name }).responses...)
			} else {
				for turn := range task.Interaction.Turns {
					body := fmt.Sprintf(`{"status":"completed","output":[{"type":"function_call","status":"completed","call_id":"python-%d","name":"run_python","arguments":"{\"code\":\"turn %d\"}"}]}`, turn, turn)
					responses = append(responses, responseFixture(body, 10, 3))
				}
			}
			adapter := &scriptedAdapter{responses: responses}
			factory := func(tools *ToolRuntime) (PythonWorkflow, error) {
				var oracle StatefulOracle
				if decodeStrict(task.Oracle, &oracle) != nil {
					return nil, ErrDataset
				}
				return &oracleWorkflow{tools: tools, oracle: oracle}, nil
			}
			limits := developmentTrialLimits(len(task.Interaction.Turns))
			limits.MaxProviderCalls++
			result, err := RunDevelopmentDiagnosticTrialForModelWithIdentityAndTreatment(context.Background(), adapter, task, ConditionHybrid, developmentModel, 0, limits, identity, treatment, factory)
			if err != nil {
				t.Fatal(err)
			}
			if !result.Passed || result.Route == nil || result.Route.Route != route || result.ProviderCalls != uint32(len(responses)) || ValidateTrialResult(result) != nil {
				t.Fatalf("result=%+v", result)
			}
			if len(adapter.requests) < 2 || !strings.Contains(string(adapter.requests[0].Payload), "select_execution_surface") {
				t.Fatal("router request missing")
			}
			execution := string(adapter.requests[1].Payload)
			if route == HybridRouteDirect {
				if strings.Contains(execution, `"name":"run_python"`) || !strings.Contains(execution, `"name":"cd"`) {
					t.Fatalf("direct route surface=%s", execution)
				}
			} else if !strings.Contains(execution, `"name":"run_python"`) || strings.Contains(execution, `"name":"cd"`) {
				t.Fatalf("python route surface=%s", execution)
			}
		})
	}
}

func TestHybridTwoStageTreatmentSpecIdentityIsRouteInvariant(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	treatment, err := LoadDevelopmentTreatment(filepath.Join(datasetRoot(t), "treatments", "hybrid-two-stage-router-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	identity := ExecutionIdentity{
		RepositoryCommit: strings.Repeat("a", 40), HostArtifactDigest: "sha256:" + strings.Repeat("a", 64),
		DatasetManifestDigest: "sha256:" + strings.Repeat("b", 64), ProviderCatalogDigest: "sha256:" + strings.Repeat("d", 64),
		ProviderCatalogObservedAt: "2026-07-26T11:00:00Z", GuestArtifactDigest: "sha256:" + strings.Repeat("c", 64), GuestProfile: "core",
	}
	factory := func(tools *ToolRuntime) (PythonWorkflow, error) {
		var oracle StatefulOracle
		if decodeStrict(task.Oracle, &oracle) != nil {
			return nil, ErrDataset
		}
		return &oracleWorkflow{tools: tools, oracle: oracle}, nil
	}
	run := func(route HybridRoute) TrialResult {
		routerBody := `{"status":"completed","output":[{"type":"function_call","status":"completed","call_id":"route","name":"select_execution_surface","arguments":"{\"surface\":\"` + string(route) + `\",\"reason_code\":\"known_arguments\"}"}]}`
		responses := []provider.Response{responseFixture(routerBody, 5, 2)}
		if route == HybridRouteDirect {
			responses = append(responses, adapterForStatefulOracle(t, task, func(name string) string { return name }).responses...)
		} else {
			for turn := range task.Interaction.Turns {
				body := fmt.Sprintf(`{"status":"completed","output":[{"type":"function_call","status":"completed","call_id":"python-%d","name":"run_python","arguments":"{\"code\":\"turn %d\"}"}]}`, turn, turn)
				responses = append(responses, responseFixture(body, 10, 3))
			}
		}
		limits := developmentTrialLimits(len(task.Interaction.Turns))
		limits.MaxProviderCalls++
		result, runErr := RunDevelopmentDiagnosticTrialForModelWithIdentityAndTreatment(
			context.Background(), &scriptedAdapter{responses: responses}, task, ConditionHybrid, developmentModel,
			0, limits, identity, treatment, factory,
		)
		if runErr != nil {
			t.Fatal(runErr)
		}
		if result.ErrorCode != "" || result.Route == nil || result.Route.Route != route {
			t.Fatalf("route=%s result=%+v", route, result)
		}
		if ValidateTrialResult(result) != nil {
			t.Fatalf("route=%s invalid artifact: %v", route, result)
		}
		return result
	}
	direct := run(HybridRouteDirect)
	python := run(HybridRoutePython)
	if direct.SpecDigest != python.SpecDigest {
		t.Fatalf("spec digest differs: direct=%s python=%s", direct.SpecDigest, python.SpecDigest)
	}
	if direct.TrialID != python.TrialID {
		t.Fatalf("trial id differs: direct=%s python=%s", direct.TrialID, python.TrialID)
	}
}

func TestHybridTwoStageSafeRepairV2MultiTurnDirectReclaimsRepairBudget(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	treatment, err := LoadDevelopmentTreatment(filepath.Join(datasetRoot(t), "treatments", "hybrid-two-stage-safe-repair-v2.json"))
	if err != nil {
		t.Fatal(err)
	}
	identity := ExecutionIdentity{
		RepositoryCommit: strings.Repeat("a", 40), HostArtifactDigest: "sha256:" + strings.Repeat("a", 64),
		DatasetManifestDigest: "sha256:" + strings.Repeat("b", 64), ProviderCatalogDigest: "sha256:" + strings.Repeat("d", 64),
		ProviderCatalogObservedAt: "2026-07-26T11:00:00Z", GuestArtifactDigest: "sha256:" + strings.Repeat("c", 64), GuestProfile: "core",
	}
	routerBody := `{"status":"completed","output":[{"type":"function_call","status":"completed","call_id":"route-v2-direct","name":"select_execution_surface","arguments":"{\"surface\":\"direct\",\"reason_code\":\"known_arguments\"}"}]}`
	responses := []provider.Response{responseFixture(routerBody, 5, 2)}
	responses = append(responses, adapterForStatefulOracle(t, task, func(name string) string { return name }).responses...)
	limits := developmentTrialLimits(len(task.Interaction.Turns))
	limits.MaxProviderCalls += treatment.MaxRouterCallsPerHybridTrial + treatment.MaxPythonRepairsPerTrial
	limits.MaxPythonRuns += treatment.MaxPythonRepairsPerTrial
	result, err := RunDevelopmentDiagnosticTrialForModelWithIdentityAndTreatment(
		context.Background(), &scriptedAdapter{responses: responses}, task, ConditionHybrid, developmentModel, 0, limits, identity, treatment, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed || result.Route == nil || result.Route.Route != HybridRouteDirect || result.ProviderCalls != uint32(len(responses)) ||
		result.Repair != nil || result.PythonAttempts != 0 || result.PythonRuns != 0 || ValidateTrialResult(result) != nil {
		t.Fatalf("result=%+v", result)
	}
}

func TestHybridTwoStageRouteFailurePreservesTrialResultAndExchange(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	treatment, err := LoadDevelopmentTreatment(filepath.Join(datasetRoot(t), "treatments", "hybrid-two-stage-router-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	identity := ExecutionIdentity{
		RepositoryCommit: strings.Repeat("a", 40), HostArtifactDigest: "sha256:" + strings.Repeat("a", 64),
		DatasetManifestDigest: "sha256:" + strings.Repeat("b", 64), ProviderCatalogDigest: "sha256:" + strings.Repeat("d", 64),
		ProviderCatalogObservedAt: "2026-07-26T11:00:00Z", GuestArtifactDigest: "sha256:" + strings.Repeat("c", 64), GuestProfile: "core",
	}
	limits := developmentTrialLimits(len(task.Interaction.Turns))
	limits.MaxProviderCalls++
	routerBody := `{"status":"completed","output":[{"type":"function_call","status":"completed","call_id":"route","name":"select_execution_surface","arguments":"{\"surface\":\"invalid\",\"reason_code\":\"known_arguments\"}"}]}`
	result, runErr := RunDevelopmentDiagnosticTrialForModelWithIdentityAndTreatment(
		context.Background(), &scriptedAdapter{responses: []provider.Response{responseFixture(routerBody, 5, 2)}},
		task, ConditionHybrid, developmentModel, 0, limits, identity, treatment, nil,
	)
	if runErr != nil {
		t.Fatal(runErr)
	}
	if result.ErrorCode == "" || result.Route != nil || result.Passed || result.ProviderCalls != 1 || len(result.Exchanges) != 1 {
		t.Fatalf("result=%+v", result)
	}
	if ValidateTrialResult(result) != nil {
		t.Fatalf("invalid trial result after route failure: %v", result)
	}
}

func TestStructuredHostContextTreatmentAppearsBeforeLaterTurns(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	adapter := adapterForStatefulOracle(t, task, func(name string) string { return name })
	treatment, err := LoadDevelopmentTreatment(filepath.Join(datasetRoot(t), "treatments", "structured-host-context-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	identity := ExecutionIdentity{
		RepositoryCommit: strings.Repeat("a", 40), HostArtifactDigest: "sha256:" + strings.Repeat("a", 64),
		DatasetManifestDigest: "sha256:" + strings.Repeat("b", 64), ProviderCatalogDigest: "sha256:" + strings.Repeat("d", 64),
		ProviderCatalogObservedAt: "2026-07-26T11:00:00Z",
	}
	result, err := RunDevelopmentDiagnosticTrialForModelWithIdentityAndTreatment(
		context.Background(), adapter, task, ConditionDirect, developmentModel, 0,
		developmentTrialLimits(len(task.Interaction.Turns)), identity, treatment, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed || len(result.HostContextDigests) != 2 || result.RawDebug == nil || len(result.RawDebug.HostContexts) != 2 {
		t.Fatalf("result=%+v raw=%+v", result, result.RawDebug)
	}
	if strings.Contains(string(adapter.requests[0].Payload), "agentic-host-context/v1") || strings.Contains(string(adapter.requests[1].Payload), "agentic-host-context/v1") {
		t.Fatal("first turn received Host context")
	}
	for _, index := range []int{2, 4} {
		payload := string(adapter.requests[index].Payload)
		if !strings.Contains(payload, "agentic-host-context/v1") || !strings.Contains(payload, "successful_effects") || !strings.Contains(payload, "/alex/Documents") {
			t.Fatalf("request %d missing context: %s", index, payload)
		}
	}
	encoded, _ := json.Marshal(result)
	for _, forbidden := range []string{"summary.txt", "/alex/Documents", "successful_effects"} {
		if containsBytes(encoded, []byte(forbidden)) {
			t.Fatalf("formal result leaked %q: %s", forbidden, encoded)
		}
	}
	if ValidateTrialResult(result) != nil {
		t.Fatal("structured context result failed artifact validation")
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
	var continuation struct {
		Input []struct {
			Type   string `json:"type"`
			CallID string `json:"call_id"`
		} `json:"input"`
	}
	if json.Unmarshal(adapter.requests[1].Payload, &continuation) != nil {
		t.Fatal("decode continuation payload")
	}
	foundOutput := false
	for _, item := range continuation.Input {
		if item.Type == "function_call_output" && item.CallID != "" {
			foundOutput = true
		}
	}
	if !foundOutput {
		t.Fatal("next turn omitted prior Host tool outputs")
	}
	encoded, _ := json.Marshal(result)
	for _, forbidden := range []string{"summary.txt", "quantum computing", "provider-private", "response-private", "Pop on over"} {
		if containsBytes(encoded, []byte(forbidden)) {
			t.Fatalf("serialized result leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestRunDevelopmentTrialEmitsMetadataOnlyTracePluginEvents(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	adapter := adapterForStatefulOracle(t, task, func(name string) string { return name })
	sink := agenttrace.NewMemorySink()
	ctx, err := agenttrace.WithPlugin(context.Background(), agenttrace.Plugin{Mode: agenttrace.ModeRequired, Sink: sink})
	if err != nil {
		t.Fatal(err)
	}
	result, err := RunDevelopmentTrial(ctx, adapter, task, ConditionDirect, developmentTrialLimits(len(task.Interaction.Turns)), nil)
	if err != nil {
		t.Fatal(err)
	}
	events := sink.Events()
	if len(events) < 6 || events[0].EventType != agenttrace.EventRunStarted || events[len(events)-1].EventType != agenttrace.EventRunCompleted {
		t.Fatalf("events=%+v", events)
	}
	seen := map[agenttrace.EventType]bool{}
	for index, event := range events {
		seen[event.EventType] = true
		if event.AgentRunID != result.TrialID || event.Sequence != uint64(index+1) || event.Validate() != nil {
			t.Fatalf("event[%d]=%+v", index, event)
		}
		payload := string(event.Payload)
		for _, forbidden := range []string{"\"developer_prompt\":", "\"tool_surface\":", "\"arguments\":", "\"observation\":", "\"code\":"} {
			if strings.Contains(payload, forbidden) {
				t.Fatalf("portable event leaked %q: %s", forbidden, payload)
			}
		}
	}
	for _, required := range []agenttrace.EventType{
		agenttrace.EventLLMRequestStarted, agenttrace.EventLLMResponseReceived, agenttrace.EventLLMOutputObserved,
		agenttrace.EventDirectToolStarted, agenttrace.EventDirectToolCompleted, agenttrace.EventFinalStateObserved,
	} {
		if !seen[required] {
			t.Fatalf("missing event type %q", required)
		}
	}
}

func TestRequiredTraceSQLiteDogfoodAndForkLineageContinuation(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	adapter := adapterForStatefulOracle(t, task, func(name string) string { return name })
	path := filepath.Join(t.TempDir(), "agent-trace.sqlite")
	store, err := agenttrace.OpenSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := agenttrace.WithPlugin(context.Background(), agenttrace.Plugin{Mode: agenttrace.ModeRequired, Sink: store})
	if err != nil {
		t.Fatal(err)
	}
	result, err := RunDevelopmentTrial(ctx, adapter, task, ConditionDirect, developmentTrialLimits(len(task.Interaction.Turns)), nil)
	if err != nil || !result.Passed {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	readOnly, err := agenttrace.OpenSQLiteStoreReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	playback, err := readOnly.LoadPlayback(context.Background(), result.TrialID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := playback.IntegrityDigest(); err != nil {
		t.Fatal(err)
	}
	checkpointSequence := uint64(0)
	for _, event := range playback.Events {
		if event.EventType == agenttrace.EventCheckpointCreated {
			checkpointSequence = event.Sequence
			break
		}
	}
	if checkpointSequence == 0 {
		t.Fatal("source run has no checkpoint")
	}
	plan, err := playback.ForkAt(checkpointSequence, result.TrialID+"_fork")
	if err != nil {
		t.Fatal(err)
	}
	if err := readOnly.Close(); err != nil {
		t.Fatal(err)
	}
	writable, err := agenttrace.OpenSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	recorder, forkEvent, err := (agenttrace.Plugin{Mode: agenttrace.ModeRequired, Sink: writable}).BeginFork(context.Background(), plan, nil)
	if err != nil {
		t.Fatal(err)
	}
	final, err := recorder.Record(context.Background(), agenttrace.EventFinalStateObserved, forkEvent.EventID,
		json.RawMessage(`{"continued":true,"state_digest":"`+plan.StateFingerprint+`"}`), plan.StateFingerprint)
	if err != nil || final.Sequence != 2 {
		t.Fatalf("final=%+v err=%v", final, err)
	}
	if err := writable.Close(); err != nil {
		t.Fatal(err)
	}
	verified, err := agenttrace.OpenSQLiteStoreReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer verified.Close()
	child, err := verified.LoadPlayback(context.Background(), plan.AgentRunID)
	if err != nil || len(child.Events) != 2 || child.Events[0].EventType != agenttrace.EventForkStarted || child.Events[1].ParentEventID != child.Events[0].EventID {
		t.Fatalf("child=%+v err=%v", child, err)
	}
}

func TestRunDevelopmentTrialForModelBindsLunaRequestAndArtifact(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateless-function-calling-parallel_multiple_112")
	response := responseFixture(`{"model":"gpt-5.6-luna","status":"completed","output":[]}`, 10, 2)
	adapter := &scriptedAdapter{responses: []provider.Response{response}}
	identity := ExecutionIdentity{
		RepositoryCommit: strings.Repeat("a", 40), HostArtifactDigest: "sha256:" + strings.Repeat("a", 64),
		DatasetManifestDigest: "sha256:" + strings.Repeat("b", 64), ProviderCatalogDigest: "sha256:" + strings.Repeat("d", 64),
		ProviderCatalogObservedAt: "2026-07-26T11:00:00Z",
	}
	result, err := RunDevelopmentTrialForModelWithIdentity(context.Background(), adapter, task, ConditionDirect, "gpt-5.6-luna", 0, developmentTrialLimits(1), identity, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Model != "gpt-5.6-luna" || len(adapter.requests) != 1 || adapter.requests[0].Model != "gpt-5.6-luna" {
		t.Fatalf("result=%+v requests=%+v", result, adapter.requests)
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

func TestRunDevelopmentTrialForModelBindsCodexSparkRequestAndArtifact(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateless-function-calling-parallel_multiple_112")
	response := responseFixture(`{"model":"gpt-5.3-codex-spark","status":"completed","output":[]}`, 10, 2)
	adapter := &scriptedAdapter{responses: []provider.Response{response}}
	identity := ExecutionIdentity{
		RepositoryCommit: strings.Repeat("a", 40), HostArtifactDigest: "sha256:" + strings.Repeat("a", 64),
		DatasetManifestDigest: "sha256:" + strings.Repeat("b", 64), ProviderCatalogDigest: "sha256:" + strings.Repeat("d", 64),
		ProviderCatalogObservedAt: "2026-07-26T11:00:00Z",
	}
	result, err := RunDevelopmentTrialForModelWithIdentity(context.Background(), adapter, task, ConditionDirect, "gpt-5.3-codex-spark", 0, developmentTrialLimits(1), identity, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Model != "gpt-5.3-codex-spark" || len(adapter.requests) != 1 || !strings.Contains(string(adapter.requests[0].Payload), `"model":"gpt-5.3-codex-spark"`) {
		t.Fatalf("result=%+v request=%s", result, adapter.requests[0].Payload)
	}
	if err := ValidateTrialResult(result); err != nil {
		t.Fatalf("gpt-5.3-codex-spark result is not artifact-safe: %v", err)
	}
}

func TestRunDevelopmentTrialForModelBindsGPT4ORequestAndArtifact(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateless-function-calling-parallel_multiple_112")
	response := responseFixture(`{"model":"gpt-4o","status":"completed","output":[]}`, 10, 2)
	adapter := &scriptedAdapter{responses: []provider.Response{response}}
	identity := ExecutionIdentity{
		RepositoryCommit: strings.Repeat("a", 40), HostArtifactDigest: "sha256:" + strings.Repeat("a", 64),
		DatasetManifestDigest: "sha256:" + strings.Repeat("b", 64), ProviderCatalogDigest: "sha256:" + strings.Repeat("d", 64),
		ProviderCatalogObservedAt: "2026-07-26T11:00:00Z",
	}
	result, err := RunDevelopmentTrialForModelWithIdentity(context.Background(), adapter, task, ConditionDirect, "gpt-4o", 0, developmentTrialLimits(1), identity, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Model != "gpt-4o" || len(adapter.requests) != 1 || !strings.Contains(string(adapter.requests[0].Payload), `"model":"gpt-4o"`) {
		t.Fatalf("result=%+v request=%s", result, adapter.requests[0].Payload)
	}
	if err := ValidateTrialResult(result); err != nil {
		t.Fatalf("gpt-4o result is not artifact-safe: %v", err)
	}
}

func TestRunDevelopmentTrialForModelBindsGemini36FlashRequestAndArtifact(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateless-function-calling-parallel_multiple_112")
	response := responseFixture(`{"model":"gemini-3.6-flash","status":"completed","output":[]}`, 10, 2)
	adapter := &scriptedAdapter{responses: []provider.Response{response}}
	identity := ExecutionIdentity{
		RepositoryCommit: strings.Repeat("a", 40), HostArtifactDigest: "sha256:" + strings.Repeat("a", 64),
		DatasetManifestDigest: "sha256:" + strings.Repeat("b", 64), ProviderCatalogDigest: "sha256:" + strings.Repeat("d", 64),
		ProviderCatalogObservedAt: "2026-07-26T11:00:00Z",
	}
	result, err := RunDevelopmentTrialForModelWithIdentity(context.Background(), adapter, task, ConditionDirect, "gemini-3.6-flash", 0, developmentTrialLimits(1), identity, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Model != "gemini-3.6-flash" || len(adapter.requests) != 1 || !strings.Contains(string(adapter.requests[0].Payload), `"model":"gemini-3.6-flash"`) {
		t.Fatalf("result=%+v request=%s", result, adapter.requests[0].Payload)
	}
	if err := ValidateTrialResult(result); err != nil {
		t.Fatalf("gemini-3.6-flash result is not artifact-safe: %v", err)
	}
}

func TestRunDevelopmentTrialForModelBindsGrok420RequestAndArtifact(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateless-function-calling-parallel_multiple_112")
	response := responseFixture(`{"model":"grok-4.20-0309-non-reasoning","status":"completed","output":[]}`, 10, 2)
	adapter := &scriptedAdapter{responses: []provider.Response{response}}
	identity := ExecutionIdentity{
		RepositoryCommit: strings.Repeat("a", 40), HostArtifactDigest: "sha256:" + strings.Repeat("a", 64),
		DatasetManifestDigest: "sha256:" + strings.Repeat("b", 64), ProviderCatalogDigest: "sha256:" + strings.Repeat("d", 64),
		ProviderCatalogObservedAt: "2026-07-26T11:00:00Z",
	}
	model := "grok-4.20-0309-non-reasoning"
	result, err := RunDevelopmentTrialForModelWithIdentity(context.Background(), adapter, task, ConditionDirect, model, 0, developmentTrialLimits(1), identity, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Model != model || len(adapter.requests) != 1 || !strings.Contains(string(adapter.requests[0].Payload), `"model":"grok-4.20-0309-non-reasoning"`) {
		t.Fatalf("result=%+v request=%s", result, adapter.requests[0].Payload)
	}
	if err := ValidateTrialResult(result); err != nil {
		t.Fatalf("grok result is not artifact-safe: %v", err)
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
	tools   *ToolRuntime
	oracle  StatefulOracle
	turn    int
	codes   []string
	compact bool
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
	result := PythonRunResult{
		Success: true, CapabilityCalls: uint32(after - before), RequestDigest: digest([]byte(code)),
		ResponseDigest: digest([]byte("{}")), ResultDigest: digest([]byte("{}")), Backend: "oracle-test",
		ResetMode: engine.ResetModeFreshInstance, Observation: json.RawMessage(`{}`),
	}
	if workflow.compact {
		result.ModelCodeDigest = digest([]byte(code))
		effectiveCode, err := compactEffectivePythonCode(code)
		if err != nil {
			return PythonRunResult{}, err
		}
		result.EffectiveCodeDigest = digest([]byte(effectiveCode))
		result.WrapperDigest = compactPythonWrapperDigest()
	}
	return result, nil
}
func (*oracleWorkflow) Close(context.Context) error { return nil }

type guestFailureWorkflow struct {
	tools        *ToolRuntime
	failureClass FailureClass
	invokeHost   bool
}

func (workflow *guestFailureWorkflow) Execute(ctx context.Context, _ string, code string, _ uint32) (PythonRunResult, error) {
	calls := uint32(0)
	if workflow.invokeHost {
		if _, err := workflow.tools.InvokeDirect(ctx, "guest-failure-host-1", "guest:failure:1", "pwd", json.RawMessage(`{}`)); err != nil {
			return PythonRunResult{}, err
		}
		calls = 1
	}
	return PythonRunResult{
		Success: false, ErrorCode: "python_exception", FailureClass: workflow.failureClass, CapabilityCalls: calls,
		RequestDigest: digest([]byte(code)), ResponseDigest: digest([]byte(`{"status":"error"}`)), ResultDigest: digest([]byte(`null`)),
		Backend: "failure-test", ResetMode: engine.ResetModeFreshInstance,
		Observation: json.RawMessage(`{"error_code":"python_exception","status":"error"}`),
	}, nil
}
func (*guestFailureWorkflow) Close(context.Context) error { return nil }

type repairOracleWorkflow struct {
	oracle *oracleWorkflow
	calls  int
}

func (workflow *repairOracleWorkflow) Execute(ctx context.Context, runID, code string, maxCalls uint32) (PythonRunResult, error) {
	workflow.calls++
	if workflow.calls == 1 {
		return PythonRunResult{
			Success: false, ErrorCode: "python_exception", FailureClass: FailureClassPythonException,
			RequestDigest: digest([]byte(code)), ResponseDigest: digest([]byte(`{"status":"error"}`)), ResultDigest: digest([]byte(`null`)),
			Backend: "repair-test", ResetMode: engine.ResetModeFreshInstance,
			Observation: json.RawMessage(`{"error_code":"python_exception","status":"error"}`),
		}, nil
	}
	return workflow.oracle.Execute(ctx, runID, code, maxCalls)
}
func (workflow *repairOracleWorkflow) Close(context.Context) error { return nil }

func TestPythonRepairTreatmentRetriesOneZeroCallFailure(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	call := func(id, code string) provider.Response {
		return responseFixture(fmt.Sprintf(`{"status":"completed","output":[{"type":"function_call","status":"completed","call_id":%q,"name":"run_python","arguments":%q}]}`, id, `{"code":`+fmt.Sprintf("%q", code)+`}`), 10, 3)
	}
	adapter := &scriptedAdapter{responses: []provider.Response{
		call("repair-first", "private broken code"), call("repair-second", "private repaired code"),
		call("repair-turn-2", "private turn 2"), call("repair-turn-3", "private turn 3"),
	}}
	treatment, err := LoadDevelopmentTreatment(filepath.Join(datasetRoot(t), "treatments", "python-safe-repair-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	identity := ExecutionIdentity{
		RepositoryCommit: strings.Repeat("a", 40), HostArtifactDigest: "sha256:" + strings.Repeat("a", 64),
		DatasetManifestDigest: "sha256:" + strings.Repeat("b", 64), ProviderCatalogDigest: "sha256:" + strings.Repeat("d", 64),
		ProviderCatalogObservedAt: "2026-07-26T11:00:00Z", GuestArtifactDigest: "sha256:" + strings.Repeat("c", 64), GuestProfile: "core",
	}
	var workflow *repairOracleWorkflow
	factory := func(tools *ToolRuntime) (PythonWorkflow, error) {
		var oracle StatefulOracle
		if decodeStrict(task.Oracle, &oracle) != nil {
			return nil, ErrDataset
		}
		workflow = &repairOracleWorkflow{oracle: &oracleWorkflow{tools: tools, oracle: oracle}}
		return workflow, nil
	}
	limits := developmentTrialLimits(len(task.Interaction.Turns))
	limits.MaxPythonRuns++
	limits.MaxProviderCalls++
	result, err := RunDevelopmentDiagnosticTrialForModelWithIdentityAndTreatment(
		context.Background(), adapter, task, ConditionPython, developmentModel, 0, limits, identity, treatment, factory,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed || result.ProviderCalls != 4 || result.PythonRuns != 4 || result.Repair == nil || !result.Repair.Offered || !result.Repair.Attempted || !result.Repair.Succeeded ||
		result.Repair.Turn != 0 || result.Repair.OriginalFailureClass != FailureClassPythonException || result.Repair.OriginalFailureDigest == "" || result.FailureDetail != nil || workflow.calls != 4 {
		t.Fatalf("result=%+v workflow=%+v", result, workflow)
	}
	if !strings.Contains(string(adapter.requests[1].Payload), "exactly one corrected run_python") || ValidateTrialResult(result) != nil {
		t.Fatalf("repair request/result invalid: %s result=%+v", adapter.requests[1].Payload, result)
	}
	tampered := result
	repair := *result.Repair
	repair.OriginalFailureDigest = "sha256:" + strings.Repeat("0", 64)
	tampered.Repair = &repair
	if ValidateTrialResult(tampered) == nil {
		t.Fatal("tampered original failure digest accepted")
	}
	encoded, _ := json.Marshal(result)
	for _, forbidden := range []string{"private broken code", "private repaired code", "ValueError", "traceback"} {
		if containsBytes(encoded, []byte(forbidden)) {
			t.Fatalf("formal repair evidence leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestHybridTwoStageSafeRepairV2RepairsSelectedPythonWithoutHostEffects(t *testing.T) {
	dataset, err := LoadRoutingDataset(filepath.Join(datasetRoot(t), "..", "routing", "v1"))
	if err != nil {
		t.Fatal(err)
	}
	var task Task
	for _, candidate := range dataset.Tasks {
		if candidate.ID == "rd-003" {
			task = candidate
			break
		}
	}
	if task.ID == "" {
		t.Fatal("missing routing task")
	}
	call := func(id, code string) provider.Response {
		return responseFixture(fmt.Sprintf(`{"status":"completed","output":[{"type":"function_call","status":"completed","call_id":%q,"name":"run_python","arguments":%q}]}`, id, `{"code":`+fmt.Sprintf("%q", code)+`}`), 10, 3)
	}
	adapter := &scriptedAdapter{responses: []provider.Response{
		responseFixture(`{"status":"completed","output":[{"type":"function_call","status":"completed","call_id":"route-v2","name":"select_execution_surface","arguments":"{\"surface\":\"python\",\"reason_code\":\"transformation\"}"}]}`, 10, 3),
		call("v2-first", "private broken code"),
		call("v2-repair", "private repaired code"),
	}}
	treatment, err := LoadDevelopmentTreatment(filepath.Join(datasetRoot(t), "treatments", "hybrid-two-stage-safe-repair-v2.json"))
	if err != nil {
		t.Fatal(err)
	}
	identity := ExecutionIdentity{
		RepositoryCommit: strings.Repeat("a", 40), HostArtifactDigest: "sha256:" + strings.Repeat("a", 64),
		DatasetManifestDigest: "sha256:" + strings.Repeat("b", 64), ProviderCatalogDigest: "sha256:" + strings.Repeat("d", 64),
		ProviderCatalogObservedAt: "2026-07-26T11:00:00Z", GuestArtifactDigest: "sha256:" + strings.Repeat("c", 64), GuestProfile: "core",
	}
	var workflow *repairOracleWorkflow
	result, err := RunDevelopmentDiagnosticTrialForModelWithIdentityAndTreatment(
		context.Background(), adapter, task, ConditionHybrid, developmentModel, 0,
		TrialLimits{MaxProviderCalls: 3, MaxToolCalls: 16, MaxPythonRuns: 2, MaxInputTokens: 10_000, MaxOutputTokens: 2_000, MaxTotalTokens: 12_000, MaxOutputTokensPerCall: 512},
		identity, treatment, func(tools *ToolRuntime) (PythonWorkflow, error) {
			var oracle StatefulOracle
			if decodeStrict(task.Oracle, &oracle) != nil {
				return nil, ErrDataset
			}
			workflow = &repairOracleWorkflow{oracle: &oracleWorkflow{tools: tools, oracle: oracle}}
			return workflow, nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed || result.Route == nil || result.Route.Route != HybridRoutePython || result.ProviderCalls != 3 || result.PythonRuns != 2 ||
		result.Repair == nil || !result.Repair.Offered || !result.Repair.Attempted || !result.Repair.Succeeded || workflow.calls != 2 || ValidateTrialResult(result) != nil {
		t.Fatalf("result=%+v workflow=%+v", result, workflow)
	}
	if !strings.Contains(string(adapter.requests[1].Payload), "Host filesystem is not available through open") ||
		!strings.Contains(string(adapter.requests[2].Payload), "exactly one corrected run_python") {
		t.Fatalf("execution=%s repair=%s", adapter.requests[1].Payload, adapter.requests[2].Payload)
	}
}

type engineFailureWorkflow struct{}

func (*engineFailureWorkflow) Execute(context.Context, string, string, uint32) (PythonRunResult, error) {
	return PythonRunResult{RawRequest: json.RawMessage(`{"private":"request"}`), RawResponse: json.RawMessage(`{"private":"response"}`)}, errors.New("private engine failure")
}
func (*engineFailureWorkflow) Close(context.Context) error { return nil }

func TestPythonRepairTreatmentRejectsEngineFailure(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	adapter := &scriptedAdapter{responses: []provider.Response{responseFixture(`{"status":"completed","output":[{"type":"function_call","status":"completed","call_id":"repair-engine","name":"run_python","arguments":"{\"code\":\"private\"}"}]}`, 5, 5)}}
	treatment, _ := LoadDevelopmentTreatment(filepath.Join(datasetRoot(t), "treatments", "python-safe-repair-v1.json"))
	identity := ExecutionIdentity{
		RepositoryCommit: strings.Repeat("a", 40), HostArtifactDigest: "sha256:" + strings.Repeat("a", 64),
		DatasetManifestDigest: "sha256:" + strings.Repeat("b", 64), ProviderCatalogDigest: "sha256:" + strings.Repeat("d", 64),
		ProviderCatalogObservedAt: "2026-07-26T11:00:00Z", GuestArtifactDigest: "sha256:" + strings.Repeat("c", 64), GuestProfile: "core",
	}
	limits := developmentTrialLimits(len(task.Interaction.Turns))
	limits.MaxPythonRuns++
	limits.MaxProviderCalls++
	result, err := RunDevelopmentDiagnosticTrialForModelWithIdentityAndTreatment(context.Background(), adapter, task, ConditionPython, developmentModel, 0, limits, identity, treatment, func(*ToolRuntime) (PythonWorkflow, error) {
		return &engineFailureWorkflow{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ErrorCode != "python_engine_failure" || result.Repair != nil || result.ProviderCalls != 1 || result.PythonAttempts != 1 || result.PythonRuns != 0 || ValidateTrialResult(result) != nil {
		t.Fatalf("result=%+v", result)
	}
}

func TestPythonRepairTreatmentRejectsHostToolFailure(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	adapter := &scriptedAdapter{responses: []provider.Response{responseFixture(`{"status":"completed","output":[{"type":"function_call","status":"completed","call_id":"repair-host","name":"run_python","arguments":"{\"code\":\"private\"}"}]}`, 5, 5)}}
	treatment, _ := LoadDevelopmentTreatment(filepath.Join(datasetRoot(t), "treatments", "python-safe-repair-v1.json"))
	identity := ExecutionIdentity{
		RepositoryCommit: strings.Repeat("a", 40), HostArtifactDigest: "sha256:" + strings.Repeat("a", 64),
		DatasetManifestDigest: "sha256:" + strings.Repeat("b", 64), ProviderCatalogDigest: "sha256:" + strings.Repeat("d", 64),
		ProviderCatalogObservedAt: "2026-07-26T11:00:00Z", GuestArtifactDigest: "sha256:" + strings.Repeat("c", 64), GuestProfile: "core",
	}
	limits := developmentTrialLimits(len(task.Interaction.Turns))
	limits.MaxPythonRuns++
	limits.MaxProviderCalls++
	result, err := RunDevelopmentDiagnosticTrialForModelWithIdentityAndTreatment(context.Background(), adapter, task, ConditionPython, developmentModel, 0, limits, identity, treatment, func(tools *ToolRuntime) (PythonWorkflow, error) {
		return &guestFailureWorkflow{tools: tools, failureClass: FailureClassHostToolError, invokeHost: true}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ErrorCode != "python_guest_error" || result.Repair != nil || result.ProviderCalls != 1 || result.PythonRuns != 1 || result.FailureDetail == nil || result.FailureDetail.RetryEligible {
		t.Fatalf("result=%+v", result)
	}
}

func TestRunnerRecordsBoundedGuestFailureDetail(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	identity := ExecutionIdentity{
		RepositoryCommit: strings.Repeat("a", 40), HostArtifactDigest: "sha256:" + strings.Repeat("a", 64),
		DatasetManifestDigest: "sha256:" + strings.Repeat("b", 64), ProviderCatalogDigest: "sha256:" + strings.Repeat("d", 64),
		ProviderCatalogObservedAt: "2026-07-26T11:00:00Z", GuestArtifactDigest: "sha256:" + strings.Repeat("c", 64), GuestProfile: "core",
	}
	for _, test := range []struct {
		name          string
		failureClass  FailureClass
		invokeHost    bool
		retryEligible bool
		calls         uint32
	}{
		{name: "zero-call-python", failureClass: FailureClassPythonException, retryEligible: true},
		{name: "Host-tool", failureClass: FailureClassHostToolError, invokeHost: true, retryEligible: false, calls: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			adapter := &scriptedAdapter{responses: []provider.Response{responseFixture(`{"status":"completed","output":[{"type":"function_call","status":"completed","call_id":"py1","name":"run_python","arguments":"{\"code\":\"result = {}\"}"}]}`, 5, 5)}}
			result, err := RunDevelopmentTrialWithIdentity(context.Background(), adapter, task, ConditionPython, 0, developmentTrialLimits(len(task.Interaction.Turns)), identity, func(tools *ToolRuntime) (PythonWorkflow, error) {
				return &guestFailureWorkflow{tools: tools, failureClass: test.failureClass, invokeHost: test.invokeHost}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.ErrorCode != "python_guest_error" || result.FailureDetail == nil || result.FailureDetail.Class != test.failureClass ||
				result.FailureDetail.Turn != 0 || result.FailureDetail.CapabilityCallsBefore != test.calls || result.FailureDetail.RetryEligible != test.retryEligible || ValidateTrialResult(result) != nil {
				t.Fatalf("result=%+v", result)
			}
			tampered := result
			detail := *result.FailureDetail
			detail.RetryEligible = !detail.RetryEligible
			tampered.FailureDetail = &detail
			if ValidateTrialResult(tampered) == nil {
				t.Fatal("tampered retry eligibility accepted")
			}
			tampered = result
			detail = *result.FailureDetail
			detail.Turn++
			tampered.FailureDetail = &detail
			if ValidateTrialResult(tampered) == nil {
				t.Fatal("tampered failure turn accepted")
			}
		})
	}
}

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

func TestPreboundCompactJSONTrialBindsSourceEvidenceAndPrompt(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	responses := make([]provider.Response, len(task.Interaction.Turns))
	for index := range responses {
		body := fmt.Sprintf(`{"status":"completed","model":"gpt-5.6-luna","output":[{"type":"function_call","status":"completed","call_id":"compact-%d","name":"run_python","arguments":"{\"code\":\"pwd()\"}"}]}`, index)
		responses[index] = responseFixture(body, 20, 5)
	}
	treatment, err := LoadDevelopmentTreatment(filepath.Join(datasetRoot(t), "treatments", "hybrid-two-stage-prebound-compact-json-v4.json"))
	if err != nil {
		t.Fatal(err)
	}
	identity := ExecutionIdentity{
		RepositoryCommit: strings.Repeat("a", 40), HostArtifactDigest: "sha256:" + strings.Repeat("a", 64),
		DatasetManifestDigest: "sha256:" + strings.Repeat("b", 64), ProviderCatalogDigest: "sha256:" + strings.Repeat("d", 64),
		ProviderCatalogObservedAt: "2026-07-26T11:00:00Z", GuestArtifactDigest: "sha256:" + strings.Repeat("c", 64), GuestProfile: "core",
	}
	factory := func(tools *ToolRuntime) (PythonWorkflow, error) {
		var oracle StatefulOracle
		if decodeStrict(task.Oracle, &oracle) != nil {
			return nil, ErrDataset
		}
		return &oracleWorkflow{tools: tools, oracle: oracle, compact: true}, nil
	}
	limits := developmentTrialLimits(len(task.Interaction.Turns))
	limits.MaxProviderCalls++
	limits.MaxPythonRuns++
	result, err := RunDevelopmentDiagnosticTrialForModelWithIdentityAndTreatment(
		context.Background(), &scriptedAdapter{responses: responses}, task, ConditionPython, luna56DevelopmentModel, 0,
		limits, identity, treatment, factory,
	)
	if err != nil || !result.Passed || ValidateTrialResult(result) != nil || result.RawDebug == nil ||
		!strings.Contains(result.RawDebug.DeveloperPrompt, "prebound") || !strings.Contains(result.RawDebug.DeveloperPrompt, "Every Available SDK function is keyword-only") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	for _, evidence := range result.PythonEvidence {
		if !validDigest(evidence.ModelCodeDigest) || !validDigest(evidence.EffectiveCodeDigest) || evidence.WrapperDigest != compactPythonWrapperDigest() {
			t.Fatalf("evidence=%+v", evidence)
		}
	}
	tampered := result
	tampered.PythonEvidence = append([]PythonRunResult(nil), result.PythonEvidence...)
	tampered.PythonEvidence[0].WrapperDigest = ""
	if ValidateTrialResult(tampered) == nil {
		t.Fatal("missing compact wrapper digest accepted")
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
		!strings.Contains(description, "touch(file_name: str) -> {}") ||
		!strings.Contains(description, "[returns an error if the file already exists; do not pre-check existence]") ||
		!strings.Contains(description, "echo(content: str, file_name: str=...) -> {terminal_output: str}") ||
		!strings.Contains(description, "[when file_name is provided, the target file must already exist; echo does not create missing files]") {
		t.Fatalf("surface=%d mapping=%v prompt=%q description=%q", len(surface), mapping, prompt, description)
	}
}

func TestPreboundCompactPythonSurfaceRequiresDirectMinimalCode(t *testing.T) {
	task := findAgenticTask(t, "bfcl-v4-stateful-local-tools-multi_turn_base_12")
	runtime, err := NewToolRuntime(task)
	if err != nil {
		t.Fatal(err)
	}
	treatment, err := LoadDevelopmentTreatment(filepath.Join(datasetRoot(t), "treatments", "hybrid-two-stage-prebound-compact-v3.json"))
	if err != nil {
		t.Fatal(err)
	}
	surface, _, prompt, err := buildConditionSurfaceForTreatment(runtime, ConditionPython, false, treatment)
	if err != nil {
		t.Fatal(err)
	}
	description, _ := surface[0]["description"].(string)
	for _, required := range []string{"prebound", "call them directly", "result defaults to {}", "bare calls", "no unused return bindings"} {
		if !strings.Contains(prompt+" "+description, required) {
			t.Fatalf("missing %q prompt=%q description=%q", required, prompt, description)
		}
	}
	if strings.Contains(prompt+" "+description, "Import functions from host_tools") || strings.Contains(prompt+" "+description, "Import every Host operation") || strings.Contains(prompt+" "+description, "Assign a JSON object to result") {
		t.Fatalf("legacy boilerplate survived prompt=%q description=%q", prompt, description)
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

func TestCompactPythonSDKIncludesBoundedResponseShapes(t *testing.T) {
	dataset, err := LoadRoutingDataset(filepath.Join(datasetRoot(t), "..", "routing", "v1"))
	if err != nil {
		t.Fatal(err)
	}
	var task Task
	for _, candidate := range dataset.Tasks {
		if candidate.ID == "rd-003" {
			task = candidate
			break
		}
	}
	runtime, err := NewToolRuntime(task)
	if err != nil {
		t.Fatal(err)
	}
	sdk := compactPythonSDK(runtime)
	for _, expected := range []string{
		"cat(file_name: str) -> {file_content: str}",
		"touch(file_name: str) -> {}",
		"file_name: The name of the file from current directory to display. No path is allowed.",
		"folder: The folder of the directory to change to. You can only change one folder level at a time.",
	} {
		if !strings.Contains(sdk, expected) {
			t.Fatalf("SDK omitted response shape %q: %s", expected, sdk)
		}
	}
}

func TestExactPlanTreatmentAddsGenericExecutionContract(t *testing.T) {
	dataset, err := LoadRoutingDataset(filepath.Join(datasetRoot(t), "..", "routing", "v1"))
	if err != nil {
		t.Fatal(err)
	}
	var task Task
	for _, candidate := range dataset.Tasks {
		if candidate.ID == "rd-006" {
			task = candidate
			break
		}
	}
	if task.ID == "" {
		t.Fatal("task rd-006 not found")
	}
	runtime, err := NewToolRuntime(task)
	if err != nil {
		t.Fatal(err)
	}
	treatment, err := LoadDevelopmentTreatment(filepath.Join(datasetRoot(t), "treatments", "hybrid-two-stage-prebound-exact-plan-v5.json"))
	if err != nil {
		t.Fatal(err)
	}
	_, _, prompt, err := buildConditionSurfaceForTreatment(runtime, ConditionPython, false, treatment)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"internally form the exact required Host-call sequence",
		"Include every directory change required by the user request",
		"extract the named field shown in the SDK response shape",
		"Do not catch or suppress Host-tool exceptions",
	} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("prompt missing %q: %s", expected, prompt)
		}
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

package agenttrajectory_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/agenttrajectory"
)

type scriptedProvider struct {
	responses []string
	requests  []agenttrajectory.ModelRequest
}

func (provider *scriptedProvider) Complete(_ context.Context, request agenttrajectory.ModelRequest) (agenttrajectory.ModelResult, error) {
	provider.requests = append(provider.requests, request)
	if len(provider.responses) == 0 {
		return agenttrajectory.ModelResult{}, errors.New("unexpected model call")
	}
	content := provider.responses[0]
	provider.responses = provider.responses[1:]
	rawRequest, _ := json.Marshal(map[string]any{"messages": request.Messages})
	rawResponse, _ := json.Marshal(map[string]any{"id": "fixture", "model": "deepseek-v4-flash", "choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"role": "assistant", "content": content}}}})
	return agenttrajectory.ModelResult{CallID: request.CallID, ActorID: request.ActorID, Model: "deepseek-v4-flash", Content: content, RawRequest: rawRequest, RawResponse: rawResponse}, nil
}

func (provider *scriptedProvider) CallCount() uint32 { return uint32(len(provider.requests)) }

type recordingSpy struct {
	calls []string
}

func (recorder *recordingSpy) RecordModelCall(_ context.Context, request agenttrajectory.ModelRequest, result agenttrajectory.ModelResult) error {
	if request.CallID != result.CallID || request.ActorID != result.ActorID {
		return errors.New("recording linkage drift")
	}
	recorder.calls = append(recorder.calls, request.CallID)
	return nil
}

type fakeCandidateExecutor struct {
	sources  map[string]string
	selected string
}

func (executor *fakeCandidateExecutor) ExecuteCandidates(_ context.Context, candidates []agenttrajectory.CandidateResponse) ([]agenttrajectory.CandidateExecution, error) {
	if len(candidates) != 2 {
		return nil, errors.New("expected two candidates")
	}
	executor.sources = map[string]string{}
	results := make([]agenttrajectory.CandidateExecution, 0, len(candidates))
	for _, candidate := range candidates {
		executor.sources[candidate.CandidateID] = candidate.PythonSource
		if candidate.CandidateID == agenttrajectory.CandidateBrighton {
			results = append(results, agenttrajectory.CandidateExecution{CandidateID: candidate.CandidateID, Output: json.RawMessage(`{"candidate_id":"brighton","observation":"OBSERVED-BRIGHTON-ONLY","total_cost_gbp":94}`), WorkspaceSHA256: digestForTest("brighton-workspace")})
			continue
		}
		results = append(results, agenttrajectory.CandidateExecution{CandidateID: candidate.CandidateID, Output: json.RawMessage(`{"candidate_id":"oxford","observation":"OBSERVED-OXFORD-ONLY","total_cost_gbp":86}`), WorkspaceSHA256: digestForTest("oxford-workspace")})
	}
	return results, nil
}

func (executor *fakeCandidateExecutor) Seal(_ context.Context, selected string) (agenttrajectory.BranchResult, error) {
	if selected != agenttrajectory.CandidateOxford || len(executor.sources) != 2 {
		return agenttrajectory.BranchResult{}, errors.New("unexpected selection")
	}
	executor.selected = selected
	return agenttrajectory.BranchResult{SelectedCandidateID: selected, SelectedRootSHA256: digestForTest("selected-root"), DiscardedCandidateIDs: []string{agenttrajectory.CandidateBrighton}}, nil
}

func TestHarnessUsesFourNecessaryModelCallsAndObservedGuestOutputs(t *testing.T) {
	fixture, err := agenttrajectory.LoadFixture("testdata/day-trip-planning")
	if err != nil {
		t.Fatal(err)
	}
	provider := &scriptedProvider{responses: []string{
		`{"schema_version":"pysolate.day-trip-candidate.v1","candidate_id":"brighton","summary":"Check Brighton weather, return rail fare, and the open attraction before calculating the observed total.","python_source":"weather = travel.weather(\"brighton\")\nrail = travel.rail(\"brighton\", travellers=2)\nsite = travel.attractions(\"brighton\")\nresult = {\"candidate_id\": \"brighton\", \"weather\": weather, \"rail\": rail, \"attraction\": site, \"total_cost_gbp\": rail[\"total_cost_gbp\"] + 2 * site[\"entry_cost_gbp\"]}"}`,
		`{"schema_version":"pysolate.day-trip-candidate.v1","candidate_id":"oxford","summary":"Check Oxford weather, return rail fare, and the open attraction before calculating the observed total.","python_source":"weather = travel.weather(\"oxford\")\nrail = travel.rail(\"oxford\", travellers=2)\nsite = travel.attractions(\"oxford\")\nresult = {\"candidate_id\": \"oxford\", \"weather\": weather, \"rail\": rail, \"attraction\": site, \"total_cost_gbp\": rail[\"total_cost_gbp\"] + 2 * site[\"entry_cost_gbp\"]}"}`,
		`{"schema_version":"pysolate.day-trip-selection.v1","selected_candidate_id":"oxford","justification":"The observed Oxford branch costs GBP 86 and remains within budget."}`,
		`{"schema_version":"pysolate.day-trip-final.v1","selected_candidate_id":"oxford","itinerary":"Take the observed morning train, visit the open Oxford attraction, and return on the observed evening service.","total_cost_gbp":86}`,
	}}
	recorder := &recordingSpy{}
	executor := &fakeCandidateExecutor{}
	harness, err := agenttrajectory.NewHarness(agenttrajectory.HarnessConfig{Fixture: fixture, Provider: provider, Recorder: recorder, Executor: executor})
	if err != nil {
		t.Fatal(err)
	}
	result, err := harness.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(provider.requests) != 4 || len(recorder.calls) != 4 || len(provider.responses) != 0 {
		t.Fatalf("calls provider=%d recorder=%d remaining=%d", len(provider.requests), len(recorder.calls), len(provider.responses))
	}
	if result.Selection.SelectedCandidateID != agenttrajectory.CandidateOxford || result.Final.SelectedCandidateID != agenttrajectory.CandidateOxford || executor.selected != agenttrajectory.CandidateOxford {
		t.Fatalf("result=%+v selected=%s", result, executor.selected)
	}
	selectionPrompt := provider.requests[2].Messages[len(provider.requests[2].Messages)-1].Content
	if !strings.Contains(selectionPrompt, "OBSERVED-BRIGHTON-ONLY") || !strings.Contains(selectionPrompt, "OBSERVED-OXFORD-ONLY") {
		t.Fatalf("selection prompt omitted observed Guest outputs: %s", selectionPrompt)
	}
	finalPrompt := provider.requests[3].Messages[len(provider.requests[3].Messages)-1].Content
	if !strings.Contains(finalPrompt, result.Branch.SelectedRootSHA256) || !strings.Contains(finalPrompt, "OBSERVED-OXFORD-ONLY") {
		t.Fatalf("final prompt omitted selected branch evidence: %s", finalPrompt)
	}
}

func TestHarnessFailsClosedBeforeSelectionWhenCandidateOutputIsInvalid(t *testing.T) {
	fixture, err := agenttrajectory.LoadFixture("testdata/day-trip-planning")
	if err != nil {
		t.Fatal(err)
	}
	provider := &scriptedProvider{responses: []string{
		`{"schema_version":"pysolate.day-trip-candidate.v1","candidate_id":"brighton","summary":"candidate","python_source":"weather = travel.weather(\"brighton\")\nrail = travel.rail(\"brighton\", travellers=2)\nattraction = travel.attractions(\"brighton\")\nresult = {\"candidate_id\": \"brighton\", \"weather\": weather, \"rail\": rail, \"attraction\": attraction, \"total_cost_gbp\": rail[\"total_cost_gbp\"] + attraction[\"entry_cost_gbp\"] * 2}"}`,
		`{"schema_version":"pysolate.day-trip-candidate.v1","candidate_id":"oxford","summary":"candidate","python_source":"weather = travel.weather(\"oxford\")\nrail = travel.rail(\"oxford\", travellers=2)\nattraction = travel.attractions(\"oxford\")\nresult = {\"candidate_id\": \"oxford\", \"weather\": weather, \"rail\": rail, \"attraction\": attraction, \"total_cost_gbp\": rail[\"total_cost_gbp\"] + attraction[\"entry_cost_gbp\"] * 2}"}`,
	}}
	executor := &fakeCandidateExecutor{}
	executorResult := agenttrajectory.CandidateExecution{}
	_ = executorResult
	bad := failingExecutor{delegate: executor}
	harness, err := agenttrajectory.NewHarness(agenttrajectory.HarnessConfig{Fixture: fixture, Provider: provider, Recorder: &recordingSpy{}, Executor: bad})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := harness.Run(context.Background()); err == nil || len(provider.requests) != 2 {
		t.Fatalf("invalid candidate execution did not stop before selection: calls=%d err=%v", len(provider.requests), err)
	}
}

type failingExecutor struct {
	delegate *fakeCandidateExecutor
}

func (executor failingExecutor) ExecuteCandidates(_ context.Context, _ []agenttrajectory.CandidateResponse) ([]agenttrajectory.CandidateExecution, error) {
	return nil, errors.New("guest failed")
}

func (executor failingExecutor) Seal(ctx context.Context, selected string) (agenttrajectory.BranchResult, error) {
	return executor.delegate.Seal(ctx, selected)
}

func digestForTest(value string) string {
	const hex = "0123456789abcdef"
	result := make([]byte, 64)
	for index := range result {
		result[index] = hex[(index+len(value))%len(hex)]
	}
	return "sha256:" + string(result)
}

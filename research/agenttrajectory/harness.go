package agenttrajectory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

var harnessDigestRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

type ModelCallRecorder interface {
	RecordModelCall(context.Context, ModelRequest, ModelResult) error
}

type CandidateExecution struct {
	CandidateID     string          `json:"candidate_id"`
	Output          json.RawMessage `json:"output"`
	WorkspaceSHA256 string          `json:"workspace_sha256"`
}

type BranchResult struct {
	SelectedCandidateID   string   `json:"selected_candidate_id"`
	SelectedRootSHA256    string   `json:"selected_root_sha256"`
	DiscardedCandidateIDs []string `json:"discarded_candidate_ids"`
}

type CandidateExecutor interface {
	ExecuteCandidates(context.Context, []CandidateResponse) ([]CandidateExecution, error)
	Seal(context.Context, string) (BranchResult, error)
}

type HarnessConfig struct {
	Fixture  Fixture
	Provider ModelProvider
	Recorder ModelCallRecorder
	Executor CandidateExecutor
}

type Harness struct {
	config HarnessConfig
}

type HarnessResult struct {
	Planning   PlanningBrief        `json:"planning"`
	Candidates []CandidateResponse  `json:"candidates"`
	Executions []CandidateExecution `json:"executions"`
	Selection  SelectionResponse    `json:"selection"`
	Branch     BranchResult         `json:"branch"`
	Final      FinalResponse        `json:"final"`
}

func NewHarness(config HarnessConfig) (*Harness, error) {
	if config.Provider == nil || config.Recorder == nil || config.Executor == nil || config.Fixture.AggregateSHA256 != DayTripFixtureAggregateSHA256 || config.Fixture.System == "" || len(config.Fixture.Skills) != 3 {
		return nil, errors.New("invalid agent trajectory harness")
	}
	return &Harness{config: config}, nil
}

func (harness *Harness) Run(ctx context.Context) (HarnessResult, error) {
	if harness == nil {
		return HarnessResult{}, errors.New("invalid agent trajectory harness")
	}
	system := harness.publicSystem()
	planning := PlanningBrief{SchemaVersion: PlanningBriefSchemaVersion, Task: harness.config.Fixture.User.Request, CandidateIDs: []string{CandidateBrighton, CandidateOxford}}
	if planning.Validate() != nil {
		return HarnessResult{}, ErrInvalidContract
	}

	candidates := make([]CandidateResponse, 0, 2)
	for _, candidateID := range []string{CandidateBrighton, CandidateOxford} {
		candidateResult, callErr := harness.call(ctx, ModelRequest{
			CallID: "candidate-" + candidateID, ActorID: candidateID, ResponseKind: ResponseCandidate,
			Messages: []ModelMessage{{Role: "system", Content: system + "\n\nYou are the " + candidateID + " candidate Agent."}, {Role: "user", Content: candidatePrompt(planning, candidateID)}},
		})
		if callErr != nil {
			return HarnessResult{}, callErr
		}
		candidate, decodeErr := normalizeCandidateResponse(candidateResult.Content)
		if decodeErr != nil || candidate.CandidateID != candidateID {
			return HarnessResult{}, fmt.Errorf("decode %s candidate output: %w", candidateID, ErrInvalidContract)
		}
		candidates = append(candidates, candidate)
	}

	executions, err := harness.config.Executor.ExecuteCandidates(ctx, append([]CandidateResponse(nil), candidates...))
	if err != nil || validateCandidateExecutions(executions) != nil {
		return HarnessResult{}, errors.New("candidate Guest execution failed")
	}
	executionBody, err := json.Marshal(executions)
	if err != nil {
		return HarnessResult{}, err
	}
	selectionResult, err := harness.call(ctx, ModelRequest{
		CallID: "main-select", ActorID: "main", ResponseKind: ResponseSelection,
		Messages: []ModelMessage{{Role: "system", Content: system}, {Role: "user", Content: "Choose exactly one candidate using only these observed Guest outputs. Return exactly this JSON shape with no other keys: {\"schema_version\":\"pysolate.day-trip-selection.v1\",\"selected_candidate_id\":\"brighton or oxford\",\"justification\":\"concise evidence-based reason\"}.\n" + string(executionBody)}},
	})
	if err != nil {
		return HarnessResult{}, err
	}
	selection, err := normalizeSelectionResponse(selectionResult.Content)
	if err != nil {
		return HarnessResult{}, fmt.Errorf("decode main selection output: %w", ErrInvalidContract)
	}
	selectedCost, err := observedCandidateCost(executions, selection.SelectedCandidateID)
	if err != nil || selectedCost > harness.config.Fixture.User.BudgetGBP {
		return HarnessResult{}, errors.New("main selection did not choose an observed in-budget candidate")
	}
	branch, err := harness.config.Executor.Seal(ctx, selection.SelectedCandidateID)
	if err != nil || branch.Validate() != nil || branch.SelectedCandidateID != selection.SelectedCandidateID {
		return HarnessResult{}, errors.New("candidate branch selection failed")
	}
	selectedExecution, ok := findExecution(executions, selection.SelectedCandidateID)
	if !ok {
		return HarnessResult{}, errors.New("selected Guest output missing")
	}
	finalContext, err := json.Marshal(struct {
		Selection SelectionResponse  `json:"selection"`
		Branch    BranchResult       `json:"branch"`
		Observed  CandidateExecution `json:"observed_selected_candidate"`
	}{Selection: selection, Branch: branch, Observed: selectedExecution})
	if err != nil {
		return HarnessResult{}, err
	}
	finalResult, err := harness.call(ctx, ModelRequest{
		CallID: "main-final", ActorID: "main", ResponseKind: ResponseFinal,
		Messages: []ModelMessage{{Role: "system", Content: system}, {Role: "user", Content: "Write the final itinerary from the selected observed branch only. Return exactly this JSON shape with no other keys: {\"schema_version\":\"pysolate.day-trip-final.v1\",\"selected_candidate_id\":\"selected ID\",\"itinerary\":\"concise itinerary using observed times and attraction\",\"total_cost_gbp\":78}. The number must equal the observed selected total.\n" + string(finalContext)}},
	})
	if err != nil {
		return HarnessResult{}, err
	}
	final, err := normalizeFinalResponse(finalResult.Content)
	if err != nil || final.SelectedCandidateID != branch.SelectedCandidateID || final.TotalCostGBP != selectedCost {
		return HarnessResult{}, fmt.Errorf("decode main final output: %w", ErrInvalidContract)
	}
	return HarnessResult{Planning: planning, Candidates: candidates, Executions: executions, Selection: selection, Branch: branch, Final: final}, nil
}

func observedCandidateCost(executions []CandidateExecution, candidateID string) (float64, error) {
	for _, execution := range executions {
		if execution.CandidateID != candidateID {
			continue
		}
		var value struct {
			TotalCostGBP float64 `json:"total_cost_gbp"`
		}
		if json.Unmarshal(execution.Output, &value) != nil || value.TotalCostGBP < 0 {
			return 0, ErrInvalidContract
		}
		return value.TotalCostGBP, nil
	}
	return 0, ErrInvalidContract
}

func (harness *Harness) call(ctx context.Context, request ModelRequest) (ModelResult, error) {
	result, err := harness.config.Provider.Complete(ctx, request)
	if err != nil {
		return ModelResult{}, err
	}
	if err := harness.config.Recorder.RecordModelCall(ctx, request, result); err != nil {
		return ModelResult{}, err
	}
	return result, nil
}

func (harness *Harness) publicSystem() string {
	ids := make([]string, 0, len(harness.config.Fixture.Skills))
	for id := range harness.config.Fixture.Skills {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var output strings.Builder
	output.WriteString(harness.config.Fixture.System)
	for _, id := range ids {
		output.WriteString("\n\n# Loaded public skill: ")
		output.WriteString(id)
		output.WriteByte('\n')
		output.WriteString(harness.config.Fixture.Skills[id])
	}
	return output.String()
}

func candidatePrompt(planning PlanningBrief, candidateID string) string {
	planningBody, _ := json.Marshal(planning)
	return fmt.Sprintf("Create only the %s candidate. Return exactly this JSON shape with no other keys: {\"schema_version\":\"pysolate.day-trip-candidate.v1\",\"candidate_id\":%q,\"summary\":\"what the code observes\",\"python_source\":\"Python source string\"}. python_source must call travel.weather(%q), travel.rail(%q, travellers=2), and travel.attractions(%q) exactly once, then assign result={\"candidate_id\":%q,\"weather\":weather,\"rail\":rail,\"attraction\":attraction,\"total_cost_gbp\":rail[\"total_cost_gbp\"]+attraction[\"entry_cost_gbp\"]*2}. Do not invent API values and do not include markdown fences. Planning brief:\n%s", candidateID, candidateID, candidateID, candidateID, candidateID, candidateID, planningBody)
}

func (execution CandidateExecution) Validate() error {
	if !validCandidateID(execution.CandidateID) || !harnessDigestRE.MatchString(execution.WorkspaceSHA256) || len(execution.Output) == 0 || len(execution.Output) > MaxContractJSONBytes || scanJSON(execution.Output) != nil {
		return ErrInvalidContract
	}
	var summary struct {
		CandidateID  string  `json:"candidate_id"`
		TotalCostGBP float64 `json:"total_cost_gbp"`
	}
	if json.Unmarshal(execution.Output, &summary) != nil || summary.CandidateID != execution.CandidateID || summary.TotalCostGBP < 0 || summary.TotalCostGBP > 1000 {
		return ErrInvalidContract
	}
	return nil
}

func (branch BranchResult) Validate() error {
	if !validCandidateID(branch.SelectedCandidateID) || !harnessDigestRE.MatchString(branch.SelectedRootSHA256) || len(branch.DiscardedCandidateIDs) != 1 || !validCandidateID(branch.DiscardedCandidateIDs[0]) || branch.DiscardedCandidateIDs[0] == branch.SelectedCandidateID {
		return ErrInvalidContract
	}
	return nil
}

func validateCandidateExecutions(executions []CandidateExecution) error {
	if len(executions) != 2 {
		return ErrInvalidContract
	}
	seen := map[string]bool{}
	for _, execution := range executions {
		if execution.Validate() != nil || seen[execution.CandidateID] {
			return ErrInvalidContract
		}
		seen[execution.CandidateID] = true
	}
	if !seen[CandidateBrighton] || !seen[CandidateOxford] {
		return ErrInvalidContract
	}
	return nil
}

func findExecution(executions []CandidateExecution, candidateID string) (CandidateExecution, bool) {
	for _, execution := range executions {
		if execution.CandidateID == candidateID {
			return execution, true
		}
	}
	return CandidateExecution{}, false
}

func normalizePlanningBrief(content string) (PlanningBrief, error) {
	var value PlanningBrief
	err := normalizeModelContract(content, &value, func() error { return value.Validate() })
	return value, err
}

func normalizeCandidateResponse(content string) (CandidateResponse, error) {
	var value CandidateResponse
	err := normalizeModelContract(content, &value, func() error { return value.Validate() })
	return value, err
}

func normalizeSelectionResponse(content string) (SelectionResponse, error) {
	var value SelectionResponse
	err := normalizeModelContract(content, &value, func() error { return value.Validate() })
	return value, err
}

func normalizeFinalResponse(content string) (FinalResponse, error) {
	var value FinalResponse
	err := normalizeModelContract(content, &value, func() error { return value.Validate() })
	return value, err
}

func normalizeModelContract(content string, destination any, validate func() error) error {
	raw := []byte(content)
	if len(raw) == 0 || len(raw) > MaxContractJSONBytes || scanJSON(raw) != nil {
		return ErrInvalidContract
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(destination) != nil {
		return ErrInvalidContract
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return ErrInvalidContract
	}
	return validate()
}

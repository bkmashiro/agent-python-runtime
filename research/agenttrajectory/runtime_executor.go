package agenttrajectory

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/subagent"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
	experimentalsys "github.com/tetratelabs/wazero/experimental/sys"
)

type PysolateExecutorConfig struct {
	Artifact      []byte
	Fixture       Fixture
	WorkspaceRoot string
}

type TravelCallEvent struct {
	Phase       string
	API         string
	Destination string
	LatencyMS   int
	Outcome     string
}

type TravelCallObserver func(TravelCallEvent)

type PysolateCandidateExecutor struct {
	artifact       []byte
	artifactSHA256 string
	fixture        Fixture
	workspaceRoot  string
	manager        *workspace.Manager
	base           workspace.Ref
	parentSHA256   string
	parentLineage  string
	profile        runtimeconfig.RunConfig
	profileSHA256  string
	parentPlan     *capability.Plan
	childPlan      *capability.Plan
	orchestrator   *subagent.Orchestrator
	programs       map[string]CandidateResponse

	mu           sync.Mutex
	outputs      map[string]json.RawMessage
	workspaceSHA map[string]string
	selectedRoot workspace.Ref
	sealed       bool
	closed       bool
}

func NewPysolateCandidateExecutor(config PysolateExecutorConfig) (*PysolateCandidateExecutor, error) {
	if len(config.Artifact) == 0 || config.Fixture.AggregateSHA256 != DayTripFixtureAggregateSHA256 || config.Fixture.User.Validate() != nil || config.Fixture.Workspace.Request.Validate() != nil || config.Fixture.Workspace.API.Validate() != nil || config.WorkspaceRoot == "" || !filepath.IsAbs(config.WorkspaceRoot) || filepath.Clean(config.WorkspaceRoot) != config.WorkspaceRoot {
		return nil, errors.New("invalid Pysolate day-trip executor config")
	}
	if err := os.Mkdir(config.WorkspaceRoot, 0o700); err != nil {
		return nil, err
	}
	manager, err := workspace.NewManager(config.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	base, err := manager.Create(nil, workspace.DefaultLimits())
	if err != nil {
		manager.Close()
		return nil, err
	}
	info, err := manager.Inspect(base)
	if err != nil {
		manager.Close()
		return nil, err
	}
	lineage, _, err := manager.PortableIdentity(base)
	if err != nil {
		manager.Close()
		return nil, err
	}
	parentPlan, err := buildTravelPlan(config.Fixture, 6)
	if err != nil {
		manager.Close()
		return nil, err
	}
	childPlan, err := buildTravelPlan(config.Fixture, 3)
	if err != nil {
		manager.Close()
		return nil, err
	}
	profile := runtimeconfig.DefaultRunConfig()
	profile.ProgramSurface = runtimeconfig.ProgramSurfaceProgrammatic
	profile.Mechanisms.ProgrammaticToolCalling = true
	profile.Mechanisms.PrivateWorkspace = true
	profileBytes, _ := json.Marshal(profile)
	executor := &PysolateCandidateExecutor{
		artifact: append([]byte(nil), config.Artifact...), artifactSHA256: digestBytes(config.Artifact), fixture: config.Fixture,
		workspaceRoot: config.WorkspaceRoot, manager: manager, base: base, parentSHA256: info.WorkspaceSHA256, parentLineage: lineage,
		profile: profile, profileSHA256: digestBytes(profileBytes), parentPlan: parentPlan, childPlan: childPlan,
		programs: map[string]CandidateResponse{}, outputs: map[string]json.RawMessage{}, workspaceSHA: map[string]string{},
	}
	return executor, nil
}

func (executor *PysolateCandidateExecutor) ExecuteCandidates(ctx context.Context, candidates []CandidateResponse) ([]CandidateExecution, error) {
	if executor == nil || ctx == nil || executor.closed || executor.orchestrator != nil || len(candidates) != 2 {
		return nil, errors.New("invalid Pysolate candidate execution")
	}
	for _, candidate := range candidates {
		if candidate.Validate() != nil {
			return nil, ErrInvalidContract
		}
		executor.programs[candidate.CandidateID] = candidate
	}
	if len(executor.programs) != 2 {
		return nil, ErrInvalidContract
	}
	wrapped := subagent.ExecutorFunc(func(runContext context.Context, invocation subagent.Invocation) error {
		candidate, ok := executor.programs[invocation.Descriptor.ChildID]
		if !ok {
			return ErrInvalidContract
		}
		executableSource, err := executableCandidateSource(candidate.PythonSource, candidate.CandidateID)
		if err != nil {
			return err
		}
		request, err := json.Marshal(struct {
			RunID  string         `json:"run_id"`
			Code   string         `json:"code"`
			Inputs map[string]any `json:"inputs"`
		}{RunID: "day-trip-" + invocation.Descriptor.ChildID, Code: executableSource, Inputs: map[string]any{}})
		if err != nil {
			return err
		}
		parentCallID := "day-trip-parent-" + invocation.Descriptor.ChildID
		presentation, err := executor.childPlan.Present(capability.ProgramSurfaceProgrammatic, parentCallID)
		if err != nil {
			return err
		}
		factory := wazeroengine.Factory{
			WorkspaceManager: executor.manager, WorkspaceRef: invocation.WorkspaceRef, WorkspaceOwner: "day-trip-" + invocation.Descriptor.ChildID,
			BrokerFactory: func(context.Context) (*capability.Broker, error) {
				return capability.NewBroker(capability.Config{RunIdentity: "day-trip-" + invocation.Descriptor.ChildID, Plan: executor.childPlan, ProgrammaticParentCallID: parentCallID})
			},
		}
		runner, err := factory.New(runContext, executor.artifact, executor.profile)
		if err != nil {
			return err
		}
		response, runErr := runner.Run(runContext, request, presentation.PythonPrelude)
		closeErr := runner.Close(runContext)
		if runErr != nil || closeErr != nil {
			return errors.Join(runErr, closeErr)
		}
		body, err := parseObservedCandidate(response, invocation.Descriptor.ChildID, executor.fixture)
		if err != nil {
			return fmt.Errorf("parse %s Guest response %s: %w", invocation.Descriptor.ChildID, string(response), err)
		}
		executor.mu.Lock()
		executor.outputs[invocation.Descriptor.ChildID] = append(json.RawMessage(nil), body...)
		executor.mu.Unlock()
		if err := writeBranchOutput(executor.manager, invocation.WorkspaceRef, invocation.Descriptor.ChildID, body); err != nil {
			return err
		}
		info, err := executor.manager.Inspect(invocation.WorkspaceRef)
		if err != nil {
			return err
		}
		executor.mu.Lock()
		executor.workspaceSHA[invocation.Descriptor.ChildID] = info.WorkspaceSHA256
		executor.mu.Unlock()
		return nil
	})
	orchestrator, err := subagent.New(subagent.Config{
		Manager: executor.manager, ParentRef: executor.base, ParentWorkspaceSHA256: executor.parentSHA256, ParentLineage: executor.parentLineage,
		MaxFanout: 2, MaxDepth: 1, ParentPlan: executor.parentPlan, ChildPlans: map[string]*capability.Plan{executor.childPlan.Identity(): executor.childPlan}, MaxDelegatedCalls: 6, Executor: wrapped,
	})
	if err != nil {
		return nil, err
	}
	executor.orchestrator = orchestrator
	for _, candidateID := range []string{CandidateBrighton, CandidateOxford} {
		candidate := executor.programs[candidateID]
		descriptor := subagent.Descriptor{
			SchemaVersion: subagent.DescriptorSchemaVersion, ChildID: candidateID, ParentStreamEpoch: "day-trip-epoch-1", ParentLineageSHA256: executor.parentLineage,
			SourceOccurrence: "candidate-" + candidateID, SourceSHA256: digestBytes([]byte(candidate.PythonSource)), InputsSHA256: executor.fixture.AggregateSHA256,
			ArtifactSHA256: executor.artifactSHA256, ExecutionProfileSHA256: executor.profileSHA256, ChildPlanSHA256: executor.childPlan.Identity(), PrivacyPartition: "day-trip-private-attempt", Depth: 1,
		}
		if err := orchestrator.Stage(ctx, descriptor); err != nil {
			_ = orchestrator.Abort(context.Background(), subagent.ParentInvalid)
			return nil, err
		}
	}
	if _, err := orchestrator.Await(ctx); err != nil {
		return nil, err
	}
	results := make([]CandidateExecution, 0, 2)
	for _, candidateID := range []string{CandidateBrighton, CandidateOxford} {
		executor.mu.Lock()
		result := CandidateExecution{CandidateID: candidateID, Output: append(json.RawMessage(nil), executor.outputs[candidateID]...), WorkspaceSHA256: executor.workspaceSHA[candidateID]}
		executor.mu.Unlock()
		if result.Validate() != nil {
			return nil, fmt.Errorf("invalid observed Pysolate candidate id=%s workspace=%s output=%s", result.CandidateID, result.WorkspaceSHA256, string(result.Output))
		}
		results = append(results, result)
	}
	return results, nil
}

func (executor *PysolateCandidateExecutor) Seal(ctx context.Context, selected string) (BranchResult, error) {
	if executor == nil || ctx == nil || executor.closed || executor.sealed || executor.orchestrator == nil || !validCandidateID(selected) {
		return BranchResult{}, errors.New("invalid Pysolate branch selection")
	}
	joined, err := executor.orchestrator.Seal(ctx, selected)
	if err != nil {
		return BranchResult{}, err
	}
	discarded := CandidateBrighton
	if selected == CandidateBrighton {
		discarded = CandidateOxford
	}
	result := BranchResult{SelectedCandidateID: selected, DiscardedCandidateIDs: []string{discarded}, SelectedRootSHA256: joined.SelectedRoot.IdentitySHA256}
	if result.Validate() != nil {
		return BranchResult{}, errors.New("invalid Pysolate branch receipt")
	}
	executor.selectedRoot = joined.SelectedRoot.Ref()
	executor.sealed = true
	return result, nil
}

func (executor *PysolateCandidateExecutor) Close(ctx context.Context) error {
	if executor == nil {
		return nil
	}
	executor.mu.Lock()
	if executor.closed {
		executor.mu.Unlock()
		return nil
	}
	executor.closed = true
	executor.mu.Unlock()
	var abortErr error
	if executor.orchestrator != nil && !executor.sealed {
		abortErr = executor.orchestrator.Abort(ctx, subagent.ParentCancelled)
	}
	if executor.selectedRoot != "" {
		_ = executor.manager.Destroy(executor.selectedRoot)
	}
	closeErr := executor.manager.Close()
	removeErr := os.RemoveAll(executor.workspaceRoot)
	return errors.Join(abortErr, closeErr, removeErr)
}

func buildTravelPlan(fixture Fixture, maxCalls uint32) (*capability.Plan, error) {
	return NewTravelCapabilityPlan(fixture, maxCalls, nil)
}

// NewTravelCapabilityPlan exposes only the reviewed deterministic public
// fixture; it carries no provider request or response data.
func NewTravelCapabilityPlan(fixture Fixture, maxCalls uint32, observe TravelCallObserver) (*capability.Plan, error) {
	registry := capability.NewRegistry()
	grant, err := capability.NewGrant(json.RawMessage(`{"fixture":"public-day-trip","network":false}`))
	if err != nil {
		return nil, err
	}
	specs := []struct {
		spec capability.Spec
		kind string
	}{
		{travelCapabilitySpec("weather", json.RawMessage(`{"type":"object","properties":{"destination":{"type":"string","enum":["brighton","oxford"]}},"required":["destination"],"additionalProperties":false}`), json.RawMessage(`{"type":"object","properties":{"forecast":{"type":"string"},"high_c":{"type":"number"},"rain_chance_pct":{"type":"integer"}},"required":["forecast","high_c","rain_chance_pct"],"additionalProperties":false}`), []string{"destination"}), "weather"},
		{travelCapabilitySpec("rail", json.RawMessage(`{"type":"object","properties":{"destination":{"type":"string","enum":["brighton","oxford"]},"travellers":{"type":"integer","const":2}},"required":["destination","travellers"],"additionalProperties":false}`), json.RawMessage(`{"type":"object","properties":{"outbound":{"type":"object"},"return":{"type":"object"},"total_cost_gbp":{"type":"number"},"currency":{"type":"string","const":"GBP"}},"required":["outbound","return","total_cost_gbp","currency"],"additionalProperties":false}`), []string{"destination", "travellers"}), "rail"},
		{travelCapabilitySpec("attractions", json.RawMessage(`{"type":"object","properties":{"destination":{"type":"string","enum":["brighton","oxford"]}},"required":["destination"],"additionalProperties":false}`), json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},"open_saturday":{"type":"boolean"},"opening_hours":{"type":"string"},"entry_cost_gbp":{"type":"number"}},"required":["name","open_saturday","opening_hours","entry_cost_gbp"],"additionalProperties":false}`), []string{"destination"}), "attractions"},
	}
	for _, item := range specs {
		kind := item.kind
		handler := capability.HandlerFunc(func(ctx context.Context, arguments json.RawMessage) (json.RawMessage, error) {
			return callTravelFixtureObserved(ctx, fixture, kind, arguments, observe)
		})
		if err := registry.Register(item.spec, grant, handler); err != nil {
			return nil, fmt.Errorf("register travel.%s: %w", kind, err)
		}
	}
	return registry.Seal(capability.PlanConfig{MaxCalls: maxCalls})
}

func travelCapabilitySpec(method string, input, output json.RawMessage, arguments []string) capability.Spec {
	return capability.Spec{
		Name: "travel." + method, Version: "pysolate.day-trip-travel-api.v1", Description: "Deterministic delayed public day-trip API fixture.",
		EffectClass: capability.EffectExternalRead, Playback: capability.PlaybackLiveOnly, HandlerIdentity: "pysolate.day-trip-travel-api.v1-" + method,
		InputSchema: input, OutputSchema: output, Python: &capability.PythonProjection{Module: "travel", Method: method, Arguments: arguments}, ReadOnly: true, Idempotent: true,
		PreDispatch: &capability.PreDispatchContract{Resource: capability.ResourceReference{Namespace: "travel-" + method, Argument: "destination"}, Freshness: capability.FreshnessPlanEpoch, Unclaimed: capability.UnclaimedDiscardWithDisposition, Privacy: capability.PreDispatchPrivacyExactPartition, Coalescing: capability.PreDispatchCoalescingForbidden, MaxResultBytes: 1 << 20, CostUnits: 1},
	}
}

func callTravelFixtureObserved(ctx context.Context, fixture Fixture, kind string, arguments json.RawMessage, observe TravelCallObserver) (result json.RawMessage, resultErr error) {
	var input struct {
		Destination string `json:"destination"`
		Travellers  int    `json:"travellers"`
	}
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil {
		return nil, errors.New("invalid travel fixture call")
	}
	if observe != nil {
		observe(TravelCallEvent{Phase: "start", API: kind, Destination: input.Destination, LatencyMS: fixture.Workspace.API.APILatencyMS[kind]})
		defer func() {
			outcome := "ok"
			if resultErr != nil {
				outcome = "error"
			}
			observe(TravelCallEvent{Phase: "finish", API: kind, Destination: input.Destination, LatencyMS: fixture.Workspace.API.APILatencyMS[kind], Outcome: outcome})
		}()
	}
	return callTravelFixture(ctx, fixture, kind, arguments)
}

func callTravelFixture(ctx context.Context, fixture Fixture, kind string, arguments json.RawMessage) (json.RawMessage, error) {
	var input struct {
		Destination string `json:"destination"`
		Travellers  int    `json:"travellers"`
	}
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || !validCandidateID(input.Destination) || (kind == "rail" && input.Travellers != 2) {
		return nil, errors.New("invalid travel fixture call")
	}
	latency := fixture.Workspace.API.APILatencyMS[kind]
	timer := time.NewTimer(time.Duration(latency) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
	}
	var value any
	switch kind {
	case "weather":
		value = fixture.Workspace.API.Weather[input.Destination]
	case "rail":
		value = fixture.Workspace.API.Rail[input.Destination]
	case "attractions":
		value = fixture.Workspace.API.Attractions[input.Destination]
	default:
		return nil, errors.New("unknown travel fixture capability")
	}
	return json.Marshal(value)
}

func executableCandidateSource(source, candidateID string) (string, error) {
	trimmed := strings.TrimSpace(source)
	if strings.HasPrefix(trimmed, "import travel;") {
		trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "import travel;"))
	} else {
		lines := strings.Split(trimmed, "\n")
		if len(lines) > 0 && strings.TrimSpace(lines[0]) == "import travel" {
			trimmed = strings.TrimSpace(strings.Join(lines[1:], "\n"))
		}
	}
	if trimmed == "" || strings.Contains(trimmed, "import travel") || !validCandidatePythonSource(trimmed, candidateID) {
		return "", ErrInvalidContract
	}
	return trimmed, nil
}

func parseObservedCandidate(response []byte, candidateID string, fixture Fixture) (json.RawMessage, error) {
	var envelope struct {
		Status string          `json:"status"`
		Result json.RawMessage `json:"result"`
	}
	if json.Unmarshal(response, &envelope) != nil || envelope.Status != "ok" || len(envelope.Result) == 0 || bytes.Equal(envelope.Result, []byte("null")) {
		return nil, errors.New("invalid day-trip Guest response")
	}
	var observed struct {
		CandidateID string           `json:"candidate_id"`
		Weather     WeatherResult    `json:"weather"`
		Rail        RailResult       `json:"rail"`
		Attraction  AttractionResult `json:"attraction"`
		TotalCost   float64          `json:"total_cost_gbp"`
	}
	decoder := json.NewDecoder(bytes.NewReader(envelope.Result))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&observed) != nil || observed.CandidateID != candidateID || observed.Weather != fixture.Workspace.API.Weather[candidateID] || observed.Rail != fixture.Workspace.API.Rail[candidateID] || observed.Attraction != fixture.Workspace.API.Attractions[candidateID] {
		return nil, errors.New("day-trip Guest output drifted from observed API values")
	}
	expectedTotal := observed.Rail.TotalCostGBP + observed.Attraction.EntryCostGBP*float64(fixture.User.Travellers)
	if math.Abs(observed.TotalCost-expectedTotal) > 0.001 {
		return nil, errors.New("day-trip Guest total is not derived from observed prices")
	}
	canonical, err := json.Marshal(observed)
	return canonical, err
}

func writeBranchOutput(manager *workspace.Manager, ref workspace.Ref, candidateID string, body []byte) error {
	lease, err := manager.Acquire(ref, "host-materialize-"+candidateID)
	if err != nil {
		return err
	}
	defer lease.Release()
	file, errno := lease.FS().OpenFile(candidateID+"-plan.json", experimentalsys.O_CREAT|experimentalsys.O_WRONLY|experimentalsys.O_TRUNC, 0o600)
	if errno != 0 {
		return errors.New("open branch output")
	}
	_, writeErrno := file.Write(body)
	closeErrno := file.Close()
	if writeErrno != 0 || closeErrno != 0 {
		return errors.New("write branch output")
	}
	return nil
}

func digestBytes(body []byte) string {
	digest := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func sortedKeys(values map[string]CandidateResponse) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

var _ CandidateExecutor = (*PysolateCandidateExecutor)(nil)
var _ = fmt.Sprintf

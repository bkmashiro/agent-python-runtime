package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/agentfunction"
	"github.com/bkmashiro/agent-python-runtime/runtime/approval"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/receipt"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

const tau2WriteCapability = "tau2.airline.apply_task_11_reference_update"
const tau2WriteStateFile = "task-world.json"

var tau2WriteArguments = json.RawMessage(`{"cabin":"basic_economy","flights":[{"date":"2024-05-19","flight_number":"HAT003"},{"date":"2024-05-20","flight_number":"HAT290"}],"payment_id":"gift_card_1642017","reservation_id":"GV1N64"}`)

type tau2WriteAdapterResponse struct {
	SchemaVersion        string          `json:"schema_version"`
	SourceRevision       string          `json:"source_revision"`
	Domain               string          `json:"domain"`
	TaskID               string          `json:"task_id"`
	Operation            string          `json:"operation"`
	CallID               string          `json:"call_id,omitempty"`
	Tool                 string          `json:"tool,omitempty"`
	Content              string          `json:"content,omitempty"`
	StateSHA256          string          `json:"state_sha256,omitempty"`
	State                json.RawMessage `json:"state,omitempty"`
	CandidateStateSHA256 string          `json:"candidate_state_sha256,omitempty"`
	CandidateState       json.RawMessage `json:"candidate_state,omitempty"`
}

type tau2WriteLane struct {
	Name, Disposition, InitialStateSHA256, FinalStateSHA256, Content string
	PlanIdentity, WorkspaceRef                                       string
	HandlerCalls                                                     uint32
	Request, Payload                                                 []byte
	FinalState                                                       json.RawMessage
	Source, PlanDocument                                             []byte
	WorkspaceEvent                                                   map[string]any
	Receipt                                                          receipt.Receipt
	Approval                                                         []approval.Record
}

func TestTau2AirlineWriteCanaryThroughPrivateWorkspaceAndRealGuest(t *testing.T) {
	python := os.Getenv("PYSOLATE_TAU2_PYTHON")
	sourceRoot := os.Getenv("PYSOLATE_TAU2_SOURCE_ROOT")
	if python == "" || sourceRoot == "" {
		t.Skip("PYSOLATE_TAU2_PYTHON and PYSOLATE_TAU2_SOURCE_ROOT are required")
	}
	wasm, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	initialState, initialSHA := tau2WriteInitialState(t, python, sourceRoot)
	managerRoot := t.TempDir()
	if err := os.Chmod(managerRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	manager, err := workspace.NewManager(managerRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	base, err := manager.Create([]workspace.InitialFile{{Path: tau2WriteStateFile, Data: initialState}}, workspace.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	profile := tau2WriteProfile(t, wasm)
	lanes := []struct {
		name          string
		lease         uint64
		decision      string
		injectFailure bool
	}{
		{name: "accepted", lease: 2000, decision: "approve"},
		{name: "rejected", lease: 2000, decision: "reject"},
		{name: "expired", lease: 20, decision: "expire"},
		{name: "failure", lease: 2000, decision: "approve", injectFailure: true},
	}
	results := make([]tau2WriteLane, 0, len(lanes))
	for _, lane := range lanes {
		result := runTau2WriteLane(t, wasm, profile, manager, base, python, sourceRoot, initialSHA, lane.name, lane.decision, lane.lease, lane.injectFailure)
		results = append(results, result)
	}
	if results[0].HandlerCalls != 1 || results[0].Receipt.Outcome != "ok" || results[0].InitialStateSHA256 == results[0].FinalStateSHA256 || results[0].Disposition != "published" {
		t.Fatalf("accepted lane=%+v", results[0])
	}
	for _, index := range []int{1, 2} {
		lane := results[index]
		if lane.HandlerCalls != 0 || lane.Receipt.Outcome != "denied" || lane.InitialStateSHA256 != lane.FinalStateSHA256 || lane.Disposition != "discarded" {
			t.Fatalf("pre-dispatch lane=%+v", lane)
		}
	}
	failure := results[3]
	if failure.HandlerCalls != 1 || failure.Receipt.Outcome != "error" || failure.InitialStateSHA256 != failure.FinalStateSHA256 || failure.Disposition != "discarded" {
		t.Fatalf("failure lane=%+v", failure)
	}
	if results[0].PlanIdentity != results[1].PlanIdentity {
		t.Fatal("accepted and rejected lanes did not share the same sealed authority")
	}
	if evidenceDir := os.Getenv("PYSOLATE_TAU2_WRITE_EVIDENCE_DIR"); evidenceDir != "" {
		writeTau2WriteEvidence(t, evidenceDir, wasm, results)
	}
}

func tau2WriteInitialState(t *testing.T, python, sourceRoot string) ([]byte, string) {
	t.Helper()
	request := map[string]any{"schema_version": "pysolate.tau2-write-request.v1", "source_revision": tau2CanaryRevision, "domain": "airline", "task_id": "11", "operation": "init"}
	response := callTau2WriteAdapter(t, context.Background(), python, sourceRoot, request, false)
	if response.Operation != "init" || len(response.State) == 0 || tau2Digest(response.State) != response.StateSHA256 {
		t.Fatalf("invalid initial state response identity")
	}
	return append([]byte(nil), response.State...), response.StateSHA256
}

func tau2WriteProfile(t *testing.T, wasm []byte) runtimeconfig.ExecutionProfile {
	t.Helper()
	profile, err := runtimeconfig.NewExecutionProfile("base", []string{"json"})
	if err != nil {
		t.Fatal(err)
	}
	profile, err = profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{
		ProfileID: "base", ArtifactSHA256: tau2Digest(wasm), ManifestSHA256: tau2Digest([]byte("tau2-airline-11-private-write-manifest")),
		ImportRoots: []string{"json"}, QualifiedImportRoots: []string{"json"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return profile
}

func runTau2WriteLane(t *testing.T, wasm []byte, profile runtimeconfig.ExecutionProfile, manager *workspace.Manager, base workspace.Ref, python, sourceRoot, initialSHA, name, decision string, leaseMilliseconds uint64, injectFailure bool) tau2WriteLane {
	t.Helper()
	attempt, err := manager.ForkAttempt(base)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := manager.Acquire(attempt.Ref(), "tau2-airline-11-"+name)
	if err != nil {
		t.Fatal(err)
	}
	root, err := lease.BindMountSource()
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(root, tau2WriteStateFile)
	initialBody, err := os.ReadFile(statePath)
	if err != nil || tau2Digest(initialBody) != initialSHA {
		t.Fatalf("lane initial state mismatch err=%v", err)
	}
	var handlerCalls atomic.Uint32
	plan := tau2WritePlan(t, python, sourceRoot, statePath, leaseMilliseconds, injectFailure, &handlerCalls)
	source := "result = tools.apply_task_11_reference_update()\n"
	planDocument, err := plan.EvidenceDocument()
	if err != nil {
		t.Fatal(err)
	}
	parent := "tau2-airline-11-" + name
	resolver := tau2WriteSourceResolver(t, wasm, profile, plan, source, parent)
	presentation, err := plan.Present(capability.ProgramSurfaceProgrammatic, parent)
	if err != nil {
		t.Fatal(err)
	}
	controller := approval.NewController()
	var broker *capability.Broker
	config := runtimeconfig.DefaultRunConfig()
	config.ExecutionProfile = &profile
	config.ProgramSurface = runtimeconfig.ProgramSurfaceProgrammatic
	config.Mechanisms.ProgrammaticToolCalling = true
	config.Mechanisms.ApprovalSuspension = true
	runner, err := (wazeroengine.Factory{BrokerFactory: func(context.Context) (*capability.Broker, error) {
		created, createErr := capability.NewBroker(capability.Config{
			RunIdentity: parent, Plan: plan, ProgrammaticParentCallID: parent, SourceResolver: resolver,
			ApprovalSuspension: true, ApprovalController: controller,
		})
		broker = created
		return created, createErr
	}}).New(context.Background(), wasm, config)
	if err != nil {
		t.Fatal(err)
	}
	request, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{RunID: parent, Code: source, Inputs: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	type runResult struct {
		payload []byte
		err     error
	}
	finished := make(chan runResult, 1)
	go func() {
		payload, runErr := runner.Run(context.Background(), request, presentation.PythonPrelude)
		finished <- runResult{payload: payload, err: runErr}
	}()
	pending := waitForE2EApproval(t, controller)
	switch decision {
	case "approve":
		if err := controller.Approve(pending.RequestID); err != nil {
			t.Fatal(err)
		}
	case "reject":
		if err := controller.Reject(pending.RequestID); err != nil {
			t.Fatal(err)
		}
	case "expire":
	default:
		t.Fatalf("unknown decision %q", decision)
	}
	run := <-finished
	if run.err != nil {
		t.Fatalf("lane %s guest execution failed: %v", name, run.err)
	}
	if err := runner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	receipts := broker.SnapshotReceipts()
	if len(receipts) != 1 || receipts[0].Source == nil || !receipt.ValidIdentity(receipts[0]) {
		t.Fatalf("lane %s receipts=%+v", name, receipts)
	}
	finalBody, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	result := tau2WriteLane{
		Name: name, InitialStateSHA256: initialSHA, FinalStateSHA256: tau2Digest(finalBody), PlanIdentity: plan.Identity(),
		WorkspaceRef: string(attempt.Ref()), HandlerCalls: handlerCalls.Load(), Request: request, Payload: run.payload,
		FinalState: append(json.RawMessage(nil), finalBody...), Source: []byte(source), PlanDocument: planDocument,
		Receipt: receipts[0], Approval: controller.Snapshot(),
	}
	if name == "accepted" {
		response := decodeRealGuestResponse(t, request, run.payload)
		if err := json.Unmarshal(response.Result, &result.Content); err != nil || strings.TrimSpace(result.Content) == "" {
			t.Fatalf("accepted result unavailable: %v", err)
		}
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	if name == "accepted" {
		publishedRef, err := attempt.Publish()
		if err != nil {
			t.Fatal(err)
		}
		publishedLease, err := manager.Acquire(publishedRef, "tau2-airline-11-verify-published")
		if err != nil {
			t.Fatal(err)
		}
		publishedRoot, err := publishedLease.BindMountSource()
		if err != nil {
			t.Fatal(err)
		}
		publishedState, err := os.ReadFile(filepath.Join(publishedRoot, tau2WriteStateFile))
		if err != nil || tau2Digest(publishedState) != result.FinalStateSHA256 {
			t.Fatalf("published state verification failed err=%v", err)
		}
		if err := publishedLease.Release(); err != nil {
			t.Fatal(err)
		}
		result.Disposition = "published"
		result.WorkspaceEvent = map[string]any{
			"action": "publish", "attempt_ref": string(attempt.Ref()), "result_ref": string(publishedRef),
			"verified": true, "post_state_sha256": result.FinalStateSHA256, "post_state_absent": false,
		}
	} else {
		if err := attempt.Discard(); err != nil {
			t.Fatal(err)
		}
		_, verifyErr := manager.Acquire(attempt.Ref(), "tau2-airline-11-verify-discarded")
		if !errors.Is(verifyErr, workspace.ErrWorkspaceNotFound) {
			t.Fatalf("discarded workspace remained acquirable: %v", verifyErr)
		}
		result.Disposition = "discarded"
		result.WorkspaceEvent = map[string]any{
			"action": "discard", "attempt_ref": string(attempt.Ref()), "result_ref": "",
			"verified": true, "post_state_sha256": "", "post_state_absent": true,
		}
	}
	return result
}

func tau2WritePlan(t *testing.T, python, sourceRoot, statePath string, leaseMilliseconds uint64, injectFailure bool, calls *atomic.Uint32) *capability.Plan {
	t.Helper()
	grantBody := tau2WriteGrantPolicy()
	grant, err := capability.NewGrant(json.RawMessage(grantBody))
	if err != nil {
		t.Fatal(err)
	}
	handlerIdentity := "pysolate.tau2.airline.private-write.handler." + tau2CanaryRevision
	if injectFailure {
		handlerIdentity += ".injected-failure"
	}
	spec := capability.Spec{
		Name: tau2WriteCapability, Version: "pysolate.tau2.airline.private-write.v1", Description: "Exact tau2 airline task 11 private-workspace WRITE adapter.",
		EffectClass: capability.EffectWorkspaceWrite, Playback: capability.PlaybackLiveOnly, HandlerIdentity: handlerIdentity,
		InputSchema:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"content":{"type":"string","minLength":1},"operation_sha256":{"type":"string","pattern":"^sha256:[0-9a-f]{64}$"},"state_sha256":{"type":"string","pattern":"^sha256:[0-9a-f]{64}$"}},"required":["content","operation_sha256","state_sha256"],"additionalProperties":false}`),
		Python:       &capability.PythonProjection{Module: "tools", Method: "apply_task_11_reference_update", Arguments: []string{}, ResultField: "content"},
		Approval:     &capability.ApprovalRequirement{Mode: capability.ApprovalLease, LeaseMilliseconds: leaseMilliseconds},
	}
	registry := capability.NewRegistry()
	if err := registry.Register(spec, grant, tau2WriteHandler(python, sourceRoot, statePath, injectFailure, calls)); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 1})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func tau2WriteGrantPolicy() json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"benchmark":"tau2","domain":"airline","effect":"workspace_write","operation_sha256":%q,"source_revision":"c3398666e6559e3a063da3fc04b5acf7f941464e","task_id":"11","workspace_scope":"attempt_private"}`, tau2Digest(tau2WriteArguments)))
}

func tau2WriteHandler(python, sourceRoot, statePath string, injectFailure bool, calls *atomic.Uint32) capability.Handler {
	return capability.HandlerFunc(func(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
		calls.Add(1)
		if string(raw) != `{}` {
			return nil, fmt.Errorf("unexpected exact reference operation arguments")
		}
		state, err := os.ReadFile(statePath)
		if err != nil {
			return nil, err
		}
		request := map[string]any{
			"schema_version": "pysolate.tau2-write-request.v1", "source_revision": tau2CanaryRevision,
			"domain": "airline", "task_id": "11", "operation": "apply", "call_id": "broker:update_reservation_flights",
			"tool": "update_reservation_flights", "arguments": json.RawMessage(tau2WriteArguments), "state": json.RawMessage(state), "inject_failure": injectFailure,
		}
		response, err := callTau2WriteAdapterRaw(ctx, python, sourceRoot, request)
		if err != nil {
			return nil, err
		}
		if response.Operation != "apply" || response.CallID != "broker:update_reservation_flights" || response.Tool != "update_reservation_flights" || strings.TrimSpace(response.Content) == "" || len(response.CandidateState) == 0 || tau2Digest(response.CandidateState) != response.CandidateStateSHA256 {
			return nil, fmt.Errorf("tau2 WRITE adapter identity mismatch")
		}
		if err := installTau2Candidate(statePath, response.CandidateState); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]string{"content": response.Content, "operation_sha256": tau2Digest(tau2WriteArguments), "state_sha256": response.CandidateStateSHA256})
	})
}

func tau2WriteSourceResolver(t *testing.T, wasm []byte, profile runtimeconfig.ExecutionProfile, plan *capability.Plan, source, parent string) *capability.SourceBindingResolver {
	t.Helper()
	analysisConfig := runtimeconfig.DefaultRunConfig()
	analysisConfig.ExecutionProfile = &profile
	analysisConfig.Mechanisms.SemanticAnalysis = true
	analysisRunner, err := (wazeroengine.Factory{}).New(context.Background(), wasm, analysisConfig)
	if err != nil {
		t.Fatal(err)
	}
	request, err := semantic.NewRequest(source, semantic.Bindings{
		ArtifactSHA256: tau2Digest(wasm), ExecutionProfileSHA256: analysisRunner.Properties().ExecutionProfileBindingSHA256,
		ImportClosureSHA256: agentfunction.ImportClosureIdentity([]string{"json"}, []string{"json"}), CapabilityPlanSHA256: plan.Identity(),
	}, plan)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := semantic.AnalyzeVerified(context.Background(), trustedSemanticRunner(t, analysisRunner), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := analysisRunner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	analysis, err := verified.Analysis()
	if err != nil || len(analysis.CallSites) != 1 || analysis.CallSites[0].Capability != tau2WriteCapability || !analysis.CallSites[0].NecessarilyReached || !analysis.CallSites[0].ArgumentsCanonical {
		t.Fatalf("WRITE analysis=%+v err=%v", analysis, err)
	}
	planned, err := semantic.BuildSourceBoundPlan(verified, plan, semantic.PlannerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := semantic.NewSourceBindingResolver(planned)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resolver.ResolveSource(capability.SourceBindingRequest{
		CallID: parent + ":program:1", ParentCallID: parent, Programmatic: true, Capability: tau2WriteCapability, Arguments: json.RawMessage(`{}`),
	}); !ok {
		t.Fatal("WRITE source resolver miss")
	}
	return resolver
}

func callTau2WriteAdapter(t *testing.T, ctx context.Context, python, sourceRoot string, request any, allowFailure bool) tau2WriteAdapterResponse {
	t.Helper()
	response, err := callTau2WriteAdapterRaw(ctx, python, sourceRoot, request)
	if err != nil && !allowFailure {
		t.Fatal(err)
	}
	return response
}

func callTau2WriteAdapterRaw(ctx context.Context, python, sourceRoot string, request any) (tau2WriteAdapterResponse, error) {
	input, err := json.Marshal(request)
	if err != nil {
		return tau2WriteAdapterResponse{}, err
	}
	script, err := filepath.Abs("../../scripts/tau2-write-adapter.py")
	if err != nil {
		return tau2WriteAdapterResponse{}, err
	}
	command := exec.CommandContext(ctx, python, script, "--source-root", sourceRoot)
	command.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		return tau2WriteAdapterResponse{}, fmt.Errorf("tau2 WRITE adapter failed: %w", err)
	}
	decoder := json.NewDecoder(&stdout)
	decoder.DisallowUnknownFields()
	var response tau2WriteAdapterResponse
	if err := decoder.Decode(&response); err != nil {
		return response, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return response, fmt.Errorf("tau2 WRITE adapter trailing output")
	}
	if response.SchemaVersion != "pysolate.tau2-write-response.v1" || response.SourceRevision != tau2CanaryRevision || response.Domain != "airline" || response.TaskID != "11" {
		return response, fmt.Errorf("tau2 WRITE adapter envelope mismatch")
	}
	return response, nil
}

func installTau2Candidate(statePath string, candidate []byte) error {
	temporary := statePath + ".candidate"
	file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	failed := true
	defer func() {
		_ = file.Close()
		if failed {
			_ = os.Remove(temporary)
		}
	}()
	if _, err := file.Write(candidate); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporary, statePath); err != nil {
		return err
	}
	failed = false
	return nil
}

func writeTau2WriteEvidence(t *testing.T, dir string, wasm []byte, lanes []tau2WriteLane) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	manifestLanes := make([]map[string]any, 0, len(lanes))
	for _, lane := range lanes {
		requestName, responseName := lane.Name+"-request.json", lane.Name+"-response.json"
		sourceName, planName := lane.Name+"-source.py", lane.Name+"-plan.json"
		for name, body := range map[string][]byte{requestName: lane.Request, responseName: lane.Payload, sourceName: lane.Source, planName: lane.PlanDocument} {
			if err := os.WriteFile(filepath.Join(dir, name), body, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		manifestLanes = append(manifestLanes, map[string]any{
			"name": lane.Name, "disposition": lane.Disposition, "initial_state_sha256": lane.InitialStateSHA256, "final_state_sha256": lane.FinalStateSHA256,
			"plan_sha256": lane.PlanIdentity, "workspace_ref": lane.WorkspaceRef, "handler_calls": lane.HandlerCalls,
			"receipt": lane.Receipt, "approval": lane.Approval, "request_sha256": tau2Digest(lane.Request), "guest_response_sha256": tau2Digest(lane.Payload),
			"workspace_event": lane.WorkspaceEvent,
			"raw_bodies":      map[string]string{"request": requestName, "response": responseName, "source": sourceName, "plan": planName},
		})
	}
	if len(lanes) == 0 || lanes[0].Name != "accepted" || len(lanes[0].FinalState) == 0 {
		t.Fatal("accepted final state evidence missing")
	}
	if err := os.WriteFile(filepath.Join(dir, "accepted-final-state.json"), lanes[0].FinalState, 0o600); err != nil {
		t.Fatal(err)
	}
	grantPolicy := tau2WriteGrantPolicy()
	if err := os.WriteFile(filepath.Join(dir, "grant-policy.json"), grantPolicy, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{
		"schema_version": "pysolate.tau2-private-write-evidence.v1", "source": map[string]string{"revision": tau2CanaryRevision, "domain": "airline", "task_id": "11"},
		"artifact_sha256": tau2Digest(wasm), "arguments": json.RawMessage(tau2WriteArguments), "grant_operation_sha256": tau2Digest(tau2WriteArguments), "accepted_tool_content": lanes[0].Content,
		"grant_policy_file": "grant-policy.json", "grant_policy_sha256": tau2Digest(grantPolicy),
		"accepted_final_state_file": "accepted-final-state.json", "accepted_final_state_sha256": tau2Digest(lanes[0].FinalState), "lanes": manifestLanes,
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "evidence-manifest.json"), append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

package e2e_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/agentfunction"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/receipt"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

type tau2SourceBoundRun struct {
	Name    string
	Request []byte
	Payload []byte
	Content string
	Receipt receipt.Receipt
}

func TestTau2AirlineSourceBoundFreshTurnCanaryThroughRealGuest(t *testing.T) {
	python := os.Getenv("PYSOLATE_TAU2_PYTHON")
	sourceRoot := os.Getenv("PYSOLATE_TAU2_SOURCE_ROOT")
	if python == "" || sourceRoot == "" {
		t.Skip("PYSOLATE_TAU2_PYTHON and PYSOLATE_TAU2_SOURCE_ROOT are required")
	}
	wasm, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	plan := tau2ReadPlan(t, python, sourceRoot)
	profile := tau2CanaryProfile(t, wasm)
	steps := []struct {
		name, capability, arguments, source string
	}{
		{"reservation", "tau2.airline.get_reservation_details", `{"reservation_id":"JMO1MG"}`, "result = tools.get_reservation_details('JMO1MG')\n"},
		{"user", "tau2.airline.get_user_details", `{"user_id":"anya_garcia_5901"}`, "result = tools.get_user_details('anya_garcia_5901')\n"},
	}
	runs := make([]tau2SourceBoundRun, 0, len(steps))
	for _, step := range steps {
		runs = append(runs, runTau2SourceBoundTurn(t, wasm, profile, plan, step.name, step.capability, step.arguments, step.source))
	}
	pureRequest, purePayload, result := runTau2PureAggregation(t, wasm, profile, runs[0].Content, runs[1].Content)
	if result.Answer != "4" || result.Cabin != "economy" || result.Membership != "silver" || result.PassengerCount != 2 {
		t.Fatalf("unexpected body-safe result metadata: %+v", result)
	}
	if evidenceDir := os.Getenv("PYSOLATE_TAU2_EVIDENCE_DIR"); evidenceDir != "" {
		writeTau2SourceBoundEvidence(t, evidenceDir, wasm, plan, runs, pureRequest, purePayload, result)
	}
}

func tau2CanaryProfile(t *testing.T, wasm []byte) runtimeconfig.ExecutionProfile {
	t.Helper()
	profile, err := runtimeconfig.NewExecutionProfile("base", []string{"json"})
	if err != nil {
		t.Fatal(err)
	}
	profile, err = profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{
		ProfileID: "base", ArtifactSHA256: tau2Digest(wasm), ManifestSHA256: tau2Digest([]byte("tau2-airline-3-manifest")),
		ImportRoots: []string{"json"}, QualifiedImportRoots: []string{"json"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return profile
}

func runTau2SourceBoundTurn(t *testing.T, wasm []byte, profile runtimeconfig.ExecutionProfile, plan *capability.Plan, name, capabilityName, arguments, source string) tau2SourceBoundRun {
	t.Helper()
	analysisConfig := runtimeconfig.DefaultRunConfig()
	analysisConfig.ExecutionProfile = &profile
	analysisConfig.Mechanisms.SemanticAnalysis = true
	analysisRunner, err := (wazeroengine.Factory{}).New(context.Background(), wasm, analysisConfig)
	if err != nil {
		t.Fatal(err)
	}
	analysisRequest, err := semantic.NewRequest(source, semantic.Bindings{
		ArtifactSHA256: tau2Digest(wasm), ExecutionProfileSHA256: analysisRunner.Properties().ExecutionProfileBindingSHA256,
		ImportClosureSHA256: agentfunction.ImportClosureIdentity([]string{"json"}, []string{"json"}), CapabilityPlanSHA256: plan.Identity(),
	}, plan)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := semantic.AnalyzeVerified(context.Background(), trustedSemanticRunner(t, analysisRunner), analysisRequest)
	if err != nil {
		t.Fatal(err)
	}
	if err := analysisRunner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	analysis, err := verified.Analysis()
	if err != nil || len(analysis.CallSites) != 1 || analysis.CallSites[0].Capability != capabilityName || !analysis.CallSites[0].NecessarilyReached || !analysis.CallSites[0].ArgumentsCanonical {
		t.Fatalf("analysis=%+v err=%v", analysis, err)
	}
	planned, err := semantic.BuildSourceBoundPlan(verified, plan, semantic.PlannerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := semantic.NewSourceBindingResolver(planned)
	if err != nil {
		t.Fatal(err)
	}
	parent := "tau2-airline-3-" + name
	if _, ok := resolver.ResolveSource(capability.SourceBindingRequest{
		CallID: parent + ":program:1", ParentCallID: parent, Programmatic: true,
		Capability: capabilityName, Arguments: json.RawMessage(arguments),
	}); !ok {
		t.Fatalf("source resolver miss capability=%s arguments=%s", capabilityName, arguments)
	}
	presentation, err := plan.Present(capability.ProgramSurfaceProgrammatic, parent)
	if err != nil {
		t.Fatal(err)
	}
	var broker *capability.Broker
	config := runtimeconfig.DefaultRunConfig()
	config.ExecutionProfile = &profile
	config.ProgramSurface = runtimeconfig.ProgramSurfaceProgrammatic
	config.Mechanisms.ProgrammaticToolCalling = true
	runner, err := (wazeroengine.Factory{BrokerFactory: func(context.Context) (*capability.Broker, error) {
		created, createErr := capability.NewBroker(capability.Config{
			RunIdentity: parent, Plan: plan, ProgrammaticParentCallID: parent, SourceResolver: resolver,
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
	payload, err := runner.Run(context.Background(), request, presentation.PythonPrelude)
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	response := decodeRealGuestResponse(t, request, payload)
	var content string
	if err := json.Unmarshal(response.Result, &content); err != nil || content == "" {
		t.Fatalf("content unavailable err=%v", err)
	}
	receipts := broker.SnapshotReceipts()
	if len(receipts) != 1 || broker.CallCount() != 1 || receipts[0].Capability != capabilityName || receipts[0].Outcome != "ok" || receipts[0].Source == nil || !receipt.ValidIdentity(receipts[0]) {
		t.Fatalf("receipts=%+v calls=%d", receipts, broker.CallCount())
	}
	return tau2SourceBoundRun{Name: name, Request: request, Payload: payload, Content: content, Receipt: receipts[0]}
}

func runTau2PureAggregation(t *testing.T, wasm []byte, profile runtimeconfig.ExecutionProfile, reservation, user string) ([]byte, []byte, struct {
	Answer         string `json:"answer"`
	Cabin          string `json:"cabin"`
	Membership     string `json:"membership"`
	PassengerCount int    `json:"passenger_count"`
}) {
	t.Helper()
	inputs, err := json.Marshal(map[string]string{"reservation": reservation, "user": user})
	if err != nil {
		t.Fatal(err)
	}
	source := "import json\nreservation = json.loads(inputs['reservation'])\nuser = json.loads(inputs['user'])\npassenger_count = len(reservation['passengers'])\nfree_per_passenger = 2 if user['membership'] == 'silver' and reservation['cabin'] == 'economy' else 0\nresult = {'answer': str(passenger_count * free_per_passenger), 'cabin': reservation['cabin'], 'membership': user['membership'], 'passenger_count': passenger_count}\n"
	request, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{RunID: "tau2-airline-3-aggregate", Code: source, Inputs: inputs})
	if err != nil {
		t.Fatal(err)
	}
	config := runtimeconfig.DefaultRunConfig()
	config.ExecutionProfile = &profile
	runner, err := (wazeroengine.Factory{}).New(context.Background(), wasm, config)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := runner.Run(context.Background(), request, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	response := decodeRealGuestResponse(t, request, payload)
	var result struct {
		Answer         string `json:"answer"`
		Cabin          string `json:"cabin"`
		Membership     string `json:"membership"`
		PassengerCount int    `json:"passenger_count"`
	}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatal(err)
	}
	return request, payload, result
}

func writeTau2SourceBoundEvidence(t *testing.T, dir string, wasm []byte, plan *capability.Plan, runs []tau2SourceBoundRun, pureRequest, purePayload []byte, result any) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	receipts := []receipt.Receipt{runs[0].Receipt, runs[1].Receipt}
	requests := append(append([]byte{}, runs[0].Request...), runs[1].Request...)
	requests = append(requests, pureRequest...)
	responses := append(append([]byte{}, runs[0].Payload...), runs[1].Payload...)
	responses = append(responses, purePayload...)
	manifest := map[string]any{
		"schema_version":  "pysolate.tau2-canary-private-evidence.v1",
		"source":          map[string]any{"revision": tau2CanaryRevision, "domain": "airline", "task_id": "3"},
		"artifact_sha256": tau2Digest(wasm), "request_sha256": tau2Digest(requests), "guest_response_sha256": tau2Digest(responses),
		"capability_plan_sha256": plan.Identity(), "broker_call_count": 2, "receipts": receipts,
		"source_occurrence_claim": "recorded", "fresh_runs": 3, "tool_runs": 2, "result": result,
		"raw_bodies": map[string]any{"tool_requests": []string{"source-bound-reservation-request.json", "source-bound-user-request.json"}, "tool_responses": []string{"source-bound-reservation-response.json", "source-bound-user-response.json"}, "aggregate_request": "source-bound-aggregate-request.json", "aggregate_response": "source-bound-aggregate-response.json"},
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded, '\n')
	files := map[string][]byte{
		"source-bound-reservation-request.json": runs[0].Request, "source-bound-reservation-response.json": runs[0].Payload,
		"source-bound-user-request.json": runs[1].Request, "source-bound-user-response.json": runs[1].Payload,
		"source-bound-aggregate-request.json": pureRequest, "source-bound-aggregate-response.json": purePayload,
		"evidence-manifest.json": encoded,
	}
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

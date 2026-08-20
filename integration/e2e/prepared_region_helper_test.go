package e2e_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/preparedregion"
)

func TestExactGuestPreparedRegionHelperClaimsHostTableWithoutBrokerOrWorkspace(t *testing.T) {
	artifact, profile := loadPreparedRegionArtifact(t)
	binding := preparedregion.PreparedRegionBinding{
		SourceSHA256: digestA, ASTSHA256: digestA, AnalysisSHA256: digestA, RegionID: digestA,
		RegionSpan:         preparedregion.SourceSpan{StartLine: 1, StartColumn: 0, EndLine: 1, EndColumn: 9},
		RegionSourceSHA256: digestA, LiveInsSHA256: digestA, EnvironmentSHA256: digestA,
		ExecutionProfileSHA256: digestA, ImportClosureSHA256: digestA, CapabilityPlanSHA256: digestA,
		PassConfigSHA256: digestA, Codec: preparedregion.PreparedRegionCodecJSONScalarV1, OutputName: "answer",
	}
	_, decision, err := preparedregion.SealPreparedRegionDecision(binding)
	if err != nil {
		t.Fatal(err)
	}
	_, capsule, err := preparedregion.SealPreparedRegionCapsule(decision.IdentitySHA256, json.RawMessage(`42`))
	if err != nil {
		t.Fatal(err)
	}
	table, err := preparedregion.NewPreparedRegionTable([]preparedregion.PreparedRegionEntry{{Decision: decision, Capsule: capsule}})
	if err != nil {
		t.Fatal(err)
	}
	config := runtimeconfig.DefaultRunConfig()
	config.Mechanisms.SemanticAnalysis = true
	config.ExecutionProfile = &profile
	response := runPreparedRegionExact(t, artifact, config, table, "prepared-region-positive", fmt.Sprintf("result = __pysolate_materialize_value__(%q)", decision.IdentitySHA256))
	var envelope struct {
		Status string          `json:"status"`
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(response, &envelope); err != nil || envelope.Status != "ok" || string(envelope.Result) != "42" || string(envelope.Error) != "null" {
		t.Fatalf("response=%s err=%v", response, err)
	}
	evidence := table.Evidence()
	if evidence.Consumed != 1 || evidence.Claims != 1 || evidence.RejectedClaims != 0 || evidence.Ready != 0 || evidence.Discarded != 0 {
		t.Fatalf("evidence=%+v", evidence)
	}

	secondTable, err := preparedregion.NewPreparedRegionTable([]preparedregion.PreparedRegionEntry{{Decision: decision, Capsule: capsule}})
	if err != nil {
		t.Fatal(err)
	}
	response = runPreparedRegionExact(t, artifact, config, secondTable, "prepared-region-second-claim", fmt.Sprintf("a = __pysolate_materialize_value__(%[1]q)\nresult = a + __pysolate_materialize_value__(%[1]q)", decision.IdentitySHA256))
	if err := json.Unmarshal(response, &envelope); err != nil || envelope.Status != "error" {
		t.Fatalf("second response=%s err=%v", response, err)
	}
	evidence = secondTable.Evidence()
	if evidence.Consumed != 1 || evidence.Claims != 1 || evidence.RejectedClaims != 1 {
		t.Fatalf("second evidence=%+v", evidence)
	}

	missingTable, err := preparedregion.NewPreparedRegionTable([]preparedregion.PreparedRegionEntry{{Decision: decision, Capsule: capsule}})
	if err != nil {
		t.Fatal(err)
	}
	missingDecision := "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	response = runPreparedRegionExact(t, artifact, config, missingTable, "prepared-region-missing", fmt.Sprintf("result = __pysolate_materialize_value__(%q)", missingDecision))
	if err := json.Unmarshal(response, &envelope); err != nil || envelope.Status != "error" {
		t.Fatalf("missing response=%s err=%v", response, err)
	}
	evidence = missingTable.Evidence()
	if evidence.Consumed != 0 || evidence.RejectedClaims != 1 || evidence.Discarded != 1 {
		t.Fatalf("missing evidence=%+v", evidence)
	}
}

func TestExactGuestEmitsCanonicalPreparedRegionPatchBindingInPrivateSession(t *testing.T) {
	artifact, profile := loadPreparedRegionArtifact(t)
	config := runtimeconfig.DefaultRunConfig()
	config.Mechanisms.SemanticAnalysis = true
	config.ExecutionProfile = &profile
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	analyzer, err := wazeroengine.New(ctx, artifact, config)
	if err != nil {
		t.Fatal(err)
	}
	defer analyzer.Close(context.Background())
	if properties := analyzer.Properties(); properties.CapabilityBrokerAvailable || properties.WorkspaceMounted {
		t.Fatalf("patch emitter gained authority: %+v", properties)
	}
	source := "seed = 40\nvalue = seed + 2\nresult = value\n"
	span := preparedregion.SourceSpan{StartLine: 2, StartColumn: 0, EndLine: 2, EndColumn: 16}
	sourceSHA := preparedRegionDigest([]byte(source))
	regionDescriptor := fmt.Sprintf("pysolate.semantic-candidate-region.v0\x00%s\x001\x002:0:2:16", sourceSHA)
	binding := preparedregion.PreparedRegionBinding{
		SourceSHA256: sourceSHA, ASTSHA256: digestA, AnalysisSHA256: digestA,
		RegionID: preparedRegionDigest([]byte(regionDescriptor)), RegionSpan: span,
		RegionSourceSHA256: preparedRegionDigest([]byte("value = seed + 2")), LiveInsSHA256: digestA,
		EnvironmentSHA256: digestA, ExecutionProfileSHA256: digestA, ImportClosureSHA256: digestA,
		CapabilityPlanSHA256: digestA, PassConfigSHA256: digestA,
		Codec: preparedregion.PreparedRegionCodecJSONScalarV1, OutputName: "value",
	}
	decisionRaw, decision, err := preparedregion.SealPreparedRegionDecision(binding)
	if err != nil {
		t.Fatal(err)
	}
	request, err := json.Marshal(map[string]string{"decision": string(decisionRaw), "final_source": source})
	if err != nil {
		t.Fatal(err)
	}
	session, err := analyzer.NewSemanticAnalysisSession(ctx, wazeroengine.SemanticAnalysisSessionLimits{MaxRequests: 1, MaxCumulativeRequestBytes: uint64(len(request)), MaxDuration: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := session.EmitPreparedRegionPatch(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	emitted, err := preparedregion.DecodePreparedRegionPatchBinding(payload)
	if err != nil {
		t.Fatalf("binding=%s err=%v", payload, err)
	}
	if emitted.DecisionSHA256 != decision.IdentitySHA256 || emitted.FinalSourceSHA256 != sourceSHA || emitted.RegionID != decision.RegionID || emitted.RegionSpan != span || emitted.OutputName != "value" || emitted.FinalASTSHA256 == emitted.DerivedASTSHA256 {
		t.Fatalf("emitted=%+v", emitted)
	}
	_, patch, err := preparedregion.SealPreparedRegionPatch(emitted)
	if err != nil || patch.ValidateDecision(decision) != nil {
		t.Fatalf("patch=%+v err=%v", patch, err)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestExactGuestScratchExecutesOneScalarRegionAndPublishesBoundCapsule(t *testing.T) {
	artifact, profile := loadPreparedRegionArtifact(t)
	config := runtimeconfig.DefaultRunConfig()
	config.Mechanisms.SemanticAnalysis = true
	config.ExecutionProfile = &profile
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	engine, err := wazeroengine.New(ctx, artifact, config)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close(context.Background())
	if properties := engine.Properties(); properties.CapabilityBrokerAvailable || properties.WorkspaceMounted {
		t.Fatalf("scratch engine gained authority: %+v", properties)
	}

	source := "seed = 40\nvalue = seed + 2\nresult = value\n"
	liveInsRaw, liveInsSHA, err := preparedregion.SealPreparedRegionLiveIns(map[string]json.RawMessage{"seed": json.RawMessage(`40`)})
	if err != nil {
		t.Fatal(err)
	}
	span := preparedregion.SourceSpan{StartLine: 2, StartColumn: 0, EndLine: 2, EndColumn: 16}
	sourceSHA := preparedRegionDigest([]byte(source))
	regionDescriptor := fmt.Sprintf("pysolate.semantic-candidate-region.v0\x00%s\x001\x002:0:2:16", sourceSHA)
	decisionRaw, decision, err := preparedregion.SealPreparedRegionDecision(preparedregion.PreparedRegionBinding{
		SourceSHA256: sourceSHA, ASTSHA256: digestA, AnalysisSHA256: digestA,
		RegionID: preparedRegionDigest([]byte(regionDescriptor)), RegionSpan: span,
		RegionSourceSHA256: preparedRegionDigest([]byte("value = seed + 2")), LiveInsSHA256: liveInsSHA,
		EnvironmentSHA256: digestA, ExecutionProfileSHA256: digestA, ImportClosureSHA256: digestA,
		CapabilityPlanSHA256: digestA, PassConfigSHA256: digestA,
		Codec: preparedregion.PreparedRegionCodecJSONScalarV1, OutputName: "value",
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := json.Marshal(map[string]string{"decision": string(decisionRaw), "final_source": source, "live_ins": string(liveInsRaw)})
	if err != nil {
		t.Fatal(err)
	}
	result, evidence, err := engine.ExecutePreparedRegionScratch(ctx, request, decision)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != preparedregion.PreparedRegionScratchReady || string(result.Payload) != "42" || !evidence.FreshModule || evidence.ModuleInstantiations != 1 || evidence.BrokerAvailable || evidence.WorkspaceMounted || evidence.TerminalStatus != preparedregion.PreparedRegionScratchReady {
		t.Fatalf("result=%+v evidence=%+v", result, evidence)
	}
	_, capsule, err := preparedregion.PublishPreparedRegionScratchResult(decision, result)
	if err != nil {
		t.Fatal(err)
	}
	table, err := preparedregion.NewPreparedRegionTable([]preparedregion.PreparedRegionEntry{{Decision: decision, Capsule: capsule}})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := table.Claim(decision.IdentitySHA256)
	if err != nil || string(payload) != "42" {
		t.Fatalf("payload=%s err=%v", payload, err)
	}

	preparedConfig := config
	preparedConfig.Mechanisms.PreparedRuntime = true
	preparedConfig.Mechanisms.MemoryCOW = runtime.GOOS == "linux"
	preparedEngine, err := wazeroengine.New(ctx, artifact, preparedConfig)
	if err != nil {
		t.Fatal(err)
	}
	capacity, provisionEvidence, err := preparedEngine.PreparePreparedRegionScratch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !provisionEvidence.NeverServed || provisionEvidence.RuntimeInitCalls != 1 || provisionEvidence.BrokerAvailable || provisionEvidence.WorkspaceMounted {
		t.Fatalf("provision evidence=%+v", provisionEvidence)
	}
	if runtime.GOOS == "linux" && !provisionEvidence.COWHit {
		t.Fatalf("Linux prepared scratch did not use private COW: %+v", provisionEvidence)
	}
	preparedResult, preparedEvidence, err := capacity.Execute(ctx, request, decision)
	if err != nil {
		t.Fatal(err)
	}
	if preparedResult.Status != preparedregion.PreparedRegionScratchReady || string(preparedResult.Payload) != "42" || !preparedEvidence.FreshModule || !preparedEvidence.PreparedCapacity || preparedEvidence.ModuleInstantiations != 0 || preparedEvidence.RuntimeInitCalls != 0 {
		t.Fatalf("prepared result=%+v evidence=%+v", preparedResult, preparedEvidence)
	}
	if _, _, err := capacity.Execute(ctx, request, decision); !errors.Is(err, wazeroengine.ErrPreparedRegionScratchCapacityConsumed) {
		t.Fatalf("second execute err=%v", err)
	}
	if lifecycle := capacity.Evidence(); lifecycle.Ready != 0 || lifecycle.Claims != 1 || lifecycle.Consumed != 1 || lifecycle.RejectedClaims != 1 || lifecycle.Discarded != 0 {
		t.Fatalf("prepared capacity lifecycle=%+v", lifecycle)
	}
	if err := capacity.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := preparedEngine.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	discardEngine, err := wazeroengine.New(ctx, artifact, preparedConfig)
	if err != nil {
		t.Fatal(err)
	}
	discardCapacity, _, err := discardEngine.PreparePreparedRegionScratch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if lifecycle := discardCapacity.Evidence(); lifecycle.Ready != 1 || lifecycle.Claims != 0 || lifecycle.Discarded != 0 {
		t.Fatalf("ready discard lifecycle=%+v", lifecycle)
	}
	if err := discardCapacity.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if lifecycle := discardCapacity.Evidence(); lifecycle.Ready != 0 || lifecycle.Claims != 0 || lifecycle.Consumed != 0 || lifecycle.Discarded != 1 {
		t.Fatalf("closed discard lifecycle=%+v", lifecycle)
	}
	if _, _, err := discardCapacity.Execute(ctx, request, decision); !errors.Is(err, wazeroengine.ErrPreparedRegionScratchCapacityConsumed) {
		t.Fatalf("discarded execute err=%v", err)
	}
	if err := discardEngine.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	wrongLiveIns, _, err := preparedregion.SealPreparedRegionLiveIns(map[string]json.RawMessage{"seed": json.RawMessage(`41`)})
	if err != nil {
		t.Fatal(err)
	}
	wrongRequest, err := json.Marshal(map[string]string{"decision": string(decisionRaw), "final_source": source, "live_ins": string(wrongLiveIns)})
	if err != nil {
		t.Fatal(err)
	}
	if _, driftEvidence, err := engine.ExecutePreparedRegionScratch(ctx, wrongRequest, decision); err == nil || driftEvidence.ModuleInstantiations != 1 || driftEvidence.TerminalStatus != "" {
		t.Fatalf("live-in drift evidence=%+v err=%v", driftEvidence, err)
	}
}

func TestExactGuestPreparedRegionSelectionCommitsDerivedProgramBeforeFreshExecution(t *testing.T) {
	artifact, profile := loadPreparedRegionArtifact(t)
	config := runtimeconfig.DefaultRunConfig()
	config.Mechanisms.SemanticAnalysis = true
	config.ExecutionProfile = &profile
	profileSHA256, err := runtimeconfig.ExecutionProfileBindingSHA256(config)
	if err != nil {
		t.Fatal(err)
	}
	source := "seed = 40\nvalue = seed + 2\nresult = value\n"
	liveInsRaw, liveInsSHA, err := preparedregion.SealPreparedRegionLiveIns(map[string]json.RawMessage{"seed": json.RawMessage(`40`)})
	if err != nil {
		t.Fatal(err)
	}
	span := preparedregion.SourceSpan{StartLine: 2, StartColumn: 0, EndLine: 2, EndColumn: 16}
	sourceSHA := preparedRegionDigest([]byte(source))
	regionDescriptor := fmt.Sprintf("pysolate.semantic-candidate-region.v0\x00%s\x001\x002:0:2:16", sourceSHA)
	decisionRaw, decision, err := preparedregion.SealPreparedRegionDecision(preparedregion.PreparedRegionBinding{
		SourceSHA256: sourceSHA, ASTSHA256: digestA, AnalysisSHA256: digestA,
		RegionID: preparedRegionDigest([]byte(regionDescriptor)), RegionSpan: span,
		RegionSourceSHA256: preparedRegionDigest([]byte("value = seed + 2")), LiveInsSHA256: liveInsSHA,
		EnvironmentSHA256: digestA, ExecutionProfileSHA256: profileSHA256, ImportClosureSHA256: digestA,
		CapabilityPlanSHA256: digestA, PassConfigSHA256: digestA,
		Codec: preparedregion.PreparedRegionCodecJSONScalarV1, OutputName: "value",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	qualificationEngine, err := wazeroengine.New(ctx, artifact, config)
	if err != nil {
		t.Fatal(err)
	}
	scratchRequest, err := json.Marshal(map[string]string{"decision": string(decisionRaw), "final_source": source, "live_ins": string(liveInsRaw)})
	if err != nil {
		t.Fatal(err)
	}
	scratchResult, _, err := qualificationEngine.ExecutePreparedRegionScratch(ctx, scratchRequest, decision)
	if err != nil {
		t.Fatal(err)
	}
	_, capsule, err := preparedregion.PublishPreparedRegionScratchResult(decision, scratchResult)
	if err != nil {
		t.Fatal(err)
	}
	session, err := qualificationEngine.NewSemanticAnalysisSession(ctx, wazeroengine.SemanticAnalysisSessionLimits{MaxRequests: 1, MaxCumulativeRequestBytes: 16 * 1024, MaxDuration: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	emitRequest, err := json.Marshal(map[string]string{"decision": string(decisionRaw), "final_source": source})
	if err != nil {
		t.Fatal(err)
	}
	bindingRaw, err := session.EmitPreparedRegionPatch(ctx, emitRequest)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := preparedregion.DecodePreparedRegionPatchBinding(bindingRaw)
	if err != nil {
		t.Fatal(err)
	}
	_, patch, err := preparedregion.SealPreparedRegionPatch(binding)
	if err != nil {
		t.Fatal(err)
	}
	_, selection, err := preparedregion.SealPreparedRegionExecutionSelection(decision, capsule, patch)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := qualificationEngine.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	runRequest, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{RunID: "prepared-region-derived-selection", Code: source, Inputs: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	originalConfig := config
	originalConfig.Mechanisms.PreparedRuntime = true
	originalConfig.Mechanisms.MemoryCOW = runtime.GOOS == "linux"
	baselineEngine, err := wazeroengine.New(ctx, artifact, originalConfig)
	if err != nil {
		t.Fatal(err)
	}
	originalCapacity, originalProvision, err := baselineEngine.PreparePreparedRegionFinal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !originalProvision.NeverServed || (runtime.GOOS == "linux" && !originalProvision.COWHit) {
		t.Fatalf("original final capacity not treatment-equivalent: %+v", originalProvision)
	}
	baseline, originalExecution, err := originalCapacity.ExecuteOriginal(ctx, runRequest)
	if err != nil {
		t.Fatal(err)
	}
	if !originalExecution.PreparedCapacity || originalExecution.FormalGuestExecutions != 1 || originalExecution.SourceValidations != 1 || originalExecution.ModuleInstantiations != 0 || originalExecution.RuntimeInitCalls != 0 {
		t.Fatalf("unexpected original execution lifecycle: %+v", originalExecution)
	}
	if response, _, err := originalCapacity.ExecuteOriginal(ctx, runRequest); response != nil || !errors.Is(err, wazeroengine.ErrPreparedRegionDerivedCapacityConsumed) {
		t.Fatalf("second original execution response=%s err=%v", response, err)
	}
	if err := baselineEngine.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	table, err := preparedregion.NewPreparedRegionTable(nil)
	if err != nil {
		t.Fatal(err)
	}
	derivedConfig := config
	derivedConfig.Mechanisms.PreparedRuntime = true
	derivedConfig.Mechanisms.MemoryCOW = runtime.GOOS == "linux"
	runner, err := (wazeroengine.Factory{PreparedRegions: table}).New(ctx, artifact, derivedConfig)
	if err != nil {
		t.Fatal(err)
	}
	derivedEngine, ok := runner.(*wazeroengine.Engine)
	if !ok {
		t.Fatalf("unexpected runner type %T", runner)
	}
	finalCapacity, provisionEvidence, err := derivedEngine.PreparePreparedRegionFinal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !provisionEvidence.NeverServed || provisionEvidence.ModuleInstantiations == 0 || provisionEvidence.RuntimeInitCalls == 0 {
		t.Fatalf("final capacity was not initialized during provisioning: %+v", provisionEvidence)
	}
	if runtime.GOOS == "linux" {
		if !provisionEvidence.COWHit {
			t.Fatalf("final capacity did not select private COW: %+v", provisionEvidence)
		}
	} else if !provisionEvidence.PreparedHit {
		t.Fatalf("final prepared capacity not ready: %+v", provisionEvidence)
	}
	if evidence := table.Evidence(); evidence.Ready != 0 || evidence.Unready != 0 {
		t.Fatalf("empty table changed during final capacity provisioning: %+v", evidence)
	}
	if err := table.Publish(decision, capsule); err != nil {
		t.Fatal(err)
	}
	compileEvidence, err := finalCapacity.Compile(ctx, runRequest, selection, decision, capsule, patch)
	if err != nil {
		t.Fatal(err)
	}
	if !compileEvidence.PreparedCapacity || compileEvidence.ModuleInstantiations != 0 || compileEvidence.RuntimeInitCalls != 0 {
		t.Fatalf("derived compile/load did not consume prepared capacity: %+v", compileEvidence)
	}
	if evidence := table.Evidence(); evidence.Ready != 1 || evidence.Claims != 0 || evidence.Consumed != 0 {
		t.Fatalf("compile/load claimed capsule before formal execution: %+v", evidence)
	}
	derived, executionEvidence, err := finalCapacity.Execute(ctx, runRequest)
	if err != nil {
		t.Fatal(err)
	}
	if !executionEvidence.PreparedCapacity || executionEvidence.ModuleInstantiations != 0 || executionEvidence.RuntimeInitCalls != 0 || executionEvidence.FormalGuestExecutions != 1 {
		t.Fatalf("unexpected derived execution lifecycle: %+v", executionEvidence)
	}
	var baselineEnvelope, derivedEnvelope struct {
		Status string          `json:"status"`
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
		Logs   []string        `json:"logs"`
	}
	if json.Unmarshal(baseline, &baselineEnvelope) != nil || json.Unmarshal(derived, &derivedEnvelope) != nil ||
		baselineEnvelope.Status != "ok" || derivedEnvelope.Status != baselineEnvelope.Status ||
		string(derivedEnvelope.Result) != string(baselineEnvelope.Result) || string(derivedEnvelope.Result) != "42" ||
		string(derivedEnvelope.Error) != string(baselineEnvelope.Error) || len(derivedEnvelope.Logs) != len(baselineEnvelope.Logs) {
		t.Fatalf("baseline=%s derived=%s", baseline, derived)
	}
	if evidence := table.Evidence(); evidence.Consumed != 1 || evidence.Claims != 1 || evidence.RejectedClaims != 0 {
		t.Fatalf("table evidence=%+v", evidence)
	}
	if response, _, err := finalCapacity.Execute(ctx, runRequest); response != nil || !errors.Is(err, wazeroengine.ErrPreparedRegionDerivedCapacityConsumed) {
		t.Fatalf("second derived execution response=%s err=%v", response, err)
	}
	if err := derivedEngine.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	driftTable, err := preparedregion.NewPreparedRegionTable([]preparedregion.PreparedRegionEntry{{Decision: decision, Capsule: capsule}})
	if err != nil {
		t.Fatal(err)
	}
	driftConfig := config
	driftConfig.Mechanisms.PreparedRuntime = true
	driftConfig.Mechanisms.MemoryCOW = runtime.GOOS == "linux"
	driftRunner, err := (wazeroengine.Factory{PreparedRegions: driftTable}).New(ctx, artifact, driftConfig)
	if err != nil {
		t.Fatal(err)
	}
	driftEngine := driftRunner.(*wazeroengine.Engine)
	driftCapacity, _, err := driftEngine.PreparePreparedRegionFinal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	driftRequest, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{RunID: "prepared-region-invalid-suffix", Code: source + "suffix = 1\n", Inputs: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if compileEvidence, err := driftCapacity.Compile(ctx, driftRequest, selection, decision, capsule, patch); err == nil || compileEvidence.SelectionCompiles != 0 {
		t.Fatalf("invalid suffix compile evidence=%+v err=%v", compileEvidence, err)
	}
	if response, _, err := driftCapacity.Execute(ctx, driftRequest); response != nil || !errors.Is(err, wazeroengine.ErrPreparedRegionDerivedCapacityConsumed) {
		t.Fatalf("invalid suffix execution response=%s err=%v", response, err)
	}
	if evidence := driftTable.Evidence(); evidence.Ready != 1 || evidence.Claims != 0 || evidence.Consumed != 0 {
		t.Fatalf("invalid suffix consumed region: %+v", evidence)
	}
	if err := driftEngine.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

type preparedDerivedFixture struct {
	decision  preparedregion.PreparedRegionDecision
	capsule   preparedregion.PreparedRegionCapsule
	patch     preparedregion.PreparedRegionPatch
	selection preparedregion.PreparedRegionExecutionSelection
}

func qualifyPreparedDerivedFixture(t *testing.T, ctx context.Context, artifact []byte, profile runtimeconfig.ExecutionProfile, source string, statementIndex int, span preparedregion.SourceSpan, regionSource string, liveIns map[string]json.RawMessage) preparedDerivedFixture {
	t.Helper()
	config := runtimeconfig.DefaultRunConfig()
	config.Mechanisms.SemanticAnalysis = true
	config.ExecutionProfile = &profile
	profileSHA256, err := runtimeconfig.ExecutionProfileBindingSHA256(config)
	if err != nil {
		t.Fatal(err)
	}
	liveInsRaw, liveInsSHA, err := preparedregion.SealPreparedRegionLiveIns(liveIns)
	if err != nil {
		t.Fatal(err)
	}
	sourceSHA := preparedRegionDigest([]byte(source))
	regionDescriptor := fmt.Sprintf("pysolate.semantic-candidate-region.v0\x00%s\x00%d\x00%d:%d:%d:%d", sourceSHA, statementIndex, span.StartLine, span.StartColumn, span.EndLine, span.EndColumn)
	decisionRaw, decision, err := preparedregion.SealPreparedRegionDecision(preparedregion.PreparedRegionBinding{
		SourceSHA256: sourceSHA, ASTSHA256: digestA, AnalysisSHA256: digestA,
		RegionID: preparedRegionDigest([]byte(regionDescriptor)), RegionSpan: span,
		RegionSourceSHA256: preparedRegionDigest([]byte(regionSource)), LiveInsSHA256: liveInsSHA,
		EnvironmentSHA256: digestA, ExecutionProfileSHA256: profileSHA256, ImportClosureSHA256: digestA,
		CapabilityPlanSHA256: digestA, PassConfigSHA256: digestA,
		Codec: preparedregion.PreparedRegionCodecJSONScalarV1, OutputName: "value",
	})
	if err != nil {
		t.Fatal(err)
	}
	qualificationEngine, err := wazeroengine.New(ctx, artifact, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = qualificationEngine.Close(context.Background()) })
	scratchRequest, err := json.Marshal(map[string]string{"decision": string(decisionRaw), "final_source": source, "live_ins": string(liveInsRaw)})
	if err != nil {
		t.Fatal(err)
	}
	scratchResult, _, err := qualificationEngine.ExecutePreparedRegionScratch(ctx, scratchRequest, decision)
	if err != nil {
		t.Fatal(err)
	}
	_, capsule, err := preparedregion.PublishPreparedRegionScratchResult(decision, scratchResult)
	if err != nil {
		t.Fatal(err)
	}
	session, err := qualificationEngine.NewSemanticAnalysisSession(ctx, wazeroengine.SemanticAnalysisSessionLimits{MaxRequests: 1, MaxCumulativeRequestBytes: 16 * 1024, MaxDuration: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	emitRequest, err := json.Marshal(map[string]string{"decision": string(decisionRaw), "final_source": source})
	if err != nil {
		t.Fatal(err)
	}
	bindingRaw, err := session.EmitPreparedRegionPatch(ctx, emitRequest)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	binding, err := preparedregion.DecodePreparedRegionPatchBinding(bindingRaw)
	if err != nil {
		t.Fatal(err)
	}
	_, patch, err := preparedregion.SealPreparedRegionPatch(binding)
	if err != nil {
		t.Fatal(err)
	}
	_, selection, err := preparedregion.SealPreparedRegionExecutionSelection(decision, capsule, patch)
	if err != nil {
		t.Fatal(err)
	}
	return preparedDerivedFixture{decision: decision, capsule: capsule, patch: patch, selection: selection}
}

type preparedFailureEnvelope struct {
	Status string `json:"status"`
	Error  struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		ErrorType string `json:"error_type"`
		Trace     string `json:"traceback"`
	} `json:"error"`
	Logs []string `json:"logs"`
}

func decodePreparedFailure(t *testing.T, raw []byte) preparedFailureEnvelope {
	t.Helper()
	var envelope preparedFailureEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope.Status != "error" || envelope.Error.Code != "python_exception" {
		t.Fatalf("failure envelope=%s err=%v", raw, err)
	}
	return envelope
}

func TestExactGuestPreparedRegionAdversarialPathsPreserveFailureAndConsumption(t *testing.T) {
	artifact, profile := loadPreparedRegionArtifact(t)
	config := runtimeconfig.DefaultRunConfig()
	config.Mechanisms.SemanticAnalysis = true
	config.ExecutionProfile = &profile
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	t.Run("exception before region leaves capsule unclaimed", func(t *testing.T) {
		source := "seed = 40\nif inputs[\"fail\"]:\n    raise ValueError(\"before region\")\nvalue = seed + 2\nresult = value\n"
		fixture := qualifyPreparedDerivedFixture(t, ctx, artifact, profile, source, 2, preparedregion.SourceSpan{StartLine: 4, StartColumn: 0, EndLine: 4, EndColumn: 16}, "value = seed + 2", map[string]json.RawMessage{"seed": json.RawMessage(`40`)})
		request, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{RunID: "prepared-region-before-exception", Code: source, Inputs: json.RawMessage(`{"fail":true}`)})
		if err != nil {
			t.Fatal(err)
		}
		baselineEngine, err := wazeroengine.New(ctx, artifact, config)
		if err != nil {
			t.Fatal(err)
		}
		baselineRaw, err := baselineEngine.Run(ctx, request, "")
		if err != nil {
			t.Fatal(err)
		}
		if err := baselineEngine.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		table, err := preparedregion.NewPreparedRegionTable([]preparedregion.PreparedRegionEntry{{Decision: fixture.decision, Capsule: fixture.capsule}})
		if err != nil {
			t.Fatal(err)
		}
		runner, err := (wazeroengine.Factory{PreparedRegions: table}).New(ctx, artifact, config)
		if err != nil {
			t.Fatal(err)
		}
		derivedEngine := runner.(*wazeroengine.Engine)
		derivedRaw, err := derivedEngine.RunPreparedRegionDerived(ctx, request, "", fixture.selection, fixture.decision, fixture.capsule, fixture.patch)
		if err != nil {
			t.Fatal(err)
		}
		baselineFailure, derivedFailure := decodePreparedFailure(t, baselineRaw), decodePreparedFailure(t, derivedRaw)
		if !reflect.DeepEqual(baselineFailure, derivedFailure) || derivedFailure.Error.ErrorType != "ValueError" || derivedFailure.Error.Message != "before region" || !strings.Contains(derivedFailure.Error.Trace, `line 3, in _pysolate_agent_main`) {
			t.Fatalf("baseline=%s derived=%s", baselineRaw, derivedRaw)
		}
		if evidence := table.Evidence(); evidence.Claims != 0 || evidence.Consumed != 0 || evidence.Discarded != 1 {
			t.Fatalf("pre-region exception table evidence=%+v", evidence)
		}
		if err := derivedEngine.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("exception after region preserves traceback and consumes once", func(t *testing.T) {
		source := "seed = 40\nvalue = seed + 2\nraise LookupError(\"after region\")\nresult = value\n"
		fixture := qualifyPreparedDerivedFixture(t, ctx, artifact, profile, source, 1, preparedregion.SourceSpan{StartLine: 2, StartColumn: 0, EndLine: 2, EndColumn: 16}, "value = seed + 2", map[string]json.RawMessage{"seed": json.RawMessage(`40`)})
		request, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{RunID: "prepared-region-after-exception", Code: source, Inputs: json.RawMessage(`{}`)})
		if err != nil {
			t.Fatal(err)
		}
		baselineEngine, err := wazeroengine.New(ctx, artifact, config)
		if err != nil {
			t.Fatal(err)
		}
		baselineRaw, err := baselineEngine.Run(ctx, request, "")
		if err != nil {
			t.Fatal(err)
		}
		if err := baselineEngine.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		table, err := preparedregion.NewPreparedRegionTable([]preparedregion.PreparedRegionEntry{{Decision: fixture.decision, Capsule: fixture.capsule}})
		if err != nil {
			t.Fatal(err)
		}
		runner, err := (wazeroengine.Factory{PreparedRegions: table}).New(ctx, artifact, config)
		if err != nil {
			t.Fatal(err)
		}
		derivedEngine := runner.(*wazeroengine.Engine)
		derivedRaw, err := derivedEngine.RunPreparedRegionDerived(ctx, request, "", fixture.selection, fixture.decision, fixture.capsule, fixture.patch)
		if err != nil {
			t.Fatal(err)
		}
		baselineFailure, derivedFailure := decodePreparedFailure(t, baselineRaw), decodePreparedFailure(t, derivedRaw)
		if !reflect.DeepEqual(baselineFailure, derivedFailure) || derivedFailure.Error.ErrorType != "LookupError" || derivedFailure.Error.Message != "after region" || !strings.Contains(derivedFailure.Error.Trace, `line 3, in _pysolate_agent_main`) {
			t.Fatalf("baseline=%s derived=%s", baselineRaw, derivedRaw)
		}
		if evidence := table.Evidence(); evidence.Claims != 1 || evidence.Consumed != 1 || evidence.RejectedClaims != 0 || evidence.Discarded != 0 {
			t.Fatalf("post-region exception table evidence=%+v", evidence)
		}
		if err := derivedEngine.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("pre-cancelled execution leaves capsule unclaimed", func(t *testing.T) {
		source := "seed = 40\nvalue = seed + 2\nresult = value\n"
		fixture := qualifyPreparedDerivedFixture(t, ctx, artifact, profile, source, 1, preparedregion.SourceSpan{StartLine: 2, StartColumn: 0, EndLine: 2, EndColumn: 16}, "value = seed + 2", map[string]json.RawMessage{"seed": json.RawMessage(`40`)})
		table, err := preparedregion.NewPreparedRegionTable([]preparedregion.PreparedRegionEntry{{Decision: fixture.decision, Capsule: fixture.capsule}})
		if err != nil {
			t.Fatal(err)
		}
		runner, err := (wazeroengine.Factory{PreparedRegions: table}).New(ctx, artifact, config)
		if err != nil {
			t.Fatal(err)
		}
		derivedEngine := runner.(*wazeroengine.Engine)
		request, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{RunID: "prepared-region-cancelled", Code: source, Inputs: json.RawMessage(`{}`)})
		if err != nil {
			t.Fatal(err)
		}
		cancelled, cancelNow := context.WithCancel(ctx)
		cancelNow()
		if raw, err := derivedEngine.RunPreparedRegionDerived(cancelled, request, "", fixture.selection, fixture.decision, fixture.capsule, fixture.patch); err == nil || raw != nil {
			t.Fatalf("pre-cancelled response=%s err=%v", raw, err)
		}
		if evidence := table.Evidence(); evidence.Ready != 0 || evidence.Claims != 0 || evidence.Consumed != 0 || evidence.Discarded != 1 {
			t.Fatalf("cancelled execution consumed capsule: %+v", evidence)
		}
		if err := derivedEngine.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		if evidence := table.Evidence(); evidence.Discarded != 1 {
			t.Fatalf("cancelled close evidence=%+v", evidence)
		}
	})
}

const digestA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func preparedRegionDigest(value []byte) string {
	digest := sha256.Sum256(value)
	return fmt.Sprintf("sha256:%x", digest[:])
}

func runPreparedRegionExact(t *testing.T, artifact []byte, config runtimeconfig.RunConfig, table *preparedregion.PreparedRegionTable, runID string, code string) []byte {
	t.Helper()
	runner, err := (wazeroengine.Factory{PreparedRegions: table}).New(context.Background(), artifact, config)
	if err != nil {
		t.Fatal(err)
	}
	properties := runner.Properties()
	if properties.CapabilityBrokerAvailable || properties.WorkspaceMounted {
		t.Fatalf("prepared region helper gained authority: %+v", properties)
	}
	request, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{RunID: runID, Code: code, Inputs: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	response, err := runner.Run(context.Background(), request, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	return response
}

func loadPreparedRegionArtifact(t *testing.T) ([]byte, runtimeconfig.ExecutionProfile) {
	t.Helper()
	artifactPath := guestArtifact(t)
	artifact, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(filepath.Dir(artifactPath), "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	digest := func(value []byte) string { sum := sha256.Sum256(value); return fmt.Sprintf("sha256:%x", sum) }
	profile, err := runtimeconfig.NewExecutionProfile("base", []string{"json"})
	if err != nil {
		t.Fatal(err)
	}
	profile, err = profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{ProfileID: "base", ArtifactSHA256: digest(artifact), ManifestSHA256: digest(manifest), ImportRoots: []string{"json"}, QualifiedImportRoots: []string{"json"}})
	if err != nil {
		t.Fatal(err)
	}
	return artifact, profile
}

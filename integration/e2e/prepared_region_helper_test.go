package e2e_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

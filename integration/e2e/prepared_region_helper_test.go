package e2e_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

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
	runner, err := (wazeroengine.Factory{PreparedRegions: table}).New(context.Background(), artifact, config)
	if err != nil {
		t.Fatal(err)
	}
	request, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{RunID: "prepared-region-positive", Code: fmt.Sprintf("result = __pysolate_materialize_value__(%q)", decision.IdentitySHA256), Inputs: json.RawMessage(`{}`)})
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
}

const digestA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

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

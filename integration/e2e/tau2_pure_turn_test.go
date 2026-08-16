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
	"github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

func TestTau2PureDynamicModelTurnThroughZeroCapabilityGuest(t *testing.T) {
	sourcePath := os.Getenv("PYSOLATE_TAU2_PURE_SOURCE_FILE")
	outputPath := os.Getenv("PYSOLATE_TAU2_PURE_OUTPUT_FILE")
	if sourcePath == "" || outputPath == "" {
		t.Skip("pure dynamic turn environment is required")
	}
	if !filepath.IsAbs(sourcePath) || !filepath.IsAbs(outputPath) || sourcePath == outputPath {
		t.Fatal("pure dynamic paths must be distinct absolute paths")
	}
	for _, path := range []string{sourcePath, filepath.Dir(outputPath)} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("private pure path has group/other permissions: %s", path)
		}
	}
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(source) == 0 || len(source) > 16*1024 {
		t.Fatal("pure source size is outside the bounded contract")
	}
	wasm, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := capability.NewRegistry().Seal(capability.PlanConfig{MaxCalls: 1})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := runtimeconfig.NewExecutionProfile("base", []string{"json"})
	if err != nil {
		t.Fatal(err)
	}
	profile, err = profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{
		ProfileID: "base", ArtifactSHA256: tau2Digest(wasm), ManifestSHA256: tau2Digest([]byte("tau2-retail-24-pure-manifest")),
		ImportRoots: []string{"json"}, QualifiedImportRoots: []string{"json"},
	})
	if err != nil {
		t.Fatal(err)
	}

	analysisConfig := runtimeconfig.DefaultRunConfig()
	analysisConfig.ExecutionProfile = &profile
	analysisConfig.Mechanisms.SemanticAnalysis = true
	analysisRunner, err := (wazero.Factory{}).New(context.Background(), wasm, analysisConfig)
	if err != nil {
		t.Fatal(err)
	}
	analysisRequest, err := semantic.NewRequest(string(source), semantic.Bindings{
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
	if err != nil || len(analysis.CallSites) != 0 {
		t.Fatalf("pure analysis=%+v err=%v", analysis, err)
	}

	parent := "tau2-retail-24-pure"
	presentation, err := plan.Present(capability.ProgramSurfaceProgrammatic, parent)
	if err != nil {
		t.Fatal(err)
	}
	var broker *capability.Broker
	config := runtimeconfig.DefaultRunConfig()
	config.ExecutionProfile = &profile
	config.ProgramSurface = runtimeconfig.ProgramSurfaceProgrammatic
	config.Mechanisms.ProgrammaticToolCalling = true
	runner, err := (wazero.Factory{BrokerFactory: func(context.Context) (*capability.Broker, error) {
		created, createErr := capability.NewBroker(capability.Config{RunIdentity: parent, Plan: plan, ProgrammaticParentCallID: parent})
		broker = created
		return created, createErr
	}}).New(context.Background(), wasm, config)
	if err != nil {
		t.Fatal(err)
	}
	request, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{RunID: parent, Code: string(source), Inputs: []byte(`{}`)})
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
		t.Fatalf("pure content unavailable err=%v", err)
	}
	if broker == nil || broker.CallCount() != 0 || len(broker.SnapshotReceipts()) != 0 {
		t.Fatalf("pure broker calls=%d receipts=%d", broker.CallCount(), len(broker.SnapshotReceipts()))
	}
	encoded, err := json.MarshalIndent(map[string]any{
		"schema_version":         "pysolate.tau2-pure-turn-private.v1",
		"artifact_sha256":        tau2Digest(wasm),
		"capability_plan_sha256": plan.Identity(),
		"source_sha256":          tau2Digest(source),
		"semantic_call_sites":    0,
		"broker_call_count":      0,
		"receipt_count":          0,
		"request_sha256":         tau2Digest(request),
		"response_sha256":        tau2Digest(payload),
		"content":                content,
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outputPath, append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(outputPath, 0o600); err != nil {
		t.Fatal(err)
	}
}

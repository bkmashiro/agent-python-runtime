package e2e_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/semanticspeculation"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/agentfunction"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

func TestExactGuestRemediatedFrozenPhase4RegionMechanismMatrix(t *testing.T) {
	matrixRaw, err := os.ReadFile("../../docs/evidence/semantic-speculation-phase4-region-case-matrix-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	matrix, err := semanticspeculation.DecodePhase4RegionCaseMatrix(matrixRaw)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	artifactDigest := sha256.Sum256(artifact)
	artifactSHA := fmt.Sprintf("sha256:%x", artifactDigest[:])
	if artifactSHA != semanticspeculation.Phase4RegionRemediationArtifactSHA256 {
		t.Fatalf("artifact=%s remediation=%s", artifactSHA, semanticspeculation.Phase4RegionRemediationArtifactSHA256)
	}
	allowedImports := []string{"json"}
	profile, err := runtimeconfig.NewExecutionProfile("base", allowedImports)
	if err != nil {
		t.Fatal(err)
	}
	profile, err = profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{
		ProfileID: "base", ArtifactSHA256: artifactSHA, ManifestSHA256: semanticTestDigest('7'),
		ImportRoots: allowedImports, QualifiedImportRoots: allowedImports,
	})
	if err != nil {
		t.Fatal(err)
	}
	config := runtimeconfig.DefaultRunConfig()
	config.ExecutionProfile = &profile
	config.Mechanisms = runtimeconfig.MechanismSet{SemanticAnalysis: true}
	runner, err := (wazeroengine.Factory{}).New(context.Background(), artifact, config)
	if err != nil {
		t.Fatal(err)
	}
	engine := trustedSemanticRunner(t, runner)
	defer engine.Close(context.Background())
	plan := phase4RegionCapabilityPlan(t)
	bindings := semantic.Bindings{
		ArtifactSHA256: artifactSHA, ExecutionProfileSHA256: engine.Properties().ExecutionProfileBindingSHA256,
		ImportClosureSHA256: agentfunction.ImportClosureIdentity(allowedImports, allowedImports), CapabilityPlanSHA256: plan.Identity(),
	}

	for _, candidate := range matrix.Cases {
		candidate := candidate
		t.Run(candidate.ID, func(t *testing.T) {
			session, sessionErr := engine.NewSemanticAnalysisSession(context.Background(), wazeroengine.SemanticAnalysisSessionLimits{
				MaxRequests: 1, MaxCumulativeRequestBytes: uint64(config.MaxRequestBytes), MaxDuration: config.Timeout,
			})
			if sessionErr != nil {
				t.Fatal(sessionErr)
			}
			defer session.Close(context.Background())
			request, requestErr := semantic.NewRequest(candidate.Source, bindings, plan)
			if requestErr != nil {
				t.Fatal(requestErr)
			}
			verified, analyzeErr := semantic.AnalyzeVerifiedSession(context.Background(), session, request)
			if analyzeErr != nil {
				t.Fatal(analyzeErr)
			}
			analysis, analysisErr := verified.Analysis()
			if analysisErr != nil || int(candidate.FocusRegionIndex) >= len(analysis.CandidateRegions) {
				t.Fatalf("regions=%d focus=%d err=%v", len(analysis.CandidateRegions), candidate.FocusRegionIndex, analysisErr)
			}
			focus := analysis.CandidateRegions[candidate.FocusRegionIndex]
			if focus.LocallyReusable() != candidate.ExpectedLocalReusable {
				t.Fatalf("expected_local=%t focus=%+v", candidate.ExpectedLocalReusable, focus)
			}
		})
	}
}

func phase4RegionCapabilityPlan(t *testing.T) *capability.Plan {
	t.Helper()
	registry := capability.NewRegistry()
	spec, grant, err := capability.DemoCatalogDefinition(capability.DemoCatalogPolicy{
		Endpoint: "http://127.0.0.1:1", Timeout: time.Second, MaxResponseBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(spec, grant, capability.NewPlaybackHandler()); err != nil {
		t.Fatalf("register demo catalog: %v", err)
	}
	mailSpec := capability.Spec{
		Name: "mail.send", Version: "pysolate.phase4-mail.v1", Description: "Frozen region effect control.",
		EffectClass: capability.EffectWorkspaceWrite, Playback: capability.PlaybackLiveOnly, HandlerIdentity: "pysolate.phase4-mail-handler.v1",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"value":{"type":"integer"}},"required":["value"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`),
		Python:       &capability.PythonProjection{Module: "mail", Method: "send", Arguments: []string{"value"}},
	}
	mailGrant, err := capability.NewGrant(json.RawMessage(`{"study":"phase4-region-census"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(mailSpec, mailGrant, capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	})); err != nil {
		t.Fatalf("register mail control: %v", err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 2})
	if err != nil {
		t.Fatalf("seal region plan: %v", err)
	}
	return plan
}

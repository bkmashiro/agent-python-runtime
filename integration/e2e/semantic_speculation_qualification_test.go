package e2e_test

import (
	"context"
	"encoding/json"
	"os"
	"sync/atomic"
	"testing"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

func TestSemanticSidecarDoesNotAuthorizeDynamicWrapper(t *testing.T) {
	wasm, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	artifactSHA := testDigestBytes(wasm)
	profile, err := runtimeconfig.NewExecutionProfile("base", []string{"json"})
	if err != nil {
		t.Fatal(err)
	}
	profile, err = profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{
		ProfileID: "base", ArtifactSHA256: artifactSHA, ManifestSHA256: testDigest("wrapper-manifest"),
		ImportRoots: []string{"json"}, QualifiedImportRoots: []string{"json"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var physical atomic.Int32
	plan := streamingPreDispatchPlan(t, capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		physical.Add(1)
		return json.RawMessage(`{"value":"weather"}`), nil
	}))
	config := runtimeconfig.DefaultRunConfig()
	config.ExecutionProfile = &profile
	config.Mechanisms.SemanticAnalysis = true
	runner, err := wazeroengine.New(context.Background(), wasm, config)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	bindings := semantic.Bindings{
		ArtifactSHA256: artifactSHA, ExecutionProfileSHA256: runner.Properties().ExecutionProfileBindingSHA256,
		ImportClosureSHA256: testDigest("wrapper-imports"), CapabilityPlanSHA256: plan.Identity(),
	}
	source := "def wrapped(key):\n    return sources.read(key)\nresult = wrapped(\"weather\")\n"
	request, err := semantic.NewRequest(source, bindings, plan)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := semantic.AnalyzeVerified(context.Background(), trustedSemanticRunner(t, runner), request)
	if err != nil {
		t.Fatal(err)
	}
	analysis, err := verified.Analysis()
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.CallSites) != 0 || analysis.CallSiteCoverage != "positive_only" || len(analysis.Functions) != 1 ||
		len(analysis.Functions[0].DirectCapabilities) != 1 || analysis.Functions[0].DirectCapabilities[0] != "sources.read" {
		t.Fatalf("wrapper classification=%+v", analysis)
	}
	unknownDynamicRegion := false
	for _, region := range analysis.CandidateRegions {
		if region.Kind != semantic.CandidateRegionStraightLine {
			continue
		}
		for _, reason := range region.RejectionReasons {
			if reason == semantic.CandidateRejectUnknownEffect {
				unknownDynamicRegion = true
			}
		}
	}
	if !unknownDynamicRegion {
		t.Fatalf("wrapper invocation lacked unknown-effect rejection: %+v", analysis.CandidateRegions)
	}
	planned, err := semantic.BuildSourceBoundPlan(verified, plan, semantic.PlannerConfig{
		Passes: []semantic.PassConfig{{Name: semantic.PassSemanticPreDispatch, Version: semantic.SemanticPreDispatchPassVersion, Enabled: true}},
		PreissueContext: semantic.PreissueContext{
			StreamEpoch: "wrapper-stream", WorkflowEpoch: "wrapper-workflow", FreshnessEpoch: "plan-epoch-1", ExpiryEpoch: "wrapper-expiry",
			PrivacyPartition: "private-wrapper", ParentLineageSHA256: testDigest("wrapper-parent"),
			BudgetReservationSHA256: testDigest("wrapper-budget"), RemainingPhysicalReads: 1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	projection := planned.Projection()
	if len(projection.Decisions) != 0 || physical.Load() != 0 {
		t.Fatalf("dynamic wrapper was authorized: plan=%+v physical=%d", projection, physical.Load())
	}
}

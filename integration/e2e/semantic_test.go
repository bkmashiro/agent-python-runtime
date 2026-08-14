package e2e_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/agentfunction"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

func TestRealGuestSemanticAnalysisBuildsReusableWholeRunPlan(t *testing.T) {
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	artifactDigest := sha256.Sum256(artifact)
	artifactSHA := fmt.Sprintf("sha256:%x", artifactDigest[:])
	allowedImports := []string{"math"}
	profile, err := runtimeconfig.NewExecutionProfile("base", allowedImports)
	if err != nil {
		t.Fatal(err)
	}
	profile, err = profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{
		ProfileID: "base", ArtifactSHA256: artifactSHA,
		ManifestSHA256: semanticTestDigest('9'), ImportRoots: allowedImports,
		QualifiedImportRoots: allowedImports,
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
	defer runner.Close(context.Background())
	request, err := semantic.NewRequest(
		"def double(value):\n    return value * 2\nresult = double(inputs['value'])\n",
		semantic.Bindings{
			ArtifactSHA256:         artifactSHA,
			ExecutionProfileSHA256: runner.Properties().ExecutionProfileBindingSHA256,
			ImportClosureSHA256:    agentfunction.ImportClosureIdentity(allowedImports, allowedImports),
			CapabilityPlanSHA256:   semanticTestDigest('2'),
		}, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	analysis, err := semantic.Analyze(context.Background(), runner, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.Functions) != 1 || analysis.ModuleEffects != (semantic.EffectSummary{}) || len(analysis.Barriers) != 0 {
		t.Fatalf("analysis=%+v", analysis)
	}
	plan, census, err := semantic.BuildWholeRunPlan(analysis, semantic.WholeRunConfig{InputsCanonical: true, OutputsCanonical: true})
	if err != nil || !plan.Regions[0].Reusable() || census.ReusableRegions != 1 {
		t.Fatalf("plan=%+v census=%+v err=%v", plan, census, err)
	}
	unknownRequest := request
	unknownRequest.Source = "result=eval('1+1')\n"
	unknown, err := semantic.Analyze(context.Background(), runner, unknownRequest)
	if err != nil {
		t.Fatal(err)
	}
	blocked, blockedCensus, err := semantic.BuildWholeRunPlan(unknown, semantic.WholeRunConfig{InputsCanonical: true, OutputsCanonical: true})
	if err != nil || blocked.Regions[0].Reusable() || len(blockedCensus.BarrierCounts) == 0 {
		t.Fatalf("blocked=%+v census=%+v err=%v", blocked, blockedCensus, err)
	}
}

func TestRealGuestSemanticOverlayBindsExactModuleEntryCall(t *testing.T) {
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	artifactDigest := sha256.Sum256(artifact)
	artifactSHA := fmt.Sprintf("sha256:%x", artifactDigest[:])
	profile, err := runtimeconfig.NewExecutionProfile("base", []string{"json"})
	if err != nil {
		t.Fatal(err)
	}
	profile, err = profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{
		ProfileID: "base", ArtifactSHA256: artifactSHA, ManifestSHA256: semanticTestDigest('8'),
		ImportRoots: []string{"json"}, QualifiedImportRoots: []string{"json"},
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
	defer runner.Close(context.Background())
	spec, grant, err := capability.DemoCatalogDefinition(capability.DemoCatalogPolicy{
		Endpoint: "http://127.0.0.1:1", Timeout: time.Second, MaxResponseBytes: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	registry := capability.NewRegistry()
	if err := registry.Register(spec, grant, capability.NewPlaybackHandler()); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 1})
	if err != nil {
		t.Fatal(err)
	}
	request, err := semantic.NewRequest("result = sources.demo_catalog()\n", semantic.Bindings{
		ArtifactSHA256: artifactSHA, ExecutionProfileSHA256: runner.Properties().ExecutionProfileBindingSHA256,
		ImportClosureSHA256: agentfunction.ImportClosureIdentity([]string{"json"}, []string{"json"}), CapabilityPlanSHA256: plan.Identity(),
	}, plan)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := semantic.AnalyzeVerified(context.Background(), runner, request)
	if err != nil {
		t.Fatal(err)
	}
	analysis, err := verified.Analysis()
	if err != nil || len(analysis.CallSites) != 1 {
		t.Fatalf("analysis=%+v err=%v", analysis, err)
	}
	site := analysis.CallSites[0]
	if !site.NecessarilyReached || site.Capability != spec.Name || string(site.CanonicalArguments) != `{}` || site.DynamicOccurrence != 1 {
		t.Fatalf("call site=%+v", site)
	}
	conditional, err := semantic.NewRequest("if inputs['flag']:\n    result = sources.demo_catalog()\n", request.Bindings, plan)
	if err != nil {
		t.Fatal(err)
	}
	conditionalAnalysis, err := semantic.Analyze(context.Background(), runner, conditional)
	if err != nil || len(conditionalAnalysis.CallSites) != 0 {
		t.Fatalf("conditional analysis=%+v err=%v", conditionalAnalysis, err)
	}
}

func TestSemanticAnalyzerIsDefaultOff(t *testing.T) {
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	runner, err := (wazeroengine.Factory{}).New(context.Background(), artifact, runtimeconfig.DefaultRunConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	analyzer := runner.(*wazeroengine.Engine)
	if _, err := analyzer.AnalyzeSemantic(context.Background(), []byte(`{}`)); !errors.Is(err, runtimeconfig.ErrMechanismDisabled) {
		t.Fatalf("default-off error=%v", err)
	}
}

func semanticTestDigest(value byte) string {
	return "sha256:" + string(makeSemanticBytes(value, 64))
}

func makeSemanticBytes(value byte, count int) []byte {
	result := make([]byte, count)
	for index := range result {
		result[index] = value
	}
	return result
}

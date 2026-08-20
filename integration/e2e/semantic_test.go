package e2e_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/labview"
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
	source := "def double(value):\n    return value * 2\nresult = double(inputs['value'])\n"
	request, err := semantic.NewRequest(
		source,
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
	verified, err := semantic.AnalyzeVerified(context.Background(), trustedSemanticRunner(t, runner), request)
	if err != nil {
		t.Fatal(err)
	}
	analysis, err := verified.Analysis()
	if err != nil {
		t.Fatal(err)
	}
	if len(analysis.Functions) != 1 || analysis.ModuleEffects != (semantic.EffectSummary{}) || len(analysis.Barriers) != 0 || len(analysis.CandidateRegions) != 2 {
		t.Fatalf("analysis=%+v", analysis)
	}
	projection, err := labview.ProjectSemanticRegionGraph(verified, source, true)
	if err != nil {
		t.Logf("verified analysis=%+v", analysis)
		t.Logf("invalid projection=%+v", projection)
		t.Fatalf("project semantic region graph: %v", err)
	}
	if len(projection.Regions) != 2 || !projection.SourceAvailable || projection.Source != source {
		t.Fatalf("projection=%+v", projection)
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
	verified, err := semantic.AnalyzeVerified(context.Background(), trustedSemanticRunner(t, runner), request)
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
	regionSource := "seed = 40\nvalue = seed * 2 + 2\nremote = sources.demo_catalog()\nresult = value\n"
	regionRequest, err := semantic.NewRequest(regionSource, request.Bindings, plan)
	if err != nil {
		t.Fatal(err)
	}
	regionAnalysis, err := semantic.Analyze(context.Background(), runner, regionRequest)
	if err != nil || len(regionAnalysis.CandidateRegions) != 4 {
		t.Fatalf("region-local analysis=%+v err=%v", regionAnalysis, err)
	}
	seed, value, effect, result := regionAnalysis.CandidateRegions[0], regionAnalysis.CandidateRegions[1], regionAnalysis.CandidateRegions[2], regionAnalysis.CandidateRegions[3]
	if !seed.LocallyReusable() || !value.LocallyReusable() || value.Effects != (semantic.EffectSummary{}) ||
		len(value.DataDependencies) != 1 || value.DataDependencies[0].Name != "seed" || value.DataDependencies[0].ProducerRegionID != seed.ID ||
		!effect.Effects.MayObserveLive || effect.LocallyReusable() || !containsCandidateRejection(effect.RejectionReasons, semantic.CandidateRejectMayRaise) || !result.LocallyReusable() {
		t.Fatalf("seed=%+v value=%+v effect=%+v result=%+v", seed, value, effect, result)
	}
	for _, negative := range []struct {
		source   string
		expected semantic.CandidateRejection
	}{
		{"value = mystery(1)\n", semantic.CandidateRejectUnknownEffect},
		{"items = [1]\nitems[0] = 2\n", semantic.CandidateRejectHeapMutation},
		{"value = 1 // 0\n", semantic.CandidateRejectMayRaise},
		{"if True:\n    value = 1\n", semantic.CandidateRejectOpaqueControl},
	} {
		negativeRequest, requestErr := semantic.NewRequest(negative.source, request.Bindings, plan)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		negativeAnalysis, analyzeErr := semantic.Analyze(context.Background(), runner, negativeRequest)
		found := false
		for _, region := range negativeAnalysis.CandidateRegions {
			found = found || containsCandidateRejection(region.RejectionReasons, negative.expected)
		}
		if analyzeErr != nil || !found {
			t.Fatalf("negative source=%q expected=%s analysis=%+v err=%v", negative.source, negative.expected, negativeAnalysis, analyzeErr)
		}
	}
	conditional, err := semantic.NewRequest("if inputs['flag']:\n    result = sources.demo_catalog()\n", request.Bindings, plan)
	if err != nil {
		t.Fatal(err)
	}
	conditionalAnalysis, err := semantic.Analyze(context.Background(), runner, conditional)
	if err != nil || len(conditionalAnalysis.CallSites) != 0 {
		t.Fatalf("conditional analysis=%+v err=%v", conditionalAnalysis, err)
	}
	for _, source := range []string{
		"sources = inputs['wrapper']\nresult = sources.demo_catalog()\n",
		"def fetch():\n    return sources.demo_catalog()\nresult = fetch()\n",
	} {
		blockedRequest, requestErr := semantic.NewRequest(source, request.Bindings, plan)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		blocked, analyzeErr := semantic.Analyze(context.Background(), runner, blockedRequest)
		hasBarrier := len(blocked.Barriers) > 0
		for _, region := range blocked.CandidateRegions {
			hasBarrier = hasBarrier || len(region.Barriers) > 0 || len(region.RejectionReasons) > 0
		}
		if analyzeErr != nil || len(blocked.CallSites) != 0 || !hasBarrier {
			t.Fatalf("source=%q blocked=%+v err=%v", source, blocked, analyzeErr)
		}
	}
}

func containsCandidateRejection(values []semantic.CandidateRejection, expected semantic.CandidateRejection) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
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

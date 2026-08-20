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

func TestExactGuestSemanticAnalysisSessionMatchesFreshAndCannotBeReused(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
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
		ProfileID: "base", ArtifactSHA256: artifactSHA, ManifestSHA256: semanticTestDigest('7'),
		ImportRoots: []string{"json"}, QualifiedImportRoots: []string{"json"},
	})
	if err != nil {
		t.Fatal(err)
	}
	config := runtimeconfig.DefaultRunConfig()
	config.ExecutionProfile = &profile
	config.Mechanisms = runtimeconfig.MechanismSet{SemanticAnalysis: true}
	analyzer, err := wazeroengine.New(ctx, artifact, config)
	if err != nil {
		t.Fatal(err)
	}
	defer analyzer.Close(context.Background())
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
	bindings := semantic.Bindings{
		ArtifactSHA256: artifactSHA, ExecutionProfileSHA256: analyzer.Properties().ExecutionProfileBindingSHA256,
		ImportClosureSHA256: agentfunction.ImportClosureIdentity([]string{"json"}, []string{"json"}), CapabilityPlanSHA256: plan.Identity(),
	}
	requests := make([]semantic.Request, 0, 2)
	for _, source := range []string{
		"result = sources.demo_catalog()\n",
		"def fetch():\n    return sources.demo_catalog()\nresult = fetch()\n",
	} {
		request, requestErr := semantic.NewRequest(source, bindings, plan)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		requests = append(requests, request)
	}
	session, err := analyzer.NewSemanticAnalysisSession(ctx, wazeroengine.SemanticAnalysisSessionLimits{
		MaxRequests: 2, MaxCumulativeRequestBytes: 2 * semantic.MaxDocumentBytes, MaxDuration: 15 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range requests {
		fresh, freshErr := semantic.AnalyzeVerified(ctx, analyzer, request)
		if freshErr != nil {
			t.Fatal(freshErr)
		}
		reused, reusedErr := semantic.AnalyzeVerifiedSession(ctx, session, request)
		if reusedErr != nil {
			t.Fatal(reusedErr)
		}
		freshAnalysis, _ := fresh.Analysis()
		reusedAnalysis, _ := reused.Analysis()
		freshIdentity, _, _ := freshAnalysis.Identity()
		reusedIdentity, _, _ := reusedAnalysis.Identity()
		if freshIdentity != reusedIdentity {
			t.Fatalf("fresh=%s session=%s", freshIdentity, reusedIdentity)
		}
	}
	if _, err := semantic.AnalyzeVerifiedSession(ctx, session, requests[0]); !errors.Is(err, wazeroengine.ErrSemanticAnalysisSessionLimit) {
		t.Fatalf("request limit err=%v", err)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	evidence := analyzer.SemanticAnalysisLifecycleEvidence()
	if evidence.Invocations != 5 || evidence.ModuleInstantiations != 3 || evidence.InitializeCalls != 3 ||
		evidence.RuntimeInitCalls != 3 || evidence.Successes != 4 || evidence.Failures != 1 {
		t.Fatalf("lifecycle=%+v", evidence)
	}
	if _, err := semantic.AnalyzeVerifiedSession(ctx, session, requests[0]); !errors.Is(err, wazeroengine.ErrSemanticAnalysisSessionClosed) {
		t.Fatalf("closed session err=%v", err)
	}
}

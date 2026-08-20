package e2e_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	goruntime "runtime"
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

func TestExactGuestSemanticAnalysisSessionConsumesSingleUsePreparedRuntime(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	config := runtimeconfig.DefaultRunConfig()
	artifactSHA := testDigestBytes(artifact)
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
	config.ExecutionProfile = &profile
	config.Mechanisms = runtimeconfig.MechanismSet{SemanticAnalysis: true, PreparedRuntime: true}
	registry := capability.NewRegistry()
	spec, grant, err := capability.DemoCatalogDefinition(capability.DemoCatalogPolicy{Endpoint: "http://127.0.0.1:1", Timeout: time.Second, MaxResponseBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(spec, grant, capability.NewPlaybackHandler()); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 1})
	if err != nil {
		t.Fatal(err)
	}
	run := func(runConfig runtimeconfig.RunConfig, preprovision, consumeTwice bool) (wazeroengine.PreparedState, wazeroengine.PreparedImageState, wazeroengine.SemanticAnalysisLifecycleEvidence, string) {
		t.Helper()
		analyzer, newErr := wazeroengine.New(ctx, artifact, runConfig)
		if newErr != nil {
			t.Fatal(newErr)
		}
		closed := false
		defer func() {
			if !closed {
				_ = analyzer.Close(context.Background())
			}
		}()
		if preprovision {
			if prepareErr := analyzer.PrepareSemanticRuntime(ctx); prepareErr != nil {
				t.Fatal(prepareErr)
			}
			if state := analyzer.PreparedState(); !state.Ready || state.PreparedRuns != 0 {
				t.Fatalf("preprovisioned state=%+v", state)
			}
		}
		request, requestErr := semantic.NewRequest("result = sources.demo_catalog()\n", semantic.Bindings{
			ArtifactSHA256: artifactSHA, ExecutionProfileSHA256: analyzer.Properties().ExecutionProfileBindingSHA256,
			ImportClosureSHA256: agentfunction.ImportClosureIdentity([]string{"json"}, []string{"json"}), CapabilityPlanSHA256: plan.Identity(),
		}, plan)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		var identity string
		analyzeOnce := func() {
			session, sessionErr := analyzer.NewSemanticAnalysisSession(ctx, wazeroengine.SemanticAnalysisSessionLimits{
				MaxRequests: 1, MaxCumulativeRequestBytes: semantic.MaxDocumentBytes, MaxDuration: 15 * time.Second,
			})
			if sessionErr != nil {
				t.Fatal(sessionErr)
			}
			verified, analyzeErr := semantic.AnalyzeVerifiedSession(ctx, session, request)
			if analyzeErr != nil {
				t.Fatal(analyzeErr)
			}
			analysis, _ := verified.Analysis()
			identity, _, analyzeErr = analysis.Identity()
			if analyzeErr != nil {
				t.Fatal(analyzeErr)
			}
			if closeErr := session.Close(context.Background()); closeErr != nil {
				t.Fatal(closeErr)
			}
		}
		analyzeOnce()
		if consumeTwice {
			analyzeOnce()
		}
		state := analyzer.PreparedState()
		image := analyzer.PreparedImageState()
		evidence := analyzer.SemanticAnalysisLifecycleEvidence()
		if closeErr := analyzer.Close(context.Background()); closeErr != nil {
			t.Fatal(closeErr)
		}
		closed = true
		if retained := analyzer.PreparedImageState(); retained.Available {
			t.Fatalf("closed analyzer retained prepared image: %+v", retained)
		}
		return state, image, evidence, identity
	}
	coldState, _, coldEvidence, coldIdentity := run(config, false, false)
	t.Logf("cold prepared=%+v lifecycle=%+v", coldState, coldEvidence)
	if !coldState.Selected || coldState.Ready || coldState.PreparedRuns != 1 || coldState.FreshFallbackRuns != 0 ||
		coldEvidence.PreparedProvisions != 1 || coldEvidence.PreparedHits != 1 || coldEvidence.FreshFallbacks != 0 ||
		coldEvidence.PreparedProvisionNanos == 0 || coldEvidence.ModuleInstantiations != 1 || coldEvidence.RuntimeInitCalls != 1 {
		t.Fatalf("cold prepared=%+v lifecycle=%+v", coldState, coldEvidence)
	}
	warmState, _, warmEvidence, warmIdentity := run(config, true, true)
	t.Logf("preprovisioned prepared=%+v lifecycle=%+v", warmState, warmEvidence)
	if !warmState.Selected || warmState.Ready || warmState.PreparedRuns != 1 || warmState.FreshFallbackRuns != 1 ||
		warmEvidence.PreparedProvisions != 0 || warmEvidence.PreparedHits != 1 || warmEvidence.FreshFallbacks != 1 ||
		warmEvidence.PreparedProvisionNanos != 0 || warmEvidence.ModuleInstantiations != 1 || warmEvidence.RuntimeInitCalls != 1 {
		t.Fatalf("preprovisioned prepared=%+v lifecycle=%+v", warmState, warmEvidence)
	}
	if coldIdentity != warmIdentity {
		t.Fatalf("cold identity=%s preprovisioned identity=%s", coldIdentity, warmIdentity)
	}
	if goruntime.GOOS != "linux" {
		cowConfig := config
		cowConfig.Mechanisms.MemoryCOW = true
		fallbackState, _, fallbackEvidence, fallbackIdentity := run(cowConfig, false, true)
		if fallbackState.FreshFallbackRuns != 2 || fallbackEvidence.PreparedProvisionFailures != 1 ||
			fallbackEvidence.FreshFallbacks != 2 || fallbackEvidence.Successes != 2 || fallbackEvidence.COWHits != 0 {
			t.Fatalf("unsupported COW fallback prepared=%+v lifecycle=%+v", fallbackState, fallbackEvidence)
		}
		if fallbackIdentity != coldIdentity {
			t.Fatalf("fallback identity=%s cold identity=%s", fallbackIdentity, coldIdentity)
		}
	} else {
		cowConfig := config
		cowConfig.Mechanisms.MemoryCOW = true
		cowState, image, cowEvidence, cowIdentity := run(cowConfig, false, false)
		t.Logf("linux COW prepared=%+v image=%+v lifecycle=%+v", cowState, image, cowEvidence)
		if !cowState.Ready || cowState.PreparedRuns != 1 || cowState.FreshFallbackRuns != 0 || !image.Available || image.BaselineBytes == 0 ||
			cowEvidence.PreparedProvisions != 1 || cowEvidence.COWHits != 1 || cowEvidence.PreparedHits != 0 ||
			cowEvidence.FreshFallbacks != 0 || cowEvidence.ModuleInstantiations != 2 || cowEvidence.InitializeCalls != 2 || cowEvidence.RuntimeInitCalls != 1 {
			t.Fatalf("COW prepared=%+v image=%+v lifecycle=%+v", cowState, image, cowEvidence)
		}
		if cowIdentity != coldIdentity {
			t.Fatalf("COW identity=%s cold identity=%s", cowIdentity, coldIdentity)
		}
		warmCOWState, warmImage, warmCOWEvidence, warmCOWIdentity := run(cowConfig, true, false)
		t.Logf("linux preprovisioned COW prepared=%+v image=%+v lifecycle=%+v", warmCOWState, warmImage, warmCOWEvidence)
		if !warmCOWState.Ready || warmCOWState.PreparedRuns != 1 || warmCOWState.FreshFallbackRuns != 0 || !warmImage.Available ||
			warmCOWEvidence.PreparedProvisions != 0 || warmCOWEvidence.PreparedProvisionNanos != 0 || warmCOWEvidence.COWHits != 1 ||
			warmCOWEvidence.FreshFallbacks != 0 || warmCOWEvidence.ModuleInstantiations != 1 || warmCOWEvidence.InitializeCalls != 1 || warmCOWEvidence.RuntimeInitCalls != 0 {
			t.Fatalf("preprovisioned COW prepared=%+v image=%+v lifecycle=%+v", warmCOWState, warmImage, warmCOWEvidence)
		}
		if warmCOWIdentity != coldIdentity {
			t.Fatalf("preprovisioned COW identity=%s cold identity=%s", warmCOWIdentity, coldIdentity)
		}
	}
}

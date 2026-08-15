package e2e_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"sync/atomic"
	"testing"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/agentfunction"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/receipt"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

func TestRealGuestProgrammaticReceiptBindsExactVerifiedSourceSpan(t *testing.T) {
	wasm, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	artifactDigest := sha256.Sum256(wasm)
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

	var handlerCalls atomic.Uint32
	capabilityPlan := programmaticPlan(t, nil, &handlerCalls, 1)
	source := "result = tools.increment(8)\n"
	analysisConfig := runtimeconfig.DefaultRunConfig()
	analysisConfig.ExecutionProfile = &profile
	analysisConfig.Mechanisms.SemanticAnalysis = true
	analysisRunner, err := (wazeroengine.Factory{}).New(context.Background(), wasm, analysisConfig)
	if err != nil {
		t.Fatal(err)
	}
	bindings := semantic.Bindings{
		ArtifactSHA256: artifactSHA, ExecutionProfileSHA256: analysisRunner.Properties().ExecutionProfileBindingSHA256,
		ImportClosureSHA256: agentfunction.ImportClosureIdentity([]string{"json"}, []string{"json"}), CapabilityPlanSHA256: capabilityPlan.Identity(),
	}
	analysisRequest, err := semantic.NewRequest(source, bindings, capabilityPlan)
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
	if err != nil || len(analysis.CallSites) != 1 {
		t.Fatalf("analysis=%+v err=%v", analysis, err)
	}
	planned, err := semantic.BuildSourceBoundPlan(verified, capabilityPlan, semantic.PlannerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := semantic.NewSourceBindingResolver(planned)
	if err != nil {
		t.Fatal(err)
	}

	const parent = "source-bound-parent"
	presentation, err := capabilityPlan.Present(capability.ProgramSurfaceProgrammatic, parent)
	if err != nil {
		t.Fatal(err)
	}
	var broker *capability.Broker
	executionConfig := runtimeconfig.DefaultRunConfig()
	executionConfig.ExecutionProfile = &profile
	executionConfig.ProgramSurface = runtimeconfig.ProgramSurfaceProgrammatic
	executionConfig.Mechanisms.ProgrammaticToolCalling = true
	executionRunner, err := (wazeroengine.Factory{BrokerFactory: func(context.Context) (*capability.Broker, error) {
		created, createErr := capability.NewBroker(capability.Config{
			RunIdentity: "source-bound-run", Plan: capabilityPlan, ProgrammaticParentCallID: parent, SourceResolver: resolver,
		})
		broker = created
		return created, createErr
	}}).New(context.Background(), wasm, executionConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer executionRunner.Close(context.Background())

	runRequest, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{RunID: "source-bound-run", Code: source, Inputs: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := executionRunner.Run(context.Background(), runRequest, presentation.PythonPrelude)
	if err != nil {
		t.Fatal(err)
	}
	response := decodeRealGuestResponse(t, runRequest, payload)
	if string(response.Result) != `9` {
		t.Fatalf("response=%+v payload=%s", response, payload)
	}
	receipts := broker.SnapshotReceipts()
	site := analysis.CallSites[0]
	if handlerCalls.Load() != 1 || len(receipts) != 1 || receipts[0].Source == nil || !receipt.ValidIdentity(receipts[0]) {
		t.Fatalf("calls=%d receipts=%#v", handlerCalls.Load(), receipts)
	}
	bound := *receipts[0].Source
	if bound.ClaimLevel != receipt.SourceClaimBound || bound.SourceSHA256 != analysis.SourceSHA256 || bound.OccurrenceID != site.ID ||
		bound.DynamicOccurrence != site.DynamicOccurrence || bound.StartLine != site.Span.StartLine || bound.StartColumn != site.Span.StartColumn ||
		bound.EndLine != site.Span.EndLine || bound.EndColumn != site.Span.EndColumn {
		t.Fatalf("binding=%+v site=%+v", bound, site)
	}
}

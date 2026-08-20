package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/semanticspeculation"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/playback"
)

func TestExactGuestScheduledSemanticPreDispatchConsumesPreparedExternalRead(t *testing.T) {
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	artifactSHA := testDigestBytes(artifact)
	profile, err := runtimeconfig.NewExecutionProfile("base", []string{"json"})
	if err != nil {
		t.Fatal(err)
	}
	profile, err = profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{
		ProfileID: "base", ArtifactSHA256: artifactSHA, ManifestSHA256: testDigest("phase3-semantic-manifest"),
		ImportRoots: []string{"json"}, QualifiedImportRoots: []string{"json"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var physical atomic.Uint32
	var providerNanos atomic.Uint64
	plan := eagerComparatorCapabilityPlan(t, capability.HandlerFunc(func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		started := time.Now()
		defer func() { providerNanos.Add(uint64(time.Since(started))) }()
		physical.Add(1)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
		return json.RawMessage(`{"value":"weather"}`), nil
	}))
	runConfig := runtimeconfig.DefaultRunConfig()
	runConfig.ExecutionProfile = &profile
	treatment, err := semanticspeculation.NewSemanticPreDispatchTreatment(semanticspeculation.SemanticPreDispatchTreatmentConfig{
		Artifact: artifact, RunConfig: runConfig, Plan: plan,
		ProviderObservation: func() semanticspeculation.ProviderObservation {
			attempts := physical.Load()
			return semanticspeculation.ProviderObservation{
				Attempts: attempts, ResultBytes: uint64(attempts) * uint64(len(`{"value":"weather"}`)),
				CostUnits: uint64(attempts), ElapsedNanos: providerNanos.Load(),
			}
		},
		ImportClosureSHA256: testDigest("phase3-semantic-imports"), PhysicalReadBudget: 1,
		RunID: "phase3-semantic-external-read", WorkspaceRoot: t.TempDir(), WorkspaceOwner: "phase3-semantic-external-read",
	})
	if err != nil {
		t.Fatal(err)
	}
	caseValue := semanticspeculation.Phase3SyntheticCases()[2]
	result, err := semanticspeculation.RunScheduledTreatment(context.Background(), caseValue, treatment)
	if err != nil {
		t.Fatal(err)
	}
	want, err := playback.CanonicalSHA256(json.RawMessage(`{"tail":"done","value":"weather"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome.FinalProgramOutcome != "success" || !result.Outcome.FinalPythonStarted || result.Outcome.ResultSHA256 != want ||
		result.Outcome.LogicalCalls != 1 || result.Outcome.PhysicalAttempts != 1 || physical.Load() != 1 ||
		result.Outcome.PhysicalDispositions.Consumed != 1 || result.Outcome.AuthorityDisposition != "read_consumed" ||
		result.Outcome.WorkspaceDisposition != "published" || result.Outcome.ReadyBeforeFinalize != 0 {
		t.Fatalf("result=%+v physical=%d", result, physical.Load())
	}
	assertColdSemanticLifecycle(t, treatment.LifecycleEvidence(), uint32(len(caseValue.Chunks)), 1, true)
}

func TestExactGuestScheduledSemanticPreDispatchPureLocalSkipsAllAnalyzerInvocations(t *testing.T) {
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	profile, err := runtimeconfig.NewExecutionProfile("base", []string{"json"})
	if err != nil {
		t.Fatal(err)
	}
	profile, err = profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{
		ProfileID: "base", ArtifactSHA256: testDigestBytes(artifact), ManifestSHA256: testDigest("phase4-lifecycle-manifest"),
		ImportRoots: []string{"json"}, QualifiedImportRoots: []string{"json"},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := eagerComparatorCapabilityPlan(t, capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return nil, errors.New("pure-local lifecycle probe unexpectedly called capability")
	}))
	runConfig := runtimeconfig.DefaultRunConfig()
	runConfig.ExecutionProfile = &profile
	treatment, err := semanticspeculation.NewSemanticPreDispatchTreatment(semanticspeculation.SemanticPreDispatchTreatmentConfig{
		Artifact: artifact, RunConfig: runConfig, Plan: plan,
		ImportClosureSHA256: testDigest("phase4-lifecycle-imports"), PhysicalReadBudget: 1,
		RunID: "phase4-two-chunk-lifecycle", WorkspaceRoot: t.TempDir(), WorkspaceOwner: "phase4-two-chunk-lifecycle",
	})
	if err != nil {
		t.Fatal(err)
	}
	caseValue := semanticspeculation.Phase3SyntheticCases()[5]
	result, err := semanticspeculation.RunScheduledTreatment(context.Background(), caseValue, treatment)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome.FinalProgramOutcome != "success" || result.Outcome.LogicalCalls != 0 {
		t.Fatalf("result=%+v", result)
	}
	assertColdSemanticLifecycle(t, treatment.LifecycleEvidence(), uint32(len(caseValue.Chunks)), 0, false)
}

func assertColdSemanticLifecycle(t *testing.T, lifecycle semanticspeculation.SemanticTreatmentLifecycleEvidence, visible, analyzed uint32, expectProvider bool) {
	t.Helper()
	if lifecycle.SchemaVersion != "pysolate.semantic-treatment-lifecycle.v2" ||
		lifecycle.Analyzer.Invocations != analyzed || lifecycle.Analyzer.ModuleInstantiations != analyzed ||
		lifecycle.Analyzer.InitializeCalls != analyzed || lifecycle.Analyzer.RuntimeInitCalls != analyzed ||
		lifecycle.Analyzer.Successes != analyzed || lifecycle.Analyzer.Failures != 0 ||
		lifecycle.VisiblePrefixes != visible || lifecycle.SkippedPrefixes != visible-analyzed ||
		lifecycle.BeginNanos == 0 || lifecycle.AnalyzerEngineNanos == 0 || lifecycle.WorkspaceSetupNanos == 0 ||
		lifecycle.FormalEngineNanos == 0 || lifecycle.SourceGenerationNanos == 0 ||
		lifecycle.FormalGuestExecutions != 1 || lifecycle.FormalExecutionNanos == 0 {
		t.Fatalf("lifecycle=%+v", lifecycle)
	}
	phaseNanos := lifecycle.Analyzer.InstantiateNanos | lifecycle.Analyzer.InitializeNanos | lifecycle.Analyzer.RuntimeInitNanos | lifecycle.Analyzer.AnalyzeNanos
	if (phaseNanos > 0) != (analyzed > 0) {
		t.Fatalf("analyzer phase lifecycle=%+v analyzed=%d", lifecycle, analyzed)
	}
	if (lifecycle.ProviderNanos > 0) != expectProvider {
		t.Fatalf("provider lifecycle=%+v expectProvider=%t", lifecycle, expectProvider)
	}
	t.Logf("body-free lifecycle: %+v", lifecycle)
}

func TestExactGuestScheduledSemanticPreDispatchProjectsRuntimeFailureAfterConsumedRead(t *testing.T) {
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	artifactSHA := testDigestBytes(artifact)
	profile, err := runtimeconfig.NewExecutionProfile("base", []string{"json"})
	if err != nil {
		t.Fatal(err)
	}
	profile, err = profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{
		ProfileID: "base", ArtifactSHA256: artifactSHA, ManifestSHA256: testDigest("phase3-semantic-manifest"),
		ImportRoots: []string{"json"}, QualifiedImportRoots: []string{"json"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var physical atomic.Uint32
	plan := eagerComparatorCapabilityPlan(t, capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		physical.Add(1)
		return json.RawMessage(`{"value":"weather"}`), nil
	}))
	runConfig := runtimeconfig.DefaultRunConfig()
	runConfig.ExecutionProfile = &profile
	treatment, err := semanticspeculation.NewSemanticPreDispatchTreatment(semanticspeculation.SemanticPreDispatchTreatmentConfig{
		Artifact: artifact, RunConfig: runConfig, Plan: plan,
		ImportClosureSHA256: testDigest("phase3-semantic-imports"), PhysicalReadBudget: 1,
		RunID: "phase3-semantic-runtime-error", WorkspaceRoot: t.TempDir(), WorkspaceOwner: "phase3-semantic-runtime-error",
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := semanticspeculation.RunScheduledTreatment(context.Background(), semanticspeculation.Phase3SyntheticCases()[3], treatment)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome.FinalProgramOutcome != "runtime_error" || !result.Outcome.FinalPythonStarted || result.Outcome.ErrorClass != "RuntimeError" ||
		result.Outcome.ResultSHA256 != "" || result.Outcome.LogicalCalls != 1 || physical.Load() != 1 ||
		result.Outcome.AuthorityDisposition != "read_consumed" || result.Outcome.WorkspaceDisposition != "discarded" {
		t.Fatalf("result=%+v physical=%d", result, physical.Load())
	}
}

func TestExactGuestScheduledSemanticPreDispatchRejectsFinalSyntaxBeforePythonAndLogicalCall(t *testing.T) {
	artifact, err := os.ReadFile(guestArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	artifactSHA := testDigestBytes(artifact)
	profile, err := runtimeconfig.NewExecutionProfile("base", []string{"json"})
	if err != nil {
		t.Fatal(err)
	}
	profile, err = profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{
		ProfileID: "base", ArtifactSHA256: artifactSHA, ManifestSHA256: testDigest("phase3-semantic-manifest"),
		ImportRoots: []string{"json"}, QualifiedImportRoots: []string{"json"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var physical atomic.Uint32
	plan := eagerComparatorCapabilityPlan(t, capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		physical.Add(1)
		return json.RawMessage(`{"value":"weather"}`), nil
	}))
	runConfig := runtimeconfig.DefaultRunConfig()
	runConfig.ExecutionProfile = &profile
	treatment, err := semanticspeculation.NewSemanticPreDispatchTreatment(semanticspeculation.SemanticPreDispatchTreatmentConfig{
		Artifact: artifact, RunConfig: runConfig, Plan: plan,
		ImportClosureSHA256: testDigest("phase3-semantic-imports"), PhysicalReadBudget: 1,
		RunID: "phase3-semantic-syntax", WorkspaceRoot: t.TempDir(), WorkspaceOwner: "phase3-semantic-syntax",
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := semanticspeculation.RunScheduledTreatment(context.Background(), semanticspeculation.Phase3SyntheticCases()[4], treatment)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome.FinalProgramOutcome != "syntax_error" || result.Outcome.FinalPythonStarted || result.Outcome.ErrorClass != "syntax_error" ||
		result.Outcome.LogicalCalls != 0 || result.Outcome.AuthorityDisposition != "unchanged" ||
		result.Outcome.WorkspaceDisposition != "discarded" || result.Outcome.PhysicalDispositions.Consumed != 0 {
		t.Fatalf("result=%+v physical=%d", result, physical.Load())
	}
}

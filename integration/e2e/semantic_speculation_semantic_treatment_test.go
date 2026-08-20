package e2e_test

import (
	"context"
	"encoding/json"
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
	plan := eagerComparatorCapabilityPlan(t, capability.HandlerFunc(func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
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
}

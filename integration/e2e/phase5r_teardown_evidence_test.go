package e2e_test

import (
	"context"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/semanticspeculation"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
)

func TestExactGuestPhase5RTeardownProjectsReadyCapsuleDiscard(t *testing.T) {
	artifact, profile := loadPreparedRegionArtifact(t)
	config := runtimeconfig.DefaultRunConfig()
	config.ExecutionProfile = &profile
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	operations, err := semanticspeculation.NewPhase5ExactGuestOperations(artifact, config)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []semanticspeculation.Phase5CapacityKind{
		semanticspeculation.Phase5AnalyzerCapacity,
		semanticspeculation.Phase5ScratchCapacity,
		semanticspeculation.Phase5FinalCapacity,
	} {
		if err := operations.Provision(ctx, kind); err != nil {
			t.Fatal(err)
		}
	}
	pilot := semanticspeculation.Phase5Cases()[0]
	input := semanticspeculation.Phase5ExecutionInput{Source: pilot.Source, FocusRegionIndex: pilot.FocusRegionIndex, OutputName: pilot.OutputName}
	if err := operations.Analyze(ctx, input); err != nil {
		t.Fatal(err)
	}
	if err := operations.EmitPatch(ctx, input); err != nil {
		t.Fatal(err)
	}
	if err := operations.ExecuteScratch(ctx, input); err != nil {
		t.Fatal(err)
	}
	if err := operations.SealCapsule(ctx); err != nil {
		t.Fatal(err)
	}
	if err := operations.Teardown(ctx); err != nil {
		t.Fatal(err)
	}
	snapshot := operations.Snapshot()
	if snapshot.HelperClaimCount != 0 || snapshot.CapsuleConsumedCount != 0 || snapshot.CapsuleDiscardedCount != 1 {
		t.Fatalf("post-teardown capsule evidence drift: %+v", snapshot)
	}
}

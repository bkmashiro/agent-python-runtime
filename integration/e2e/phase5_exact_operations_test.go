package e2e_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/semanticspeculation"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
)

func TestExactGuestPhase5OriginalOperationsExcludedPilot(t *testing.T) {
	artifact, profile := loadPreparedRegionArtifact(t)
	config := runtimeconfig.DefaultRunConfig()
	config.ExecutionProfile = &profile
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	operations, err := semanticspeculation.NewPhase5ExactGuestOperations(artifact, config)
	if err != nil {
		t.Fatal(err)
	}
	pilot := semanticspeculation.Phase5Cases()[0]
	input := semanticspeculation.Phase5ExecutionInput{Source: pilot.Source, FocusRegionIndex: pilot.FocusRegionIndex, OutputName: pilot.OutputName}
	if err := operations.Provision(ctx, semanticspeculation.Phase5FinalCapacity); err != nil {
		t.Fatal(err)
	}
	gap, err := operations.BeginFinalizationGap(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := gap.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if err := operations.ExecuteOriginal(ctx, input); err != nil {
		t.Fatal(err)
	}
	if err := operations.Analyze(ctx, input); !errors.Is(err, semanticspeculation.ErrPhase5DerivedOperationsUnavailable) {
		t.Fatalf("derived operation did not fail closed: %v", err)
	}
	if err := operations.Teardown(ctx); err != nil {
		t.Fatal(err)
	}
	snapshot := operations.Snapshot()
	digest := sha256.Sum256(pilot.ExpectedResult)
	if snapshot.ActualDisposition != pilot.ExpectedDisposition || snapshot.ActualOutcome != pilot.ExpectedOutcome || snapshot.ResultSHA256 != "sha256:"+hex.EncodeToString(digest[:]) {
		t.Fatalf("pilot outcome drift: %+v", snapshot)
	}
	if snapshot.FormalGuestExecutions != 1 || snapshot.FinalRuntimeInitCount != 1 || snapshot.AnalyzerSessionCount != 0 || snapshot.ScratchGuestExecutions != 0 || snapshot.HelperClaimCount != 0 || snapshot.AuthorityTerminalDisposition != "none" || snapshot.WorkspaceTerminalDisposition != "unmounted" {
		t.Fatalf("pilot lifecycle drift: %+v", snapshot)
	}
	if snapshot.DecisionSHA256 != "" || snapshot.PatchSHA256 != "" || snapshot.CapsuleSHA256 != "" || snapshot.SelectionSHA256 != "" || snapshot.DerivedASTSHA256 != "" {
		t.Fatalf("original pilot leaked derived identities: %+v", snapshot)
	}

	derived, err := semanticspeculation.NewPhase5ExactGuestOperations(artifact, config)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []semanticspeculation.Phase5CapacityKind{semanticspeculation.Phase5AnalyzerCapacity, semanticspeculation.Phase5ScratchCapacity, semanticspeculation.Phase5FinalCapacity} {
		if err := derived.Provision(ctx, kind); err != nil {
			t.Fatalf("provision %s: %v", kind, err)
		}
	}
	derivedSnapshot := derived.Snapshot()
	if derivedSnapshot.AnalyzerSessionCount != 1 || derivedSnapshot.AnalyzerRuntimeInitCount != 1 || derivedSnapshot.ScratchRuntimeInitCount != 1 || derivedSnapshot.FinalRuntimeInitCount != 1 || derivedSnapshot.ScratchGuestExecutions != 0 || derivedSnapshot.FormalGuestExecutions != 0 {
		t.Fatalf("derived capacities were served during provisioning: %+v", derivedSnapshot)
	}
	if err := derived.Analyze(ctx, input); err != nil {
		t.Fatal(err)
	}
	if err := derived.EmitPatch(ctx, input); err != nil {
		t.Fatal(err)
	}
	if patched := derived.Snapshot(); patched.DecisionSHA256 == "" || patched.PatchSHA256 == "" {
		t.Fatalf("derived patch identities missing: %+v", patched)
	}
	if err := derived.ExecuteScratch(ctx, input); err != nil {
		t.Fatal(err)
	}
	if err := derived.SealCapsule(ctx); err != nil {
		t.Fatal(err)
	}
	if sealed := derived.Snapshot(); sealed.CapsuleSHA256 == "" || sealed.CapsuleBytes == 0 || sealed.CapsuleBytes > 256 || sealed.ScratchGuestExecutions != 1 {
		t.Fatalf("derived capsule lifecycle drift: %+v", sealed)
	}
	if err := derived.Analyze(ctx, input); err == nil {
		t.Fatal("derived analyzer accepted a second request")
	}
	derivedSnapshot = derived.Snapshot()
	if derivedSnapshot.FormalGuestExecutions != 0 || derivedSnapshot.HelperClaimCount != 0 {
		t.Fatalf("analysis/scratch executed formal source or claimed capsule: %+v", derivedSnapshot)
	}
	if err := derived.Teardown(ctx); err != nil {
		t.Fatal(err)
	}
}

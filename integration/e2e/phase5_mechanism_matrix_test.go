package e2e_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/semanticspeculation"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
)

// TestExactGuestPhase5FrozenMechanismMatrixRecordsNoGo preserves the
// observation-free mechanism gate result for the immutable matrix/artifact.
// Passing this test means the exact preregistered failures were reproduced;
// it does not mean the Phase 5 mechanism gate passed.
func TestExactGuestPhase5FrozenMechanismMatrixRecordsNoGo(t *testing.T) {
	artifact, profile := loadPreparedRegionArtifact(t)
	config := runtimeconfig.DefaultRunConfig()
	config.ExecutionProfile = &profile
	for _, candidate := range semanticspeculation.Phase5Cases() {
		candidate := candidate
		t.Run(candidate.ID, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
			defer cancel()
			input := semanticspeculation.Phase5ExecutionInput{Source: candidate.Source, FocusRegionIndex: candidate.FocusRegionIndex, OutputName: candidate.OutputName}
			original := newPhase5ExactOperations(t, artifact, config)
			if err := original.Provision(ctx, semanticspeculation.Phase5FinalCapacity); err != nil {
				t.Fatal(err)
			}
			originalErr := original.ExecuteOriginal(ctx, input)
			if candidate.ID == "scalar_add_512_gap6000" {
				if originalErr == nil {
					t.Fatal("frozen 512-operator control unexpectedly passed immutable artifact")
				}
				if err := original.Teardown(ctx); err != nil {
					t.Fatal(err)
				}
				return
			}
			if originalErr != nil {
				t.Fatal(originalErr)
			}
			if err := original.Teardown(ctx); err != nil {
				t.Fatal(err)
			}
			baseline := original.Snapshot()

			derived := newPhase5ExactOperations(t, artifact, config)
			for _, kind := range []semanticspeculation.Phase5CapacityKind{semanticspeculation.Phase5AnalyzerCapacity, semanticspeculation.Phase5ScratchCapacity, semanticspeculation.Phase5FinalCapacity} {
				if err := derived.Provision(ctx, kind); err != nil {
					t.Fatal(err)
				}
			}
			analyzeErr := derived.Analyze(ctx, input)
			switch candidate.ID {
			case "scalar_unsafe_call":
				if analyzeErr == nil {
					t.Fatal("unsafe RHS admitted")
				}
				phase5AssertTerminalControl(t, ctx, derived, 0, 0, 0)
				return
			}
			if analyzeErr != nil {
				t.Fatal(analyzeErr)
			}
			emitErr := derived.EmitPatch(ctx, input)
			if candidate.ID == "scalar_multiply_256_gap1000" {
				if emitErr == nil {
					t.Fatal("frozen 256-operator control unexpectedly emitted a patch")
				}
				phase5AssertTerminalControl(t, ctx, derived, 0, 0, 0)
				return
			}
			if emitErr != nil {
				t.Fatal(emitErr)
			}
			if err := derived.ExecuteScratch(ctx, input); err != nil {
				t.Fatal(err)
			}
			if candidate.ID == "scalar_int64_overflow" {
				if err := derived.SealCapsule(ctx); err == nil {
					t.Fatal("overflow published capsule")
				}
				phase5AssertTerminalControl(t, ctx, derived, 1, 0, 0)
				return
			}
			if err := derived.SealCapsule(ctx); err != nil {
				t.Fatal(err)
			}
			if candidate.ID == "derived_suffix_drift" {
				drift := input
				drift.Source += "this is not valid Python\n"
				if err := derived.ValidateSelection(ctx, drift); err == nil {
					t.Fatal("suffix drift selected")
				}
				phase5AssertTerminalControl(t, ctx, derived, 1, 0, 0)
				return
			}
			if err := derived.ValidateSelection(ctx, input); err != nil {
				t.Fatal(err)
			}
			if err := derived.CompileDerived(ctx, input); err != nil {
				t.Fatal(err)
			}
			if candidate.ID == "pre_cancelled_final_execution" {
				cancelled, stop := context.WithCancel(ctx)
				stop()
				if err := derived.ExecuteDerived(cancelled, input); !errors.Is(err, context.Canceled) {
					t.Fatalf("cancel err=%v", err)
				}
				phase5AssertTerminalControl(t, ctx, derived, 1, 0, 0)
				return
			}
			if err := derived.ExecuteDerived(ctx, input); err != nil {
				t.Fatal(err)
			}
			if err := derived.Teardown(ctx); err != nil {
				t.Fatal(err)
			}
			actual := derived.Snapshot()
			if actual.ActualOutcome != baseline.ActualOutcome || actual.ResultSHA256 != baseline.ResultSHA256 || actual.ErrorClass != baseline.ErrorClass || actual.ErrorMessageSHA256 != baseline.ErrorMessageSHA256 || actual.TracebackSHA256 != baseline.TracebackSHA256 || actual.LogsSHA256 != baseline.LogsSHA256 {
				t.Fatalf("parity drift: baseline=%+v derived=%+v", baseline, actual)
			}
			expectedClaims := uint32(1)
			if candidate.ID == "exception_before_region" {
				expectedClaims = 0
			}
			if actual.HelperClaimCount != expectedClaims || actual.CapsuleConsumedCount != expectedClaims || actual.CapsuleDiscardedCount != 0 || actual.FormalGuestExecutions != 2 {
				t.Fatalf("consumed lifecycle drift: %+v", actual)
			}
		})
	}
}

func newPhase5ExactOperations(t *testing.T, artifact []byte, config runtimeconfig.RunConfig) *semanticspeculation.Phase5ExactGuestOperations {
	t.Helper()
	operations, err := semanticspeculation.NewPhase5ExactGuestOperations(artifact, config)
	if err != nil {
		t.Fatal(err)
	}
	return operations
}

func phase5AssertTerminalControl(t *testing.T, ctx context.Context, operations *semanticspeculation.Phase5ExactGuestOperations, scratch, consumed, discarded uint32) {
	t.Helper()
	if err := operations.Teardown(ctx); err != nil {
		t.Fatal(err)
	}
	snapshot := operations.Snapshot()
	if snapshot.ScratchGuestExecutions != scratch || snapshot.CapsuleConsumedCount != consumed || snapshot.CapsuleDiscardedCount != discarded || snapshot.FormalGuestExecutions != 0 || snapshot.HelperClaimCount != 0 || snapshot.AuthorityTerminalDisposition != "none" || snapshot.WorkspaceTerminalDisposition != "unmounted" {
		t.Fatalf("terminal control drift: %+v", snapshot)
	}
}

package e2e_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/semanticspeculation"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
)

func TestExactGuestPhase5RMechanismMatrix(t *testing.T) {
	artifact, profile := loadPreparedRegionArtifact(t)
	config := runtimeconfig.DefaultRunConfig()
	config.ExecutionProfile = &profile
	// Keep every Guest operation bounded while allowing the preregistered
	// 120-second case envelope to survive a loaded Linux workstation.
	config.Timeout = 60 * time.Second
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
			if err := original.ExecuteOriginal(ctx, input); err != nil {
				t.Fatal(err)
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
			if candidate.ID == "scalar_unsafe_call" {
				if analyzeErr == nil {
					t.Fatal("unsafe RHS admitted")
				}
				phase5AssertTerminalControl(t, ctx, derived, 0, 0, 0)
				return
			}
			if analyzeErr != nil {
				t.Fatal(analyzeErr)
			}
			if err := derived.EmitPatch(ctx, input); err != nil {
				t.Fatal(err)
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
				phase5AssertTerminalControl(t, ctx, derived, 1, 0, 1)
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
				phase5AssertTerminalControl(t, ctx, derived, 1, 0, 1)
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
			expectedClaims, expectedDiscarded := uint32(1), uint32(0)
			if candidate.ID == "exception_before_region" {
				expectedClaims, expectedDiscarded = 0, 1
			}
			if actual.HelperClaimCount != expectedClaims || actual.CapsuleConsumedCount != expectedClaims || actual.CapsuleDiscardedCount != expectedDiscarded || actual.FormalGuestExecutions != 2 {
				t.Fatalf("terminal lifecycle drift: %+v", actual)
			}
		})
	}
}

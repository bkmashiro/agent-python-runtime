package semantic

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/passpipeline"
)

func TestRecordSemanticPreDispatchPassOutcomesMapsConsumedAndOrphanedPhysicalWork(t *testing.T) {
	for name, consume := range map[string]bool{"consumed": true, "orphaned": false} {
		t.Run(name, func(t *testing.T) {
			controller, verified, registration, pipeline, finalSource := semanticPassOutcomeFixture(t)
			if consume {
				outcome, err := controller.Claim(context.Background(), "sources.read", json.RawMessage(`{"key":"profile"}`))
				if err != nil || outcome.Validate() != nil {
					t.Fatalf("claim=%+v err=%v", outcome, err)
				}
			}
			if err := controller.Finalize(true); err != nil {
				t.Fatal(err)
			}
			records, err := RecordSemanticPreDispatchPassOutcomes(pipeline, registration, verified, controller, uint64(len(finalSource)), 2)
			if err != nil || len(records) != 1 {
				t.Fatalf("records=%+v err=%v", records, err)
			}
			record := records[0]
			if record.Stage != passpipeline.StagePrefixOverlay || record.OriginalSourceSHA256 != digestText(finalSource) ||
				record.OriginalASTSHA256 == "" || record.DerivedSourceSHA256 != "" || record.DerivedASTSHA256 != "" ||
				record.PhysicalEvents != 1 || record.WorkspaceDisposition != "not_owned" {
				t.Fatalf("record=%+v", record)
			}
			if consume {
				if record.Outcome != passpipeline.OutcomeApplied || record.LogicalEvents != 1 || record.RejectionReason != "" {
					t.Fatalf("consumed record=%+v", record)
				}
			} else if record.Outcome != passpipeline.OutcomeDiscarded || record.LogicalEvents != 0 || record.RejectionReason != "orphaned" {
				t.Fatalf("orphan record=%+v", record)
			}
			if _, err := RecordSemanticPreDispatchPassOutcomes(pipeline, registration, verified, controller, uint64(len(finalSource)), 2); !errors.Is(err, passpipeline.ErrDuplicateOutcome) {
				t.Fatalf("duplicate projection err=%v", err)
			}
			if len(pipeline.Records()) != 1 {
				t.Fatalf("duplicate projection records=%d", len(pipeline.Records()))
			}
		})
	}
}

func TestRecordSemanticPreDispatchPassOutcomesRejectsUnsealedUnfinalizedAndRegistrationDrift(t *testing.T) {
	controller, verified, registration, pipeline, finalSource := semanticPassOutcomeFixture(t)
	if _, err := RecordSemanticPreDispatchPassOutcomes(pipeline, registration, verified, controller, uint64(len(finalSource)), 1); !errors.Is(err, ErrPassOutcomeInvalid) {
		t.Fatalf("unfinalized err=%v", err)
	}
	if err := controller.Finalize(true); err != nil {
		t.Fatal(err)
	}
	analysis, err := verified.Analysis()
	if err != nil {
		t.Fatal(err)
	}
	drifted, err := NewPassRegistration(PassSemanticPreDispatch, SemanticPreDispatchPassVersion, analysis.AnalyzerSHA256, legalityDigest("different-config"), PassConsumerOverlayOnly, SemanticPreDispatchBindings())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RecordSemanticPreDispatchPassOutcomes(pipeline, drifted, verified, controller, uint64(len(finalSource)), 1); !errors.Is(err, ErrPassOutcomeInvalid) {
		t.Fatalf("registration drift err=%v", err)
	}
}

func semanticPassOutcomeFixture(t *testing.T) (*StreamingSemanticPreDispatch, VerifiedAnalysis, PassRegistration, *passpipeline.Pipeline, string) {
	t.Helper()
	plan := legalityTestPlan(t, true)
	prefixSource := "result = sources.read(\"profile\")\n"
	prefixVerified, _ := streamingPrefixAnalysis(t, plan, prefixSource)
	budget, err := NewPreDispatchBudget(1)
	if err != nil {
		t.Fatal(err)
	}
	launcher := &queuedLauncher{}
	controller, err := NewStreamingSemanticPreDispatch(plan, budget, launcher)
	if err != nil {
		t.Fatal(err)
	}
	admission, err := NewStreamingPrefixAdmission(plan, controller, legalityContext())
	if err != nil {
		t.Fatal(err)
	}
	if added, err := admission.AdmitVerifiedPrefix(context.Background(), prefixSource, prefixVerified); err != nil || added != 1 {
		t.Fatalf("admit=%d err=%v", added, err)
	}
	launcher.RunAll()
	finalSource := prefixSource + "answer = result\n"
	if err := admission.SealFinalSource(finalSource); err != nil {
		t.Fatal(err)
	}
	analysis, err := prefixVerified.Analysis()
	if err != nil {
		t.Fatal(err)
	}
	analysis.SourceSHA256 = digestText(finalSource)
	_, analysisJSON, err := analysis.Identity()
	if err != nil {
		t.Fatal(err)
	}
	verified := VerifiedAnalysis{analysisJSON: analysisJSON}
	registration, err := NewPassRegistration(
		PassSemanticPreDispatch, SemanticPreDispatchPassVersion,
		analysis.AnalyzerSHA256, legalityDigest("streaming-pass-outcome-config"),
		PassConsumerOverlayOnly, SemanticPreDispatchBindings(),
	)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := passpipeline.CurrentEntry(registration, true)
	if err != nil {
		t.Fatal(err)
	}
	pipeline, err := passpipeline.New([]passpipeline.Entry{entry}, passpipeline.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return controller, verified, registration, pipeline, finalSource
}

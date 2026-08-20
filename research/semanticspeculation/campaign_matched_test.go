package semanticspeculation

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type fixedScheduledTreatment struct{ outcome TreatmentOutcome }

func (t *fixedScheduledTreatment) Begin(context.Context, json.RawMessage) error { return nil }
func (t *fixedScheduledTreatment) ObserveChunk(context.Context, string) error   { return nil }
func (t *fixedScheduledTreatment) Finalize(context.Context) (TreatmentOutcome, error) {
	return t.outcome, nil
}
func (t *fixedScheduledTreatment) Cancel(context.Context) error { return nil }

func TestRunMatchedCaseCampaignSealsAndAggregatesThreeAchievedLanes(t *testing.T) {
	fixture := Phase3SyntheticCases()[5]
	bindings := matchedTestBindings()
	resultSHA := syntheticDigest([]byte("result"))
	factoryCalls := 0
	var executionOrder []string
	result, err := RunMatchedCaseCampaign(context.Background(), fixture, 1, bindings,
		func(treatment string, trialIndex uint32) (ScheduledTreatment, error) {
			factoryCalls++
			executionOrder = append(executionOrder, treatment)
			if trialIndex != 1 {
				t.Fatal("unexpected trial index")
			}
			outcome := TreatmentOutcome{
				FinalProgramOutcome: "success", FinalPythonStarted: true, ResultSHA256: resultSHA,
				AuthorityDisposition: "unchanged", WorkspaceDisposition: "published",
			}
			if treatment == "eager_style_gate" {
				outcome.PrefixPythonExecutions = 1
			}
			return &fixedScheduledTreatment{outcome: outcome}, nil
		},
		func(serial TrialRecord) (uint64, error) { return trialElapsedNanos(serial) - 1, nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if factoryCalls != 3 || len(result.Records) != 3 || !result.Aggregate.OracleExcludedFromAchievedSpeedup || result.Oracle.ElapsedNanos == 0 {
		t.Fatalf("result=%+v calls=%d", result, factoryCalls)
	}
	expectedOrder := matchedTreatmentOrder(fixture.ID, 1)
	for index := range expectedOrder {
		if executionOrder[index] != expectedOrder[index] || result.Records[index].Treatment != expectedOrder[index] {
			t.Fatalf("execution_order=%v records=%+v", executionOrder, result.Records)
		}
	}
	for _, record := range result.Records {
		if record.Identity == "" || record.CaseID != fixture.ID || record.TrialIndex != 1 {
			t.Fatalf("record=%+v", record)
		}
	}
}

func TestRunMatchedCaseCampaignRejectsFixtureMismatchBeforeOracle(t *testing.T) {
	fixture := Phase3SyntheticCases()[5]
	oracleCalled := false
	_, err := RunMatchedCaseCampaign(context.Background(), fixture, 1, matchedTestBindings(),
		func(treatment string, _ uint32) (ScheduledTreatment, error) {
			outcome := TreatmentOutcome{
				FinalProgramOutcome: "success", FinalPythonStarted: true, ResultSHA256: syntheticDigest([]byte("result")),
				LogicalCalls: 1, AuthorityDisposition: "read_consumed", WorkspaceDisposition: "published",
			}
			return &fixedScheduledTreatment{outcome: outcome}, nil
		},
		func(TrialRecord) (uint64, error) { oracleCalled = true; return 1, nil },
	)
	if !errors.Is(err, ErrInvalidMatchedCampaign) || oracleCalled {
		t.Fatalf("err=%v oracle_called=%v", err, oracleCalled)
	}
}

func TestBuildScheduledTrialRecordRejectsValidButUnfrozenFixture(t *testing.T) {
	fixture := Phase3SyntheticCases()[5]
	fixture.ID = "post_hoc_case"
	result := ScheduledTreatmentResult{StartedNanos: 1, FinalizeNanos: 2, EndedNanos: 3, Outcome: TreatmentOutcome{
		FinalProgramOutcome: "success", FinalPythonStarted: true, ResultSHA256: syntheticDigest([]byte("result")),
		AuthorityDisposition: "unchanged", WorkspaceDisposition: "published",
	}}
	if _, err := BuildScheduledTrialRecord(fixture, "serial_whole_file", 1, matchedTestBindings(), result); err == nil {
		t.Fatal("post-hoc fixture was accepted under the frozen matrix identity")
	}
}

func matchedTestBindings() TrialBindings {
	return TrialBindings{
		ArtifactSHA256: syntheticDigest([]byte("artifact")), ManifestSHA256: syntheticDigest([]byte("manifest")),
		ImportInventorySHA256: syntheticDigest([]byte("imports")), ExecutionProfileSHA256: syntheticDigest([]byte("profile")),
		CapabilityPlanSHA256: syntheticDigest([]byte("plan")), PrivacySHA256: syntheticDigest([]byte("privacy")),
	}
}

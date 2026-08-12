package evaluation_test

import (
	"errors"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/evaluation"
)

func TestRowRecorderEnforcesLifecycleAndPermanentEvidenceFailure(t *testing.T) {
	planned := evaluation.PlannedRow{RowID: evaluation.RowIdentity("structured-source-v1", evaluation.TreatmentLiveCapture, 0), WorkloadID: "structured-source-v1", Treatment: evaluation.TreatmentLiveCapture, Supported: true}
	recorder, err := evaluation.NewRowRecorder(planned)
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.RecordSetup(3); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Start(); err != nil {
		t.Fatal(err)
	}
	if err := recorder.RecordExecution(8); err != nil {
		t.Fatal(err)
	}
	if err := recorder.RecordOracle(evaluation.OraclePassed, 2); err != nil {
		t.Fatal(err)
	}
	if err := recorder.RecordEvidence(false, 1, evaluation.RowMetrics{}, "required_recorder_failed"); err != nil {
		t.Fatal(err)
	}
	if err := recorder.RecordEvidence(true, 1, evaluation.RowMetrics{}, ""); !errors.Is(err, evaluation.ErrInvalid) {
		t.Fatalf("recovered evidence=%v", err)
	}
	row, err := recorder.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != evaluation.RowFailed || row.EvidenceComplete || row.ProblemCode != "required_recorder_failed" {
		t.Fatalf("row=%+v", row)
	}
}

func TestRowRecorderCompletedTimeoutAndUnsupported(t *testing.T) {
	planned := evaluation.PlannedRow{RowID: evaluation.RowIdentity("bounded-planning-v1", evaluation.TreatmentOfflineReplay, 0), WorkloadID: "bounded-planning-v1", Treatment: evaluation.TreatmentOfflineReplay, Supported: true}
	r, err := evaluation.NewRowRecorder(planned)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range []func() error{func() error { return r.RecordSetup(1) }, r.Start, func() error { return r.RecordExecution(5) }, func() error { return r.RecordOracle(evaluation.OraclePassed, 1) }, func() error { return r.RecordEvidence(true, 1, evaluation.RowMetrics{ReplayEquivalent: true}, "") }} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	row, err := r.Finalize()
	if err != nil || row.Status != evaluation.RowCompleted {
		t.Fatalf("row=%+v err=%v", row, err)
	}

	timed, _ := evaluation.NewRowRecorder(planned)
	_ = timed.RecordSetup(1)
	_ = timed.Start()
	if err := timed.Timeout(9, "wall_ceiling_exceeded"); err != nil {
		t.Fatal(err)
	}
	timedRow, err := timed.Finalize()
	if err != nil || timedRow.Status != evaluation.RowTimedOut || timedRow.OracleStatus != evaluation.OracleNotRun {
		t.Fatalf("timeout=%+v err=%v", timedRow, err)
	}

	unsupportedPlan := planned
	unsupportedPlan.Supported = false
	unsupportedPlan.UnsupportedReason = "treatment_not_admitted"
	unsupported, err := evaluation.NewRowRecorder(unsupportedPlan)
	if err != nil {
		t.Fatal(err)
	}
	unsupportedRow, err := unsupported.Finalize()
	if err != nil || unsupportedRow.Status != evaluation.RowUnsupported || unsupportedRow.Started {
		t.Fatalf("unsupported=%+v err=%v", unsupportedRow, err)
	}
}

func TestRowRecorderInvalidEvidenceInputDoesNotCommitTerminalState(t *testing.T) {
	planned := evaluation.PlannedRow{RowID: evaluation.RowIdentity("bounded-planning-v1", evaluation.TreatmentLiveCapture, 0), WorkloadID: "bounded-planning-v1", Treatment: evaluation.TreatmentLiveCapture, Supported: true}
	r, _ := evaluation.NewRowRecorder(planned)
	_ = r.RecordSetup(1)
	_ = r.Start()
	_ = r.RecordExecution(1)
	_ = r.RecordOracle(evaluation.OraclePassed, 1)
	if err := r.RecordEvidence(true, 1, evaluation.RowMetrics{}, "unexpected_problem"); !errors.Is(err, evaluation.ErrInvalid) {
		t.Fatalf("invalid evidence=%v", err)
	}
	if err := r.RecordEvidence(true, 1, evaluation.RowMetrics{}, ""); err != nil {
		t.Fatalf("valid retry=%v", err)
	}
	if row, err := r.Finalize(); err != nil || row.Status != evaluation.RowCompleted {
		t.Fatalf("row=%+v err=%v", row, err)
	}
}

func TestRowRecorderRejectsOutOfOrderAndIncompleteFinalization(t *testing.T) {
	planned := evaluation.PlannedRow{RowID: evaluation.RowIdentity("bounded-planning-v1", evaluation.TreatmentLiveCapture, 0), WorkloadID: "bounded-planning-v1", Treatment: evaluation.TreatmentLiveCapture, Supported: true}
	r, _ := evaluation.NewRowRecorder(planned)
	if err := r.Start(); !errors.Is(err, evaluation.ErrInvalid) {
		t.Fatalf("start before setup=%v", err)
	}
	if _, err := r.Finalize(); !errors.Is(err, evaluation.ErrInvalid) {
		t.Fatalf("incomplete=%v", err)
	}
}

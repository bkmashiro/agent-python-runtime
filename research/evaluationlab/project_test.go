package evaluationlab_test

import (
	"strings"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/evaluation"
	"github.com/bkmashiro/agent-python-runtime/research/evaluationlab"
	"github.com/bkmashiro/agent-python-runtime/research/labview"
)

func TestProjectStrictReportIntoHonestUnavailableLabSet(t *testing.T) {
	reportBytes, measurementBytes, report := reportFixture(t)
	projected, err := evaluationlab.Project(reportBytes, measurementBytes, report.Rows[0].RowID)
	if err != nil {
		t.Fatal(err)
	}
	if err := labview.ValidateSet(projected); err != nil {
		t.Fatalf("projection: %v", err)
	}
	if projected.Index.SourceSHA256 == "" || projected.Run.RunID != report.Rows[0].RowID || projected.Run.EvidenceCompleteness != labview.Incomplete || len(projected.Timeline.Events) != 0 || len(projected.Workspace.Changes) != 0 || len(projected.DAG.Edges) != 0 {
		t.Fatalf("dishonest projection: %+v", projected)
	}
	if projected.Problem.Code != "evidence_incomplete" || projected.Problem.Scope != "run" || len(projected.Run.ProblemCodes) != 1 || projected.Run.ProblemCodes[0] != "evidence_incomplete" {
		t.Fatalf("problem=%+v run=%+v", projected.Problem, projected.Run)
	}
	if len(projected.Run.Refs) != 7 {
		t.Fatalf("refs=%d", len(projected.Run.Refs))
	}
	seen := map[string]bool{}
	for _, ref := range projected.Run.Refs {
		if ref.Privacy != labview.PrivacyPrivate || ref.Availability != labview.AvailabilityUnavailable || seen[ref.SHA256] {
			t.Fatalf("unavailable marker ref=%+v", ref)
		}
		seen[ref.SHA256] = true
	}
	if projected.Refs.Ref != projected.Run.Refs[5] {
		t.Fatalf("object ref does not select result marker")
	}
	if len(projected.Comparison.ReasonCodes) != 1 || projected.Comparison.ReasonCodes[0] != "evidence_incomplete" || len(projected.Comparison.CallDeltas) != 0 || len(projected.Comparison.WorkspaceDeltas) != 0 {
		t.Fatalf("comparison=%+v", projected.Comparison)
	}
	for _, fixture := range []struct {
		kind  labview.Kind
		value any
	}{{labview.KindIndex, projected.Index}, {labview.KindStudySummary, projected.Study}, {labview.KindRunDetail, projected.Run}, {labview.KindTimelinePage, projected.Timeline}, {labview.KindBranchDAG, projected.DAG}, {labview.KindWorkspaceDiff, projected.Workspace}, {labview.KindRunComparison, projected.Comparison}, {labview.KindObjectRef, projected.Refs}, {labview.KindProblem, projected.Problem}} {
		body, _, err := labview.Encode(fixture.kind, fixture.value)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"/Users/", "/private/", "http://", "credential", "password", "authorization", "prompt", "agent_source"} {
			if strings.Contains(string(body), forbidden) {
				t.Fatalf("kind=%s leaked %q", fixture.kind, forbidden)
			}
		}
	}
}

func TestProjectRejectsUnknownRowAndNonCanonicalReport(t *testing.T) {
	reportBytes, measurementBytes, report := reportFixture(t)
	if _, err := evaluationlab.Project(reportBytes, measurementBytes, "missing-row"); err == nil {
		t.Fatal("missing row accepted")
	}
	if _, err := evaluationlab.Project(append(reportBytes, '\n'), measurementBytes, report.Rows[0].RowID); err == nil {
		t.Fatal("non-canonical report accepted")
	}
	measurements, _, err := evaluation.DecodeMeasurementSummary(measurementBytes)
	if err != nil {
		t.Fatal(err)
	}
	measurements.PlanSHA256 = "sha256:" + repeat('e', 64)
	drifted, _, err := evaluation.EncodeMeasurementSummary(measurements)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := evaluationlab.Project(reportBytes, drifted, report.Rows[0].RowID); err == nil {
		t.Fatal("measurement identity drift accepted")
	}
	projected, err := evaluationlab.Project(reportBytes, measurementBytes, report.Rows[0].RowID)
	if err != nil {
		t.Fatal(err)
	}
	projected.Comparison.ReasonCodes = []string{"authority_changed"}
	if _, _, err := labview.Encode(labview.KindRunComparison, projected.Comparison); err == nil {
		t.Fatal("Go validator accepted schema-forbidden reason")
	}
}

func reportFixture(t *testing.T) ([]byte, []byte, evaluation.Report) {
	digest := func(ch byte) string { return "sha256:" + string(make([]byte, 0)) + repeat(ch, 64) }
	report := evaluation.Report{
		SchemaVersion: evaluation.ReportSchemaVersion, EvidenceClass: evaluation.EvidenceMechanismOnly,
		CorpusSHA256: digest('a'), PlanSHA256: digest('b'), ProhibitedClaims: evaluation.RequiredProhibitedClaims(),
		Summary: evaluation.Summary{Offered: 2, Completed: 2},
		Rows: []evaluation.Row{
			{RowID: evaluation.RowIdentity("structured-source-v1", evaluation.TreatmentLiveCapture, 0), WorkloadID: "structured-source-v1", Treatment: evaluation.TreatmentLiveCapture, Status: evaluation.RowCompleted, OracleStatus: evaluation.OraclePassed, EvidenceComplete: true, CorpusSHA256: digest('a'), PlanSHA256: digest('b'), EvidenceRefs: []string{digest('c')}},
			{RowID: evaluation.RowIdentity("bounded-planning-v1", evaluation.TreatmentOfflineReplay, 0), WorkloadID: "bounded-planning-v1", Treatment: evaluation.TreatmentOfflineReplay, Status: evaluation.RowCompleted, OracleStatus: evaluation.OraclePassed, EvidenceComplete: true, CorpusSHA256: digest('a'), PlanSHA256: digest('b'), EvidenceRefs: []string{digest('d')}},
		},
	}
	encoded, _, err := evaluation.EncodeReport(report)
	if err != nil {
		t.Fatal(err)
	}
	measurements := evaluation.MeasurementSummary{SchemaVersion: evaluation.MeasurementSchemaVersion, EvidenceClass: evaluation.EvidenceMechanismOnly, CorpusSHA256: digest('a'), PlanSHA256: digest('b'), ProhibitedClaims: evaluation.RequiredProhibitedClaims(), Offered: 2, Started: 2, Completed: 2, OraclePassed: 2, EvidenceComplete: 2, ObjectPuts: 4, ReusedPuts: 2, LogicalBytes: 2048, StoredBytes: 4096}
	measurementBytes, _, err := evaluation.EncodeMeasurementSummary(measurements)
	if err != nil {
		t.Fatal(err)
	}
	return encoded, measurementBytes, report
}

func repeat(ch byte, n int) string {
	result := make([]byte, n)
	for i := range result {
		result[i] = ch
	}
	return string(result)
}

package evaluation_test

import (
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/evaluation"
)

func TestDeriveMeasurementSummaryUsesExplicitDenominators(t *testing.T) {
	study := evaluation.RawStudy{SchemaVersion: evaluation.RawSchemaVersion, CorpusSHA256: digestC, PlanSHA256: digestB, Rows: []evaluation.RawRow{
		{RowID: evaluation.RowIdentity("structured-source-v1", evaluation.TreatmentLiveCapture, 0), WorkloadID: "structured-source-v1", Treatment: evaluation.TreatmentLiveCapture, Started: true, Status: evaluation.RowCompleted, OracleStatus: evaluation.OraclePassed, EvidenceComplete: true, Metrics: evaluation.RowMetrics{ObjectCount: 2, ReusedObjectCount: 1, LogicalBytes: 100, StoredBytes: 140}},
		{RowID: evaluation.RowIdentity("structured-source-v1", evaluation.TreatmentOfflineReplay, 0), WorkloadID: "structured-source-v1", Treatment: evaluation.TreatmentOfflineReplay, Started: true, Status: evaluation.RowCompleted, OracleStatus: evaluation.OraclePassed, EvidenceComplete: true, Metrics: evaluation.RowMetrics{ReplayChecked: true, ReplayEquivalent: true, ObjectCount: 2, ReusedObjectCount: 2, LogicalBytes: 100}},
		{RowID: evaluation.RowIdentity("structured-source-v1", evaluation.TreatmentCounterfactualBranch, 0), WorkloadID: "structured-source-v1", Treatment: evaluation.TreatmentCounterfactualBranch, Started: true, Status: evaluation.RowCompleted, OracleStatus: evaluation.OraclePassed, EvidenceComplete: true, Metrics: evaluation.RowMetrics{BranchChecked: true, BranchDiverged: true, ObjectCount: 1, LogicalBytes: 50, StoredBytes: 80}},
		{RowID: evaluation.RowIdentity("structured-source-v1", evaluation.TreatmentDeterministicVerify, 0), WorkloadID: "structured-source-v1", Treatment: evaluation.TreatmentDeterministicVerify, Status: evaluation.RowUnsupported, OracleStatus: evaluation.OracleNotRun, ProblemCode: "mounted_workspace_or_unqualified_scope"},
	}}
	summary, encoded, identity, err := evaluation.DeriveMeasurementSummary(study)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Offered != 4 || summary.Started != 3 || summary.Completed != 3 || summary.OraclePassed != 3 || summary.ReplayChecked != 1 || summary.ReplayEquivalent != 1 || summary.BranchChecked != 1 || summary.BranchDiverged != 1 || summary.ObjectPuts != 5 || summary.ReusedPuts != 3 || summary.LogicalBytes != 250 || summary.StoredBytes != 220 || summary.EvidenceComplete != 3 || len(encoded) == 0 || identity == "" {
		t.Fatalf("summary=%+v", summary)
	}
	if len(summary.ProhibitedClaims) != len(evaluation.RequiredProhibitedClaims()) {
		t.Fatalf("claims=%v", summary.ProhibitedClaims)
	}
	decoded, decodedID, err := evaluation.DecodeMeasurementSummary(encoded)
	if err != nil || decodedID != identity || decoded.Offered != summary.Offered {
		t.Fatalf("decoded=%+v identity=%s/%s err=%v", decoded, identity, decodedID, err)
	}
	weakened := summary
	weakened.ProhibitedClaims = weakened.ProhibitedClaims[:len(weakened.ProhibitedClaims)-1]
	if _, _, err := evaluation.EncodeMeasurementSummary(weakened); err == nil {
		t.Fatal("weakened prohibited claims accepted")
	}
}

func TestMeasurementSummaryRejectsUint32ConservationOverflow(t *testing.T) {
	summary := evaluation.MeasurementSummary{
		SchemaVersion: evaluation.MeasurementSchemaVersion, EvidenceClass: evaluation.EvidenceMechanismOnly,
		CorpusSHA256: digestA, PlanSHA256: digestB, ProhibitedClaims: evaluation.RequiredProhibitedClaims(),
		Offered: 1, Started: 1, Completed: ^uint32(0), Failed: 2,
	}
	if _, _, err := evaluation.EncodeMeasurementSummary(summary); err == nil {
		t.Fatal("uint32 conservation overflow accepted")
	}
}

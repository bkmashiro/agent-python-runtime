package evaluation_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/evaluation"
)

func TestRawStudyLifecycleConservationAndBodyFreeEvidence(t *testing.T) {
	raw := evaluation.RawStudy{
		SchemaVersion: "pysolate.evaluation-raw.v1", CorpusSHA256: digestC, PlanSHA256: digestB,
		Rows: []evaluation.RawRow{
			{RowID: evaluation.RowIdentity("structured-source-v1", evaluation.TreatmentLiveCapture, 0), WorkloadID: "structured-source-v1", Treatment: evaluation.TreatmentLiveCapture, Repetition: 0, Started: true, Status: evaluation.RowCompleted, OracleStatus: evaluation.OraclePassed, EvidenceComplete: true, PhaseMillis: evaluation.PhaseMillis{Setup: 2, Execution: 8, Oracle: 1, Evidence: 1}, Metrics: evaluation.RowMetrics{ReplayEquivalent: true, LogicalBytes: 200, StoredBytes: 120, ObjectCount: 3, ReusedObjectCount: 1}},
			{RowID: evaluation.RowIdentity("stateful-local-v1", evaluation.TreatmentDeterministicVerify, 0), WorkloadID: "stateful-local-v1", Treatment: evaluation.TreatmentDeterministicVerify, Repetition: 0, Started: false, Status: evaluation.RowUnsupported, OracleStatus: evaluation.OracleNotRun, EvidenceComplete: false, ProblemCode: "mounted_workspace_or_unqualified_scope"},
		},
	}
	if err := raw.Validate(); err != nil {
		t.Fatal(err)
	}
	evidence, digest, err := raw.Rows[0].BodyFreeEvidence(digestC, digestB)
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence) == 0 || digest == "" {
		t.Fatal("missing evidence")
	}
	for _, forbidden := range [][]byte{[]byte("prompt"), []byte("provider"), []byte("endpoint"), []byte("credential"), []byte("/Users/")} {
		if bytes.Contains(bytes.ToLower(evidence), bytes.ToLower(forbidden)) {
			t.Fatalf("leak=%s", forbidden)
		}
	}
	bad := raw
	bad.Rows = append([]evaluation.RawRow(nil), raw.Rows...)
	bad.Rows[1].Started = true
	if err := bad.Validate(); !errors.Is(err, evaluation.ErrInvalid) {
		t.Fatalf("unsupported started=%v", err)
	}
}

func TestRawStudyStrictCanonicalRoundTrip(t *testing.T) {
	raw := evaluation.RawStudy{SchemaVersion: evaluation.RawSchemaVersion, CorpusSHA256: digestC, PlanSHA256: digestB, Rows: []evaluation.RawRow{{RowID: evaluation.RowIdentity("bounded-planning-v1", evaluation.TreatmentLiveCapture, 0), WorkloadID: "bounded-planning-v1", Treatment: evaluation.TreatmentLiveCapture, Started: true, Status: evaluation.RowCompleted, OracleStatus: evaluation.OraclePassed, EvidenceComplete: true}}}
	encoded, identity, err := evaluation.EncodeRawStudy(raw)
	if err != nil {
		t.Fatal(err)
	}
	decoded, decodedIdentity, err := evaluation.DecodeRawStudy(encoded)
	if err != nil || identity != decodedIdentity || decoded.Rows[0].RowID != raw.Rows[0].RowID {
		t.Fatalf("decode=%+v identity=%s/%s err=%v", decoded, identity, decodedIdentity, err)
	}
	unknown := append([]byte(nil), encoded[:len(encoded)-1]...)
	unknown = append(unknown, []byte(`,"unknown":true}`)...)
	if _, _, err := evaluation.DecodeRawStudy(unknown); !errors.Is(err, evaluation.ErrInvalid) {
		t.Fatalf("unknown=%v", err)
	}
	if _, _, err := evaluation.DecodeRawStudy(append(encoded, []byte("{}")...)); !errors.Is(err, evaluation.ErrInvalid) {
		t.Fatalf("trailing=%v", err)
	}
}

func TestRebuildReportFromRawRowsIsExact(t *testing.T) {
	corpus, plan := runnerContracts()
	corpusRaw, corpusID, _ := evaluation.EncodeCorpus(corpus)
	_ = corpusRaw
	plan.CorpusSHA256 = corpusID
	_, planID, _ := evaluation.EncodePlan(plan)
	planned, err := evaluation.ExpandPlanRows(corpus, plan)
	if err != nil {
		t.Fatal(err)
	}
	raw := evaluation.RawStudy{SchemaVersion: "pysolate.evaluation-raw.v1", CorpusSHA256: corpusID, PlanSHA256: planID, Rows: make([]evaluation.RawRow, len(planned))}
	for i, p := range planned {
		r := evaluation.RawRow{RowID: p.RowID, WorkloadID: p.WorkloadID, Treatment: p.Treatment, Repetition: p.Repetition}
		if p.Supported {
			r.Started = true
			r.Status = evaluation.RowCompleted
			r.OracleStatus = evaluation.OraclePassed
			r.EvidenceComplete = true
			r.Metrics.ReplayEquivalent = true
		} else {
			r.Status = evaluation.RowUnsupported
			r.OracleStatus = evaluation.OracleNotRun
			r.ProblemCode = p.UnsupportedReason
		}
		raw.Rows[i] = r
	}
	report, refs, err := evaluation.RebuildReport(raw, planned)
	if err != nil {
		t.Fatal(err)
	}
	encoded, identity, err := evaluation.EncodeReport(report)
	if err != nil || len(encoded) == 0 || len(refs) != len(planned) {
		t.Fatal(err)
	}
	rebuilt, refs2, err := evaluation.RebuildReport(raw, planned)
	if err != nil {
		t.Fatal(err)
	}
	_, identity2, _ := evaluation.EncodeReport(rebuilt)
	if identity != identity2 || !bytes.Equal([]byte(refs[0]), []byte(refs2[0])) {
		t.Fatal("rebuild drift")
	}
	raw.Rows[0].Metrics.StoredBytes = raw.Rows[0].Metrics.LogicalBytes + 1
	if _, _, err := evaluation.RebuildReport(raw, planned); !errors.Is(err, evaluation.ErrInvalid) {
		t.Fatalf("storage=%v", err)
	}
}

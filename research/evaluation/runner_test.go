package evaluation_test

import (
	"errors"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/evaluation"
)

func TestExpandPlanRowsIsDeterministicAndIncludesUnsupported(t *testing.T) {
	corpus, plan := runnerContracts()
	rows, err := evaluation.ExpandPlanRows(corpus, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 24 {
		t.Fatalf("rows=%d", len(rows))
	}
	if rows[0].WorkloadID != corpus.Workloads[0].ID || rows[0].Treatment != plan.TreatmentOrder[0] || rows[0].Repetition != 0 {
		t.Fatalf("first=%+v", rows[0])
	}
	for i, row := range rows {
		if row.RowID != evaluation.RowIdentity(row.WorkloadID, row.Treatment, row.Repetition) {
			t.Fatalf("row %d identity", i)
		}
	}
	var unsupported int
	for _, row := range rows {
		if !row.Supported {
			unsupported++
		}
	}
	if unsupported != 6 {
		t.Fatalf("unsupported=%d", unsupported)
	}
	plan.Ceilings.MaxRows = 23
	if _, err := evaluation.ExpandPlanRows(corpus, plan); !errors.Is(err, evaluation.ErrInvalid) {
		t.Fatalf("ceiling=%v", err)
	}
}

func TestBuildReportRequiresExactOutcomeConservation(t *testing.T) {
	corpus, plan := runnerContracts()
	corpusRaw, corpusID, err := evaluation.EncodeCorpus(corpus)
	if err != nil || len(corpusRaw) == 0 {
		t.Fatal(err)
	}
	plan.CorpusSHA256 = corpusID
	_, planID, err := evaluation.EncodePlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	planned, err := evaluation.ExpandPlanRows(corpus, plan)
	if err != nil {
		t.Fatal(err)
	}
	outcomes := make([]evaluation.RowOutcome, len(planned))
	for i, row := range planned {
		if row.Supported {
			outcomes[i] = evaluation.RowOutcome{RowID: row.RowID, Status: evaluation.RowCompleted, OracleStatus: evaluation.OraclePassed, EvidenceComplete: true, EvidenceRefs: []string{"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}
		} else {
			outcomes[i] = evaluation.RowOutcome{RowID: row.RowID, Status: evaluation.RowUnsupported, OracleStatus: evaluation.OracleNotRun, ProblemCode: row.UnsupportedReason, EvidenceRefs: []string{}}
		}
	}
	report, err := evaluation.BuildReport(corpusID, planID, planned, outcomes)
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.Offered != uint32(len(planned)) || report.Summary.Unsupported != 6 {
		t.Fatalf("summary=%+v", report.Summary)
	}
	outcomes = outcomes[:len(outcomes)-1]
	if _, err := evaluation.BuildReport(corpusID, planID, planned, outcomes); !errors.Is(err, evaluation.ErrInvalid) {
		t.Fatalf("missing=%v", err)
	}
}

func runnerContracts() (evaluation.Corpus, evaluation.Plan) {
	fixtures, _ := evaluation.CanonicalFixtures()
	corpus, _, _ := evaluation.DecodeCorpus(fixtures["corpus.json"].Bytes)
	plan, _, _ := evaluation.DecodePlan(fixtures["plan.json"].Bytes)
	plan.TreatmentOrder = []evaluation.Treatment{evaluation.TreatmentLiveCapture, evaluation.TreatmentOfflineReplay, evaluation.TreatmentCounterfactualBranch, evaluation.TreatmentDeterministicVerify}
	plan.Repetitions = 2
	plan.Ceilings.MaxRows = 24
	return corpus, plan
}

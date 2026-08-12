package evaluationv2

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func completedRows(t *testing.T) (Corpus, Plan, []PilotRow) {
	t.Helper()
	corpus, err := PilotCorpus()
	if err != nil {
		t.Fatal(err)
	}
	_, corpusID, err := EncodeCorpus(corpus)
	if err != nil {
		t.Fatal(err)
	}
	plan := PilotPlan("0123456789abcdef0123456789abcdef01234567", digest('a'), digest('b'), digest('c'), corpusID)
	rows, err := ExpandRows(corpus, plan)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]PilotRow, len(rows))
	for i, row := range rows {
		calls := uint32(1)
		if row.WorkloadID == "source-join-ranking" {
			calls = 2
		}
		boundaries := calls
		if row.Condition == ConditionGuest {
			boundaries = 1
		}
		capabilityPlanID := digest('d')
		if row.WorkloadID == "source-join-ranking" {
			capabilityPlanID = digest('e')
		}
		out[i] = PilotRow{RowID: row.RowID, WorkloadID: row.WorkloadID, Condition: row.Condition, Repetition: row.Repetition, Status: StatusCompleted, OracleStatus: OraclePassed, EvidenceComplete: true, CapabilityPlanSHA256: capabilityPlanID, Metrics: PilotMetrics{ControllerBoundaries: boundaries, ControllerRequestBytes: 100, ControllerResponseBytes: 80, CapabilityCalls: calls, CapabilityArgumentBytes: uint64(calls) * 2, CapabilityResultBytes: uint64(calls) * 100, Receipts: calls, TranscriptEntries: calls}}
	}
	return corpus, plan, out
}

func TestPilotStudyStrictRoundTripAndSummary(t *testing.T) {
	corpus, plan, rows := completedRows(t)
	_, corpusID, _ := EncodeCorpus(corpus)
	_, planID, _ := EncodePlan(plan)
	study := PilotStudy{SchemaVersion: StudySchemaVersion, EvidenceClass: EvidenceClass, CorpusSHA256: corpusID, PlanSHA256: planID, ProhibitedClaims: RequiredProhibitedClaims(), Rows: rows}
	if err := ValidateStudyAgainst(study, corpus, plan); err != nil {
		t.Fatal(err)
	}
	encoded, identity, err := EncodeStudy(study)
	if err != nil {
		t.Fatal(err)
	}
	decoded, decodedID, err := DecodeStudy(encoded)
	if err != nil || decodedID != identity || len(decoded.Rows) != 4 {
		t.Fatalf("id=%q err=%v", decodedID, err)
	}
	summary, summaryBytes, _, err := DeriveSummary(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Offered != 4 || summary.Completed != 4 || summary.OraclePassed != 4 || summary.DirectRows != 2 || summary.GuestRows != 2 || summary.DirectControllerBoundaries != 3 || summary.GuestControllerBoundaries != 2 || summary.DirectCapabilityCalls != 3 || summary.GuestCapabilityCalls != 3 {
		t.Fatalf("summary=%+v", summary)
	}
	if bytes.Contains(summaryBytes, []byte("Alpha")) || bytes.Contains(summaryBytes, []byte("workspace-summary")) {
		t.Fatal("summary leaked source body")
	}
	forged := summary
	forged.Completed = ^uint32(0)
	forged.Failed = 5
	forged.TimedOut = 0
	if _, _, err := EncodeSummary(forged); !errors.Is(err, ErrInvalid) {
		t.Fatalf("overflow summary err=%v", err)
	}
}

func TestExpandedStudyTenRowConservation(t *testing.T) {
	corpus, err := ExpandedCorpus()
	if err != nil {
		t.Fatal(err)
	}
	_, corpusID, err := EncodeCorpus(corpus)
	if err != nil {
		t.Fatal(err)
	}
	plan := ExpandedPlan(strings.Repeat("a", 40), digest('b'), digest('c'), digest('d'), corpusID)
	_, planID, err := EncodePlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	planned, err := ExpandRows(corpus, plan)
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]PilotRow, len(planned))
	for i, row := range planned {
		calls := corpus.Workloads[i/2].ExpectedCapabilityCalls
		boundaries := calls
		if row.Condition == ConditionGuest {
			boundaries = 1
		}
		rows[i] = PilotRow{RowID: row.RowID, WorkloadID: row.WorkloadID, Condition: row.Condition, Status: StatusCompleted, OracleStatus: OraclePassed, EvidenceComplete: true, CapabilityPlanSHA256: digest(byte('a' + i/2)), Metrics: PilotMetrics{ControllerBoundaries: boundaries, ControllerRequestBytes: 1, ControllerResponseBytes: 1, CapabilityCalls: calls, CapabilityArgumentBytes: uint64(calls), CapabilityResultBytes: uint64(calls), Receipts: calls, TranscriptEntries: calls}}
	}
	study := PilotStudy{SchemaVersion: ExpandedStudySchemaVersion, EvidenceClass: EvidenceClass, CorpusSHA256: corpusID, PlanSHA256: planID, ProhibitedClaims: RequiredProhibitedClaims(), Rows: rows}
	if err := ValidateStudyAgainst(study, corpus, plan); err != nil {
		t.Fatal(err)
	}
	summary, _, _, err := DeriveSummary(study)
	if err != nil || summary.SchemaVersion != ExpandedSummarySchemaVersion || summary.Offered != 10 || summary.DirectRows != 5 || summary.GuestRows != 5 || summary.DirectControllerBoundaries != 6 || summary.GuestControllerBoundaries != 5 || summary.DirectCapabilityCalls != 6 || summary.GuestCapabilityCalls != 6 {
		t.Fatalf("summary=%+v err=%v", summary, err)
	}
}

func TestPilotStudyRejectsMetricAndIdentityDrift(t *testing.T) {
	corpus, plan, rows := completedRows(t)
	_, corpusID, _ := EncodeCorpus(corpus)
	_, planID, _ := EncodePlan(plan)
	study := PilotStudy{SchemaVersion: StudySchemaVersion, EvidenceClass: EvidenceClass, CorpusSHA256: corpusID, PlanSHA256: planID, ProhibitedClaims: RequiredProhibitedClaims(), Rows: rows}
	mutations := []func(*PilotStudy){
		func(v *PilotStudy) { v.Rows[0].Metrics.Receipts = 0 },
		func(v *PilotStudy) { v.Rows[0].Metrics.ControllerBoundaries = 0 },
		func(v *PilotStudy) { v.Rows[0].Condition = ConditionGuest },
		func(v *PilotStudy) { v.Rows[0].Metrics.CapabilityCalls++ },
		func(v *PilotStudy) { v.Rows[0].Status = StatusFailed; v.Rows[0].ProblemCode = "" },
		func(v *PilotStudy) { v.Rows[1].CapabilityPlanSHA256 = digest('f') },
	}
	for i, mutate := range mutations {
		copyStudy := study
		copyStudy.Rows = append([]PilotRow(nil), study.Rows...)
		mutate(&copyStudy)
		if _, _, err := EncodeStudy(copyStudy); !errors.Is(err, ErrInvalid) {
			t.Fatalf("mutation %d err=%v", i, err)
		}
	}
	encoded, _, err := EncodeStudy(study)
	if err != nil {
		t.Fatal(err)
	}
	unknown := bytes.Replace(encoded, []byte(`"schema_version"`), []byte(`"unknown":true,"schema_version"`), 1)
	if _, _, err := DecodeStudy(unknown); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown err=%v", err)
	}
	var value any
	if json.Unmarshal(encoded, &value) != nil {
		t.Fatal("invalid JSON")
	}
}

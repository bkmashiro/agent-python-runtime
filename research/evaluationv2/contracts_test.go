package evaluationv2

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

func TestCanonicalPilotCorpusAndPlanRoundTrip(t *testing.T) {
	corpus, err := PilotCorpus()
	if err != nil {
		t.Fatal(err)
	}
	if len(corpus.Workloads) != 2 || corpus.Workloads[0].ID != "catalog-top-direct" || corpus.Workloads[1].ID != "source-join-ranking" {
		t.Fatalf("workloads=%+v", corpus.Workloads)
	}
	corpusBytes, corpusID, err := EncodeCorpus(corpus)
	if err != nil {
		t.Fatal(err)
	}
	decoded, decodedID, err := DecodeCorpus(corpusBytes)
	if err != nil || decodedID != corpusID || len(decoded.Workloads) != 2 {
		t.Fatalf("decode id=%q err=%v", decodedID, err)
	}
	plan := PilotPlan("0123456789abcdef0123456789abcdef01234567", digest('a'), digest('b'), digest('c'), corpusID)
	planBytes, _, err := EncodePlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := ExpandRows(corpus, plan)
	if err != nil || len(rows) != 4 {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
	want := []string{"catalog-top-direct:direct_broker:0", "catalog-top-direct:pysolate_guest:0", "source-join-ranking:direct_broker:0", "source-join-ranking:pysolate_guest:0"}
	for i := range rows {
		if rows[i].RowID != want[i] {
			t.Fatalf("row[%d]=%q", i, rows[i].RowID)
		}
	}
	if _, _, err := DecodePlan(planBytes); err != nil {
		t.Fatal(err)
	}
}

func TestPilotContractRejectsDrift(t *testing.T) {
	corpus, err := PilotCorpus()
	if err != nil {
		t.Fatal(err)
	}
	corpusBytes, corpusID, err := EncodeCorpus(corpus)
	if err != nil {
		t.Fatal(err)
	}
	unknown := bytes.Replace(corpusBytes, []byte(`"schema_version"`), []byte(`"unknown":true,"schema_version"`), 1)
	if _, _, err := DecodeCorpus(unknown); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown err=%v", err)
	}
	duplicate := corpus
	duplicate.Workloads = append([]Workload(nil), corpus.Workloads...)
	duplicate.Workloads[1].ID = duplicate.Workloads[0].ID
	if _, _, err := EncodeCorpus(duplicate); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate err=%v", err)
	}
	tampered := corpus
	tampered.Workloads = append([]Workload(nil), corpus.Workloads...)
	tampered.Workloads[0].CodeSHA256 = digest('e')
	if _, _, err := EncodeCorpus(tampered); !errors.Is(err, ErrInvalid) {
		t.Fatalf("tampered pilot err=%v", err)
	}
	plan := PilotPlan("0123456789abcdef0123456789abcdef01234567", digest('a'), digest('b'), digest('c'), corpusID)
	plan.Conditions = []Condition{ConditionGuest, ConditionDirect}
	if _, _, err := EncodePlan(plan); !errors.Is(err, ErrInvalid) {
		t.Fatalf("condition order err=%v", err)
	}
	plan = PilotPlan("0123456789abcdef0123456789abcdef01234567", digest('a'), digest('b'), digest('c'), corpusID)
	plan.ProhibitedClaims = plan.ProhibitedClaims[:len(plan.ProhibitedClaims)-1]
	if _, _, err := EncodePlan(plan); !errors.Is(err, ErrInvalid) {
		t.Fatalf("claims err=%v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(corpusBytes, &raw); err != nil {
		t.Fatal(err)
	}
	raw["evidence_class"] = "qualified_workload"
	mutated, _ := json.Marshal(raw)
	if _, _, err := DecodeCorpus(mutated); !errors.Is(err, ErrInvalid) {
		t.Fatalf("evidence class err=%v", err)
	}
}

func TestPilotWorkloadsHaveConditionNeutralOracles(t *testing.T) {
	corpus, err := PilotCorpus()
	if err != nil {
		t.Fatal(err)
	}
	for _, workload := range corpus.Workloads {
		if workload.ExpectedResultSHA256 == "" || workload.ExpectedCapabilityCalls == 0 || len(workload.RequiredCapabilities) == 0 {
			t.Fatalf("workload=%+v", workload)
		}
		if workload.ID == "catalog-top-direct" && workload.ExpectedCapabilityCalls != 1 {
			t.Fatal("negative control must use one call")
		}
		if workload.ID == "source-join-ranking" && workload.ExpectedCapabilityCalls != 2 {
			t.Fatal("join pilot must use two calls")
		}
	}
}

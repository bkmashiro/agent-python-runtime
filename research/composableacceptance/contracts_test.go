package composableacceptance_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/composableacceptance"
)

func TestCorpusStrictCanonicalRoundTrip(t *testing.T) {
	value := validCorpus()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	decoded, identity, err := composableacceptance.DecodeCorpus(data)
	if err != nil || decoded.SourceCommit != value.SourceCommit || identity == "" {
		t.Fatalf("decoded=%+v identity=%s err=%v", decoded, identity, err)
	}
	for name, invalid := range map[string][]byte{
		"whitespace": append([]byte(" "), data...),
		"trailing":   append(append([]byte(nil), data...), []byte(`{}`)...),
		"unknown":    append(append([]byte(nil), data[:len(data)-1]...), []byte(`,"private_body":"x"}`)...),
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := composableacceptance.DecodeCorpus(invalid); !errors.Is(err, composableacceptance.ErrInvalid) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestReportRejectsIncompletePassedRowsAndSorts(t *testing.T) {
	scenario := validCorpus().Scenarios[0]
	scenarioID, err := composableacceptance.ScenarioIdentity(scenario)
	if err != nil {
		t.Fatal(err)
	}
	rows := []composableacceptance.Row{
		{ScenarioID: scenario.ID, ScenarioSHA256: scenarioID, Treatment: composableacceptance.TreatmentCOW, Status: "passed", OracleSHA256: digest('b'), GuestCreated: 2, GuestDestroyed: 2, TerminalDisposition: "completed", EvidenceComplete: true},
		{ScenarioID: scenario.ID, ScenarioSHA256: scenarioID, Treatment: composableacceptance.TreatmentFresh, Status: "passed", OracleSHA256: digest('b'), GuestCreated: 1, GuestDestroyed: 1, TerminalDisposition: "completed", EvidenceComplete: true},
	}
	composableacceptance.SortRows(rows)
	if rows[0].Treatment != composableacceptance.TreatmentFresh {
		t.Fatalf("rows=%+v", rows)
	}
	report := composableacceptance.Report{SchemaVersion: composableacceptance.ReportSchemaVersion, SourceCommit: validCorpus().SourceCommit, GuestArtifactSHA256: digest('a'), CorpusSHA256: digest('c'), Model: "gpt-5.3-codex-spark", Rows: rows}
	data, identity, err := composableacceptance.EncodeReport(report)
	if err != nil || identity == "" || !bytes.Contains(data, []byte(`"treatment":"cow"`)) {
		t.Fatalf("data=%s identity=%s err=%v", data, identity, err)
	}
	report.Rows[0].EvidenceComplete = false
	if _, _, err := composableacceptance.EncodeReport(report); !errors.Is(err, composableacceptance.ErrInvalid) {
		t.Fatalf("err=%v", err)
	}
}

func validCorpus() composableacceptance.Corpus {
	scenarios := make([]composableacceptance.Scenario, 3)
	for index := range scenarios {
		scenarios[index] = composableacceptance.Scenario{
			ID: "scenario-test-" + string(rune('a'+index)), Task: "Inspect a bounded cross-file runtime contract.",
			Files: []string{"runtime/a.go", "runtime/b.go"}, ChildAnalyses: []string{"left", "right"},
			RepeatedTransformation: "canonicalize", WaitBoundary: "wait", Observation: "source digest",
			SelectedChild: index % 2, ExpectedArtifact: "REPORT: deterministic", ProhibitedOutputs: []string{"credentials"},
		}
	}
	return composableacceptance.Corpus{SchemaVersion: composableacceptance.CorpusSchemaVersion, SourceCommit: "2451cc35cff566ad556c18c2f57064e233994675", Model: "gpt-5.3-codex-spark", Scenarios: scenarios}
}

func digest(value byte) string {
	return "sha256:" + string(bytes.Repeat([]byte{value}, 64))
}

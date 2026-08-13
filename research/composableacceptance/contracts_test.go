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
		{ScenarioID: scenario.ID, ScenarioSHA256: scenarioID, Treatment: composableacceptance.TreatmentCOW, Status: "passed", OracleSHA256: digest('b'), GuestCreated: 2, GuestDestroyed: 2, RelativeElapsedMillis: 1, EvidenceScope: "direct_replay", ConformanceSHA256: digest('c'), TerminalDisposition: "closed", EvidenceComplete: true, Trace: minimalTrace("passed", "closed")},
		{ScenarioID: scenario.ID, ScenarioSHA256: scenarioID, Treatment: composableacceptance.TreatmentFresh, Status: "passed", OracleSHA256: digest('b'), GuestCreated: 1, GuestDestroyed: 1, RelativeElapsedMillis: 1, EvidenceScope: "direct_replay", ConformanceSHA256: digest('c'), TerminalDisposition: "closed", EvidenceComplete: true, Trace: minimalTrace("passed", "closed")},
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

func TestReportRejectsRowsWithoutTrace(t *testing.T) {
	scenario := validCorpus().Scenarios[0]
	scenarioID, err := composableacceptance.ScenarioIdentity(scenario)
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"passed", "rejected", "skipped"} {
		t.Run(status, func(t *testing.T) {
			report := composableacceptance.Report{
				SchemaVersion:       composableacceptance.ReportSchemaVersion,
				SourceCommit:        validCorpus().SourceCommit,
				GuestArtifactSHA256: digest('a'),
				CorpusSHA256:        digest('c'),
				Model:               "gpt-5.3-codex-spark",
				Rows: []composableacceptance.Row{
					{
						ScenarioID:            scenario.ID,
						ScenarioSHA256:        scenarioID,
						Treatment:             composableacceptance.TreatmentFresh,
						Status:                status,
						OracleSHA256:          digest('b'),
						RelativeElapsedMillis: 1,
						EvidenceScope:         "direct_replay",
						ConformanceSHA256:     digest('c'),
						TerminalDisposition:   "closed",
						EvidenceComplete:      true,
					},
				},
			}
			if status == "passed" {
				report.Rows[0].GuestCreated = 1
				report.Rows[0].GuestDestroyed = 1
			}
			if _, _, err := composableacceptance.EncodeReport(report); !errors.Is(err, composableacceptance.ErrInvalid) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestReportValidatesTraceSequenceAndTerminal(t *testing.T) {
	scenario := validCorpus().Scenarios[0]
	scenarioID, err := composableacceptance.ScenarioIdentity(scenario)
	if err != nil {
		t.Fatal(err)
	}
	baseRow := composableacceptance.Row{
		ScenarioID:            scenario.ID,
		ScenarioSHA256:        scenarioID,
		Treatment:             composableacceptance.TreatmentFresh,
		Status:                "passed",
		OracleSHA256:          digest('b'),
		RelativeElapsedMillis: 2,
		GuestCreated:          1,
		GuestDestroyed:        1,
		EvidenceScope:         "direct_replay",
		ConformanceSHA256:     digest('c'),
		TerminalDisposition:   "closed",
		EvidenceComplete:      true,
	}
	report := composableacceptance.Report{
		SchemaVersion:       composableacceptance.ReportSchemaVersion,
		SourceCommit:        validCorpus().SourceCommit,
		GuestArtifactSHA256: digest('a'),
		CorpusSHA256:        digest('c'),
		Model:               "gpt-5.3-codex-spark",
	}

	row := baseRow
	row.Trace = []composableacceptance.TraceEvent{
		{
			Sequence:              1,
			Type:                  composableacceptance.TraceEventTypeStreaming,
			Action:                "cache.lookup",
			Outcome:               composableacceptance.TraceEventOutcomeHit,
			RelativeElapsedMillis: 1,
		},
		{
			Sequence:              2,
			Type:                  composableacceptance.TraceEventTypeRunTerminal,
			Action:                "run.terminal",
			Outcome:               composableacceptance.TraceEventOutcomeOK,
			TerminalDisposition:   "closed",
			ParentSequence:        ptr(1),
			RelativeElapsedMillis: 2,
		},
	}
	report.Rows = []composableacceptance.Row{row}
	if _, _, err := composableacceptance.EncodeReport(report); !errors.Is(err, composableacceptance.ErrInvalid) {
		t.Fatalf("err=%v", err)
	}

	row.Trace = []composableacceptance.TraceEvent{
		{
			Sequence:              1,
			Type:                  composableacceptance.TraceEventTypeRunStart,
			Action:                "run.start",
			Outcome:               composableacceptance.TraceEventOutcomeStarted,
			RelativeElapsedMillis: 1,
		},
		{
			Sequence:              2,
			Type:                  composableacceptance.TraceEventTypeRunTerminal,
			Action:                "run.terminal",
			Outcome:               composableacceptance.TraceEventOutcomeOK,
			TerminalDisposition:   "closed",
			ParentSequence:        ptr(1),
			RelativeElapsedMillis: 2,
		},
		{
			Sequence:              3,
			Type:                  composableacceptance.TraceEventTypeObservation,
			Action:                "cache.lookup",
			Outcome:               composableacceptance.TraceEventOutcomeHit,
			ParentSequence:        ptr(2),
			RelativeElapsedMillis: 2,
		},
	}
	report.Rows = []composableacceptance.Row{row}
	if _, _, err := composableacceptance.EncodeReport(report); !errors.Is(err, composableacceptance.ErrInvalid) {
		t.Fatalf("err=%v", err)
	}

	row.Trace = minimalTrace("passed", "closed")
	row.Trace = []composableacceptance.TraceEvent{row.Trace[0], row.Trace[len(row.Trace)-1]}
	row.Trace[1].Sequence = 2
	row.Trace[1].ParentSequence = ptr(1)
	report.Rows = []composableacceptance.Row{row}
	if _, _, err := composableacceptance.EncodeReport(report); !errors.Is(err, composableacceptance.ErrInvalid) {
		t.Fatalf("summary-only trace accepted: err=%v", err)
	}

	row.Trace = minimalTrace("passed", "closed")
	report.Rows = []composableacceptance.Row{row}
	if _, _, err := composableacceptance.EncodeReport(report); !errors.Is(err, nil) {
		t.Fatalf("err=%v", err)
	}
}

func TestReportRejectsTraceWithInvalidParent(t *testing.T) {
	scenario := validCorpus().Scenarios[0]
	scenarioID, err := composableacceptance.ScenarioIdentity(scenario)
	if err != nil {
		t.Fatal(err)
	}
	report := composableacceptance.Report{
		SchemaVersion:       composableacceptance.ReportSchemaVersion,
		SourceCommit:        validCorpus().SourceCommit,
		GuestArtifactSHA256: digest('a'),
		CorpusSHA256:        digest('c'),
		Model:               "gpt-5.3-codex-spark",
		Rows: []composableacceptance.Row{
			{
				ScenarioID:            scenario.ID,
				ScenarioSHA256:        scenarioID,
				Treatment:             composableacceptance.TreatmentFresh,
				Status:                "passed",
				OracleSHA256:          digest('b'),
				RelativeElapsedMillis: 1,
				GuestCreated:          1,
				GuestDestroyed:        1,
				EvidenceScope:         "direct_replay",
				ConformanceSHA256:     digest('c'),
				TerminalDisposition:   "closed",
				EvidenceComplete:      true,
				Trace: []composableacceptance.TraceEvent{
					{
						Sequence:              1,
						Type:                  composableacceptance.TraceEventTypeRunStart,
						Action:                "run.start",
						Outcome:               composableacceptance.TraceEventOutcomeStarted,
						RelativeElapsedMillis: 1,
					},
					{
						Sequence:              2,
						Type:                  composableacceptance.TraceEventTypeRunTerminal,
						Action:                "run.terminal",
						Outcome:               composableacceptance.TraceEventOutcomeOK,
						TerminalDisposition:   "closed",
						ParentSequence:        ptr(9),
						RelativeElapsedMillis: 1,
					},
				},
			},
		},
	}
	if _, _, err := composableacceptance.EncodeReport(report); !errors.Is(err, composableacceptance.ErrInvalid) {
		t.Fatalf("err=%v", err)
	}
}

func TestReportRejectsSkippedRowsWithoutTrace(t *testing.T) {
	scenario := validCorpus().Scenarios[0]
	scenarioID, err := composableacceptance.ScenarioIdentity(scenario)
	if err != nil {
		t.Fatal(err)
	}
	report := composableacceptance.Report{
		SchemaVersion:       composableacceptance.ReportSchemaVersion,
		SourceCommit:        validCorpus().SourceCommit,
		GuestArtifactSHA256: digest('a'),
		CorpusSHA256:        digest('c'),
		Model:               "gpt-5.3-codex-spark",
		Rows: []composableacceptance.Row{
			{
				ScenarioID:            scenario.ID,
				ScenarioSHA256:        scenarioID,
				Treatment:             composableacceptance.TreatmentFresh,
				Status:                "skipped",
				OracleSHA256:          digest('b'),
				RelativeElapsedMillis: 1,
				EvidenceScope:         "direct_replay",
				ConformanceSHA256:     digest('c'),
				TerminalDisposition:   "mechanism_disabled",
			},
		},
	}
	if _, _, err := composableacceptance.EncodeReport(report); !errors.Is(err, composableacceptance.ErrInvalid) {
		t.Fatalf("err=%v", err)
	}
}

func TestReportRequiresExactTerminalDispositionMatch(t *testing.T) {
	scenario := validCorpus().Scenarios[0]
	scenarioID, err := composableacceptance.ScenarioIdentity(scenario)
	if err != nil {
		t.Fatal(err)
	}
	report := composableacceptance.Report{
		SchemaVersion:       composableacceptance.ReportSchemaVersion,
		SourceCommit:        validCorpus().SourceCommit,
		GuestArtifactSHA256: digest('a'),
		CorpusSHA256:        digest('c'),
		Model:               "gpt-5.3-codex-spark",
		Rows: []composableacceptance.Row{
			{
				ScenarioID:            scenario.ID,
				ScenarioSHA256:        scenarioID,
				Treatment:             composableacceptance.TreatmentFresh,
				Status:                "rejected",
				OracleSHA256:          digest('b'),
				RelativeElapsedMillis: 1,
				GuestCreated:          1,
				GuestDestroyed:        1,
				EvidenceScope:         "direct_replay",
				ConformanceSHA256:     digest('c'),
				TerminalDisposition:   "mechanism_disabled",
				EvidenceComplete:      true,
				Trace:                 minimalTrace("rejected", "mechanism_disabled"),
			},
		},
	}
	if _, _, err := composableacceptance.EncodeReport(report); !errors.Is(err, nil) {
		t.Fatalf("err=%v", err)
	}

	report.Rows[0].Trace[len(report.Rows[0].Trace)-1].TerminalDisposition = "closed"
	if _, _, err := composableacceptance.EncodeReport(report); !errors.Is(err, composableacceptance.ErrInvalid) {
		t.Fatalf("err=%v", err)
	}
}

func TestReportRejectsUnsafeTraceActionOrCheckpointStatus(t *testing.T) {
	scenario := validCorpus().Scenarios[0]
	scenarioID, err := composableacceptance.ScenarioIdentity(scenario)
	if err != nil {
		t.Fatal(err)
	}
	base := composableacceptance.Report{
		SchemaVersion:       composableacceptance.ReportSchemaVersion,
		SourceCommit:        validCorpus().SourceCommit,
		GuestArtifactSHA256: digest('a'),
		CorpusSHA256:        digest('c'),
		Model:               "gpt-5.3-codex-spark",
	}
	row := composableacceptance.Row{
		ScenarioID:            scenario.ID,
		ScenarioSHA256:        scenarioID,
		Treatment:             composableacceptance.TreatmentFresh,
		Status:                "passed",
		OracleSHA256:          digest('b'),
		RelativeElapsedMillis: 1,
		GuestCreated:          1,
		GuestDestroyed:        1,
		EvidenceScope:         "direct_replay",
		ConformanceSHA256:     digest('c'),
		TerminalDisposition:   "closed",
		EvidenceComplete:      true,
		Trace:                 minimalTrace("passed", "closed"),
	}
	base.Rows = []composableacceptance.Row{row}
	base.Rows[0].Trace[0].Action = "Run.Start"
	if _, _, err := composableacceptance.EncodeReport(base); !errors.Is(err, composableacceptance.ErrInvalid) {
		t.Fatalf("err=%v", err)
	}

	base.Rows[0].Trace[0].Action = "run.start"
	base.Rows[0].Trace[0].CheckpointSHA256 = digest('x')
	base.Rows[0].Trace[0].CheckpointStatus = "bad status!"
	if _, _, err := composableacceptance.EncodeReport(base); !errors.Is(err, composableacceptance.ErrInvalid) {
		t.Fatalf("err=%v", err)
	}
}

func TestReportAcceptsAbsentCheckpointOnTraceEvents(t *testing.T) {
	scenario := validCorpus().Scenarios[0]
	scenarioID, err := composableacceptance.ScenarioIdentity(scenario)
	if err != nil {
		t.Fatal(err)
	}
	report := composableacceptance.Report{
		SchemaVersion:       composableacceptance.ReportSchemaVersion,
		SourceCommit:        validCorpus().SourceCommit,
		GuestArtifactSHA256: digest('a'),
		CorpusSHA256:        digest('c'),
		Model:               "gpt-5.3-codex-spark",
		Rows: []composableacceptance.Row{
			{
				ScenarioID:            scenario.ID,
				ScenarioSHA256:        scenarioID,
				Treatment:             composableacceptance.TreatmentFresh,
				Status:                "skipped",
				OracleSHA256:          digest('b'),
				RelativeElapsedMillis: 1,
				EvidenceScope:         "direct_replay",
				ConformanceSHA256:     digest('c'),
				TerminalDisposition:   "parent_invalid",
				Trace:                 minimalTrace("skipped", "parent_invalid"),
			},
		},
	}
	if _, _, err := composableacceptance.EncodeReport(report); !errors.Is(err, nil) {
		t.Fatalf("err=%v", err)
	}
}

func TestReportRejectsNonMonotonicElapsed(t *testing.T) {
	scenario := validCorpus().Scenarios[0]
	scenarioID, err := composableacceptance.ScenarioIdentity(scenario)
	if err != nil {
		t.Fatal(err)
	}
	row := composableacceptance.Row{
		ScenarioID:            scenario.ID,
		ScenarioSHA256:        scenarioID,
		Treatment:             composableacceptance.TreatmentFresh,
		Status:                "passed",
		OracleSHA256:          digest('b'),
		RelativeElapsedMillis: 2,
		GuestCreated:          1,
		GuestDestroyed:        1,
		EvidenceScope:         "direct_replay",
		ConformanceSHA256:     digest('c'),
		TerminalDisposition:   "closed",
		EvidenceComplete:      true,
		Trace: []composableacceptance.TraceEvent{
			{
				Sequence:              1,
				Type:                  composableacceptance.TraceEventTypeRunStart,
				Action:                "run.start",
				Outcome:               composableacceptance.TraceEventOutcomeStarted,
				RelativeElapsedMillis: 1,
			},
			{
				Sequence:              2,
				Type:                  composableacceptance.TraceEventTypeWorkspace,
				Action:                "cache.lookup",
				Outcome:               composableacceptance.TraceEventOutcomeHit,
				ParentSequence:        ptr(1),
				RelativeElapsedMillis: 0.5,
			},
			{
				Sequence:              3,
				Type:                  composableacceptance.TraceEventTypeRunTerminal,
				Action:                "run.terminal",
				Outcome:               composableacceptance.TraceEventOutcomeOK,
				TerminalDisposition:   "closed",
				ParentSequence:        ptr(2),
				RelativeElapsedMillis: 3,
			},
		},
	}
	report := composableacceptance.Report{
		SchemaVersion:       composableacceptance.ReportSchemaVersion,
		SourceCommit:        validCorpus().SourceCommit,
		GuestArtifactSHA256: digest('a'),
		CorpusSHA256:        digest('c'),
		Model:               "gpt-5.3-codex-spark",
		Rows:                []composableacceptance.Row{row},
	}
	if _, _, err := composableacceptance.EncodeReport(report); !errors.Is(err, composableacceptance.ErrInvalid) {
		t.Fatalf("err=%v", err)
	}
}

func TestReportRejectsSchemaV1(t *testing.T) {
	scenario := validCorpus().Scenarios[0]
	scenarioID, err := composableacceptance.ScenarioIdentity(scenario)
	if err != nil {
		t.Fatal(err)
	}
	report := composableacceptance.Report{
		SchemaVersion:       "pysolate.composable-acceptance-report.v1",
		SourceCommit:        validCorpus().SourceCommit,
		GuestArtifactSHA256: digest('a'),
		CorpusSHA256:        digest('c'),
		Model:               "gpt-5.3-codex-spark",
		Rows: []composableacceptance.Row{
			{
				ScenarioID:            scenario.ID,
				ScenarioSHA256:        scenarioID,
				Treatment:             composableacceptance.TreatmentFresh,
				Status:                "passed",
				OracleSHA256:          digest('b'),
				RelativeElapsedMillis: 1,
				GuestCreated:          1,
				GuestDestroyed:        1,
				EvidenceScope:         "direct_replay",
				ConformanceSHA256:     digest('c'),
				TerminalDisposition:   "closed",
				EvidenceComplete:      true,
				Trace:                 minimalTrace("passed", "closed"),
			},
		},
	}
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

func minimalTrace(status string, terminalDisposition string) []composableacceptance.TraceEvent {
	disposition := traceTerminalOutcome(status)
	trace := []composableacceptance.TraceEvent{
		{
			Sequence:              1,
			Type:                  composableacceptance.TraceEventTypeRunStart,
			Action:                "run.start",
			Outcome:               composableacceptance.TraceEventOutcomeStarted,
			RelativeElapsedMillis: 1,
		},
	}
	parent := uint32(1)
	if status != "skipped" {
		trace = append(trace, composableacceptance.TraceEvent{
			Sequence:              2,
			Type:                  composableacceptance.TraceEventTypeGuestLifecycle,
			Action:                "guest.run",
			Outcome:               composableacceptance.TraceEventOutcomeOK,
			ParentSequence:        ptr(1),
			RelativeElapsedMillis: 1,
		})
		parent = 2
	}
	trace = append(trace, composableacceptance.TraceEvent{
		Sequence:              parent + 1,
		Type:                  composableacceptance.TraceEventTypeRunTerminal,
		Action:                "run.terminal",
		Outcome:               disposition,
		TerminalDisposition:   terminalDisposition,
		ParentSequence:        ptr(parent),
		RelativeElapsedMillis: 1,
	})
	return trace
}

func traceTerminalOutcome(status string) composableacceptance.TraceEventOutcome {
	switch status {
	case "passed":
		return composableacceptance.TraceEventOutcomeOK
	case "rejected":
		return composableacceptance.TraceEventOutcomeRejected
	case "skipped":
		return composableacceptance.TraceEventOutcomeSkipped
	default:
		return composableacceptance.TraceEventOutcomeError
	}
}

func ptr(value uint32) *uint32 {
	return &value
}

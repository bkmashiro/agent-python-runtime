package labview_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/composableacceptance"
	"github.com/bkmashiro/agent-python-runtime/research/labview"
)

func TestComposableAcceptanceProjectsBodyFreeStudyAndRuns(t *testing.T) {
	digest := func(c byte) string { return "sha256:" + strings.Repeat(string(c), 64) }
	corpus := composableacceptance.Corpus{SchemaVersion: composableacceptance.CorpusSchemaVersion, SourceCommit: strings.Repeat("a", 40), Model: "gpt-5.3-codex-spark"}
	for index := range 3 {
		corpus.Scenarios = append(corpus.Scenarios, composableacceptance.Scenario{
			ID: "scenario-" + string(rune('a'+index)), GuestSource: "values = [3, 1, 2]\nresult = sorted(values)",
			ChildPrograms: []composableacceptance.ChildProgram{{ID: "left", Role: "researcher", Source: "result = 'left child result'", ExpectedResult: "left child result", OutputPath: "left.txt"}, {ID: "right", Role: "reviewer", Source: "result = 'right child result'", ExpectedResult: "right child result", OutputPath: "right.txt"}}, Task: "bounded private repository scenario task",
			Files: []string{"a.go", "b.go"}, ChildAnalyses: []string{"private child A", "private child B"},
			RepeatedTransformation: "normalize twice", WaitBoundary: "wait then resume",
			Observation: "refresh named observation", SelectedChild: index % 2,
			ExpectedArtifact: "PRIVATE_ARTIFACT_BODY_" + string(rune('A'+index)), ProhibitedOutputs: []string{"private body"},
		})
	}
	_, corpusSHA, err := composableacceptance.EncodeCorpus(corpus)
	if err != nil {
		t.Fatal(err)
	}
	core := composableacceptance.Report{SchemaVersion: composableacceptance.ReportSchemaVersion, SourceCommit: strings.Repeat("b", 40), GuestArtifactSHA256: digest('c'), CorpusSHA256: corpusSHA, Model: corpus.Model}
	for _, scenario := range corpus.Scenarios {
		scenarioSHA, err := composableacceptance.ScenarioIdentity(scenario)
		if err != nil {
			t.Fatal(err)
		}
		for _, treatment := range []composableacceptance.Treatment{composableacceptance.TreatmentFresh, composableacceptance.TreatmentPrepared, composableacceptance.TreatmentCOW} {
			trace := []composableacceptance.TraceEvent{
				{Sequence: 1, SpanID: "run", AgentID: "orchestrator", AgentRole: "orchestrator", Type: composableacceptance.TraceEventTypeRunStart, Action: "run.start", Outcome: composableacceptance.TraceEventOutcomeStarted},
				{Sequence: 2, ParentSequence: traceParent(1), SpanID: "guest-run", ParentSpanID: "run", AgentID: "orchestrator", AgentRole: "orchestrator", Type: composableacceptance.TraceEventTypeGuestLifecycle, Action: "guest.run", Outcome: composableacceptance.TraceEventOutcomeOK},
				{Sequence: 3, ParentSequence: traceParent(2), SpanID: "run-terminal", ParentSpanID: "guest-run", AgentID: "orchestrator", AgentRole: "orchestrator", Type: composableacceptance.TraceEventTypeRunTerminal, Action: "run.terminal", Outcome: composableacceptance.TraceEventOutcomeOK, TerminalDisposition: "closed"},
			}
			core.Rows = append(core.Rows, composableacceptance.Row{
				ScenarioID: scenario.ID, ScenarioSHA256: scenarioSHA, Treatment: treatment, Status: "passed",
				OracleSHA256: composableacceptance.ArtifactIdentity(scenario.ExpectedArtifact), GuestCreated: 1, GuestDestroyed: 1,
				EvidenceScope: "direct_replay", ConformanceSHA256: digest('f'), TerminalDisposition: "closed", EvidenceComplete: true, Trace: trace,
			})
		}
	}
	composableacceptance.SortRows(core.Rows)
	projection, err := labview.ProjectComposableAcceptance(core, digest('9'))
	if err != nil || projection.Study.WorkloadCount != 3 || projection.Study.TreatmentCount != 3 || len(projection.Runs) != 9 || projection.Study.StatusTotals[0].Count != 9 {
		t.Fatalf("projection=%+v err=%v", projection.Study, err)
	}
	studyRaw, _, err := labview.Encode(labview.KindStudySummary, projection.Study)
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range projection.Runs {
		raw, _, err := labview.Encode(labview.KindRunDetail, run)
		if err != nil {
			t.Fatal(err)
		}
		studyRaw = append(studyRaw, raw...)
	}
	for _, forbidden := range []string{"PRIVATE_ARTIFACT_BODY", "private child", "bounded private repository scenario task"} {
		if bytes.Contains(studyRaw, []byte(forbidden)) {
			t.Fatalf("projection leaked %q", forbidden)
		}
	}
}

func traceParent(sequence uint32) *uint32 { return &sequence }

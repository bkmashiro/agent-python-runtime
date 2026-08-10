package agentic

import (
	"strings"
	"testing"
)

func TestBuildScriptedOracleProgramUsesStaticPreambleAndExactCalls(t *testing.T) {
	dataset, err := LoadRoutingDataset(routingDatasetRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	var task Task
	for _, candidate := range dataset.Tasks {
		if candidate.ID == "rd-006" {
			task = candidate
			break
		}
	}
	program, calls, err := BuildScriptedOracleProgram(task)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 5 || !strings.HasPrefix(program, "import json\nfrom host_tools import cd, cp, echo, touch, wc\n") {
		t.Fatalf("calls=%d program=%q", calls, program)
	}
	for _, expected := range []string{
		`cd(**json.loads("{\"folder\":\"Documents\"}"))`,
		`wc(**json.loads("{\"file_name\":\"list.txt\",\"mode\":\"l\"}"))`,
		`result = {"status": "completed"}`,
	} {
		if !strings.Contains(program, expected) {
			t.Fatalf("program missing %q: %s", expected, program)
		}
	}
}

func TestMechanismBaselineReportRejectsDecisionClaimsAndIncompleteCohort(t *testing.T) {
	report := MechanismBaselineReport{
		SchemaVersion:         "agentic-mechanism-baseline/v1",
		Status:                "mechanism_only_not_model_evaluation",
		AdmissionMode:         "internal_legacy_no_manifest",
		RepositoryCommit:      strings.Repeat("a", 40),
		GuestArtifactSHA256:   "sha256:" + strings.Repeat("b", 64),
		DatasetManifestSHA256: "sha256:" + strings.Repeat("c", 64),
		DatasetPlanSHA256:     "sha256:" + strings.Repeat("d", 64),
		Tasks: []MechanismTaskResult{{
			TaskID: "rd-001", Archetype: "direct_favored", ExpectedCalls: 1,
			Direct: MechanismSurfaceResult{HostRoundTrips: 1, TracePassed: true, FinalStatePassed: true},
			Python: MechanismSurfaceResult{PythonRuns: 1, CapabilityCalls: 1, TracePassed: true, FinalStatePassed: true},
		}},
	}
	if report.Validate() == nil {
		t.Fatal("incomplete routing cohort was accepted")
	}
	report.Tasks = make([]MechanismTaskResult, 6)
	for i := range report.Tasks {
		report.Tasks[i] = MechanismTaskResult{
			TaskID: "rd-00" + string(rune('1'+i)), Archetype: []string{"direct_favored", "direct_favored", "python_favored", "python_favored", "boundary", "boundary"}[i], ExpectedCalls: 1,
			Direct: MechanismSurfaceResult{HostRoundTrips: 1, TracePassed: true, FinalStatePassed: true},
			Python: MechanismSurfaceResult{PythonRuns: 1, CapabilityCalls: 1, TracePassed: true, FinalStatePassed: true},
		}
	}
	report.Summary = MechanismSummary{TaskCount: 6, DirectHostRoundTrips: 6, PythonRuns: 6, PythonCapabilityCalls: 6, AllOraclesPassed: true}
	report.ProhibitedClaims = []string{"model_quality", "token_reduction", "latency_reduction", "computer_replacement_rate", "profile_qualified_placement", "decision_eligible"}
	if err := report.Validate(); err != nil {
		t.Fatalf("valid mechanism report rejected: %v", err)
	}
	report.Status = "decision_eligible"
	if report.Validate() == nil {
		t.Fatal("scripted mechanism baseline was promoted to a decision claim")
	}
}

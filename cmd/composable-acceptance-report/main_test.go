package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/composableacceptance"
)

func TestProjectWebScenarioPublishesGuestSourceOnly(t *testing.T) {
	const secret = "PRIVATE-SCENARIO-BODY-SENTINEL"
	scenario := composableacceptance.Scenario{
		ID:                     "scenario-one",
		GuestSource:            "values = [3, 1, 2]\nresult = sorted(values)",
		Task:                   secret + "-task",
		Files:                  []string{secret + "-path"},
		ChildAnalyses:          []string{secret + "-child"},
		RepeatedTransformation: secret + "-transformation",
		WaitBoundary:           secret + "-wait",
		Observation:            secret + "-observation",
		SelectedChild:          2,
		ExpectedArtifact:       secret + "-artifact",
		ProhibitedOutputs:      []string{secret + "-prohibited"},
	}

	projected := projectWebScenario(scenario)
	encoded, err := json.Marshal(projected)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("private scenario body leaked: %s", encoded)
	}
	if projected.GuestSource != scenario.GuestSource {
		t.Fatalf("guest source missing: %+v", projected)
	}
	if projected.ID != scenario.ID || projected.FileCount != 1 || projected.ChildAnalysisCount != 1 || projected.SelectedChild != 2 {
		t.Fatalf("identity-only projection mismatch: %+v", projected)
	}
	if !projected.HasRepeatedTransformation || !projected.HasWaitBoundary || !projected.HasObservation {
		t.Fatalf("presence metadata missing: %+v", projected)
	}
}

package wazero

import "testing"

func TestColdIOEvidenceRejectsContradictoryOrUnboundedState(t *testing.T) {
	valid := ColdIOEvidence{
		SchemaVersion: ColdIOEvidenceSchemaVersion,
		Selected:      true,
		State:         ColdIOTerminal,
		Waits:         1,
		ColdAttempts:  1,
		ColdSucceeded: 1,
		Resumes:       1,
		AdvisedBytes:  4096,
		Blockers:      []string{},
	}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	mutations := []func(*ColdIOEvidence){
		func(value *ColdIOEvidence) { value.SchemaVersion = "wrong" },
		func(value *ColdIOEvidence) { value.State = "unknown" },
		func(value *ColdIOEvidence) { value.Resumes = 2 },
		func(value *ColdIOEvidence) { value.ColdSucceeded = 2 },
		func(value *ColdIOEvidence) { value.AdviceFailures = 1 },
		func(value *ColdIOEvidence) { value.AdvisedBytes = 0 },
		func(value *ColdIOEvidence) { value.Blockers = nil },
		func(value *ColdIOEvidence) { value.Blockers = []string{"private/path"} },
	}
	for index, mutate := range mutations {
		candidate := valid
		candidate.Blockers = append([]string(nil), valid.Blockers...)
		mutate(&candidate)
		if candidate.Validate() == nil {
			t.Fatalf("mutation %d accepted: %+v", index, candidate)
		}
	}
}

func TestDisabledColdIOEvidenceRejectsCounters(t *testing.T) {
	base := ColdIOEvidence{SchemaVersion: ColdIOEvidenceSchemaVersion, State: ColdIODisabled, Blockers: []string{}}
	for _, mutate := range []func(*ColdIOEvidence){
		func(value *ColdIOEvidence) { value.ColdSucceeded = 1 },
		func(value *ColdIOEvidence) { value.PageOutSucceeded = 1 },
		func(value *ColdIOEvidence) { value.AdvisedBytes = 4096 },
		func(value *ColdIOEvidence) { value.AdviceFailures = 1 },
	} {
		candidate := base
		mutate(&candidate)
		if candidate.Validate() == nil {
			t.Fatalf("accepted disabled evidence: %+v", candidate)
		}
	}
}

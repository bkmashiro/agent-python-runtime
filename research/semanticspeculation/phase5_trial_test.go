package semanticspeculation

import (
	"bytes"
	"encoding/json"
	"testing"
)

func validPhase5TrialRecord(t *testing.T, treatment, profile string) Phase5TrialRecord {
	t.Helper()
	caseID := "scalar_add_16_gap0"
	var candidate Phase5Case
	for _, item := range Phase5Cases() {
		if item.ID == caseID {
			candidate = item
			break
		}
	}
	stages := make([]Phase5StageObservation, 0, len(Phase5TimingStageNames()))
	for _, name := range Phase5TimingStageNames() {
		stages = append(stages, Phase5StageObservation{Name: name, Disposition: Phase5StageNotApplicable})
	}
	setStage := func(name, disposition string, start, end uint64, critical bool) {
		for index := range stages {
			if stages[index].Name == name {
				stages[index] = Phase5StageObservation{Name: name, Disposition: disposition, StartedOffsetNanos: start, EndedOffsetNanos: end, DurationNanos: end - start, OnCriticalPath: critical}
				return
			}
		}
	}
	criticalStart := uint64(0)
	if profile == "preprovisioned_equivalent_capacity" {
		criticalStart = 100
	}
	setStage("finalization_gap", Phase5StageMeasured, criticalStart, criticalStart+10, true)
	setStage("final_guest_provision", Phase5StageMeasured, criticalStart+10, criticalStart+20, true)
	setStage("final_execution", Phase5StageMeasured, criticalStart+20, criticalStart+30, true)
	setStage("teardown", Phase5StageMeasured, criticalStart+30, criticalStart+40, true)
	record := Phase5TrialRecord{
		SchemaVersion: Phase5TrialRecordSchemaVersion, StudyID: Phase5StudyID, CaseMatrixIdentity: Phase5CaseMatrixIdentity, PreregistrationIdentity: Phase5PreregistrationIdentity, HarnessIdentity: syntheticDigest([]byte("harness-v1")), GuestArtifactSHA256: Phase5GuestArtifactSHA256,
		RunID: phase5OpaqueRunID(Phase5CampaignCoordinate{Profile: profile, CaseID: caseID, Treatment: treatment, TrialIndex: 1}), Profile: profile, CaseID: caseID, Treatment: treatment, TrialIndex: 1, SourceSHA256: candidate.SourceSHA256, RegionSourceSHA256: candidate.RegionSourceSHA256,
		ExpectedDisposition: candidate.ExpectedDisposition, ExpectedOutcome: candidate.ExpectedOutcome, ActualDisposition: "ready_consumed", ActualOutcome: "success", ResultSHA256: syntheticDigest(candidate.ExpectedResult), LogsSHA256: syntheticDigest([]byte("[]")), AuthorityTerminalDisposition: "none", WorkspaceTerminalDisposition: "unmounted",
		CriticalPathStartedOffsetNanos: criticalStart, TrialEndedOffsetNanos: criticalStart + 40, TotalCriticalPathNanos: 40, UnattributedCriticalPathNanos: 0, Stages: stages, FormalGuestExecutions: 1, FinalRuntimeInitCount: 1, PeakResidentMemoryBytes: 1024,
	}
	if profile == "preprovisioned_equivalent_capacity" {
		setStage("final_guest_provision", Phase5StagePreclock, 0, 100, false)
		record.Stages = stages
		record.DiscardedCapacityBytes = 4096
	}
	if treatment == "prepared_region_derived" {
		record.DecisionSHA256 = syntheticDigest([]byte("decision"))
		record.PatchSHA256 = syntheticDigest([]byte("patch"))
		record.CapsuleSHA256 = syntheticDigest([]byte("capsule"))
		record.SelectionSHA256 = syntheticDigest([]byte("selection"))
		record.DerivedASTSHA256 = syntheticDigest([]byte("ast"))
		record.HelperClaimCount = 1
		record.CapsuleConsumedCount = 1
		record.CapsuleBytes = 2
		record.AnalyzerSessionCount = 1
		record.AnalyzerRuntimeInitCount = 1
		record.ScratchGuestExecutions = 1
		record.ScratchRuntimeInitCount = 1
		record.FormalGuestExecutions = 2
		setStage("analysis", Phase5StageMeasured, criticalStart, criticalStart+3, true)
		setStage("patch_emission", Phase5StageMeasured, criticalStart+3, criticalStart+4, true)
		setStage("scratch_guest_provision", Phase5StageMeasured, criticalStart+4, criticalStart+6, true)
		setStage("scratch_execution", Phase5StageMeasured, criticalStart+6, criticalStart+8, true)
		setStage("capsule_seal_transport", Phase5StageMeasured, criticalStart+8, criticalStart+9, true)
		setStage("final_selection_validation", Phase5StageMeasured, criticalStart+10, criticalStart+11, true)
		setStage("final_patch_compile_load", Phase5StageMeasured, criticalStart+11, criticalStart+12, true)
		if profile == "cold_end_to_end" {
			setStage("analyzer_provision", Phase5StageMeasured, criticalStart, criticalStart+2, true)
		} else {
			setStage("analyzer_provision", Phase5StagePreclock, 0, 30, false)
			setStage("scratch_guest_provision", Phase5StagePreclock, 30, 60, false)
			setStage("final_guest_provision", Phase5StagePreclock, 60, 100, false)
		}
		record.Stages = stages
	}
	union := phase5CriticalStageUnion(stages, criticalStart, record.TrialEndedOffsetNanos)
	record.UnattributedCriticalPathNanos = record.TotalCriticalPathNanos - union
	return record
}

func TestPhase5TimingStageNamesReturnsACopy(t *testing.T) {
	names := Phase5TimingStageNames()
	names[0] = "drifted"
	if Phase5TimingStageNames()[0] != "finalization_gap" {
		t.Fatal("caller mutated frozen stage names")
	}
}

func TestPhase5TrialRecordAcceptsOriginalAndOverlappingDerivedTimelines(t *testing.T) {
	for _, profile := range phase5Profiles {
		for _, treatment := range phase5Treatments {
			record := validPhase5TrialRecord(t, treatment, profile)
			raw, err := EncodePhase5TrialRecord(record)
			if err != nil {
				t.Fatalf("%s/%s encode: %v", profile, treatment, err)
			}
			decoded, err := DecodePhase5TrialRecord(raw, syntheticDigest([]byte("harness-v1")))
			if err != nil {
				t.Fatalf("%s/%s decode: %v", profile, treatment, err)
			}
			if decoded.RunID != record.RunID || decoded.TotalCriticalPathNanos != 40 {
				t.Fatalf("decoded=%+v", decoded)
			}
		}
	}
}

func TestPhase5TrialRecordRejectsBodiesUnknownFieldsAndNoncanonicalJSON(t *testing.T) {
	record := validPhase5TrialRecord(t, "original_unchanged", "cold_end_to_end")
	raw, err := EncodePhase5TrialRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	unknown := append([]byte(nil), raw[:len(raw)-1]...)
	unknown = append(unknown, []byte(`,"result_body":42}`)...)
	if _, err := DecodePhase5TrialRecord(unknown, record.HarnessIdentity); err == nil {
		t.Fatal("accepted result body")
	}
	if _, err := DecodePhase5TrialRecord(append(raw, '\n'), record.HarnessIdentity); err == nil {
		t.Fatal("accepted trailing data")
	}
	var generic map[string]any
	if json.Unmarshal(raw, &generic) != nil {
		t.Fatal("invalid encoded JSON")
	}
	pretty, _ := json.MarshalIndent(generic, "", "  ")
	if _, err := DecodePhase5TrialRecord(pretty, record.HarnessIdentity); err == nil {
		t.Fatal("accepted noncanonical JSON")
	}
	for _, forbidden := range [][]byte{[]byte("seed ="), []byte(`"result_body"`), []byte(`"source"`), []byte(`"traceback"`), []byte(`"logs"`)} {
		if bytes.Contains(raw, forbidden) {
			t.Fatalf("record contains body %q", forbidden)
		}
	}
}

func TestPhase5TrialRecordRejectsIdentityTimelineAndLifecycleDrift(t *testing.T) {
	mutations := []func(*Phase5TrialRecord){
		func(record *Phase5TrialRecord) { record.HarnessIdentity = syntheticDigest([]byte("other")) },
		func(record *Phase5TrialRecord) { record.SourceSHA256 = syntheticDigest([]byte("other")) },
		func(record *Phase5TrialRecord) { record.Stages[0].DurationNanos++ },
		func(record *Phase5TrialRecord) { record.UnattributedCriticalPathNanos++ },
		func(record *Phase5TrialRecord) { record.HelperClaimCount = 2 },
		func(record *Phase5TrialRecord) { record.CapsuleConsumedCount = 0 },
		func(record *Phase5TrialRecord) {
			record.Stages[1], record.Stages[2] = record.Stages[2], record.Stages[1]
		},
	}
	for index, mutate := range mutations {
		record := validPhase5TrialRecord(t, "prepared_region_derived", "cold_end_to_end")
		expectedHarness := record.HarnessIdentity
		mutate(&record)
		raw, _ := json.Marshal(record)
		if _, err := DecodePhase5TrialRecord(raw, expectedHarness); err == nil {
			t.Fatalf("mutation %d accepted", index)
		}
	}
}

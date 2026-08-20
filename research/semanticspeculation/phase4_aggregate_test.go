package semanticspeculation

import (
	"testing"
	"time"
)

func TestAggregatePhase4CampaignRequiresCompleteMechanismAndProfileEconomics(t *testing.T) {
	records := make([]Phase4TrialRecord, 0, 360)
	for _, coordinate := range Phase4CampaignCoordinates() {
		total := uint64(1500000000)
		if coordinate.Treatment == "serial_whole_file" {
			total = 2000000000
		}
		if coordinate.Treatment == "semantic_pre_dispatch" {
			total = 1000000000
		}
		record := Phase4TrialRecord{SchemaVersion: Phase4TrialRecordSchemaVersion, Profile: coordinate.Profile, CaseID: coordinate.CaseID, Treatment: coordinate.Treatment, TrialIndex: coordinate.TrialIndex, ExecutionTimeoutNanos: uint64(30 * time.Second), TotalElapsedNanos: total, FormalExecutionNanos: 1, FinalProgramOutcome: "success", ResultSHA256: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", LogicalCallCount: 1, PhysicalAttemptCount: 1, ReadyBeforeFinalize: 1, AuthorityTerminalDisposition: "consumed", WorkspaceTerminalDisposition: "published", FormalGuestExecutions: 1}
		if coordinate.Treatment == "semantic_pre_dispatch" {
			record.AnalyzerSessionCount = 1
			record.AnalyzerInvocations = 1
			if coordinate.Profile == "preprovisioned_equivalent_capacity" {
				record.PreparedOrCOWHitCount = 1
				record.ProvisioningNanos = 1
				if coordinate.CaseID == "pure_local_gap6_control" {
					record.AnalyzerInvocations = 0
					record.PreparedOrCOWHitCount = 0
				}
			}
		}
		records = append(records, record)
	}
	report, err := AggregatePhase4Campaign(records)
	if err != nil {
		t.Fatal(err)
	}
	if !report.MechanismPassed || !report.EconomicsPassed || report.RecordCount != 360 || report.MatchedCells != 120 {
		t.Fatalf("report=%+v", report)
	}
	records[0].Profile = "cold_end_to_end"
	if _, err := AggregatePhase4Campaign(records); err == nil {
		t.Fatal("duplicate/missing coordinate accepted")
	}
}

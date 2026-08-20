package semanticspeculation

import (
	"context"
	"testing"
	"time"
)

func TestAggregatePhase5CampaignAppliesFrozenMatchedEconomicsGate(t *testing.T) {
	harness := syntheticDigest([]byte("phase5-frozen-harness"))
	records := phase5AggregateFixture(t, harness, 1000*time.Nanosecond, 10*time.Nanosecond)
	report, err := AggregatePhase5Campaign(records, harness)
	if err != nil {
		t.Fatal(err)
	}
	if !report.MechanismPassed || !report.EconomicsPassed || report.RecordCount != 80 || report.MatchedCells != 40 || len(report.Coordinates) != 8 || len(report.ProfileGates) != 2 || report.NoGoAction != "" {
		t.Fatalf("passing report=%+v", report)
	}
	for _, gate := range report.ProfileGates {
		if gate.EligibleCoordinates != 4 || gate.PassingCoordinates != 4 || !gate.EconomicsPassed {
			t.Fatalf("profile gate=%+v", gate)
		}
	}
	for _, coordinate := range report.Coordinates {
		if coordinate.MatchedTrials != 5 || coordinate.PositiveSavingTrials != 5 || coordinate.MedianNetSavingNanos <= 0 || !coordinate.Passed {
			t.Fatalf("coordinate=%+v", coordinate)
		}
	}
}

func TestAggregatePhase5CampaignFailsClosedAndRecordsNoGo(t *testing.T) {
	harness := syntheticDigest([]byte("phase5-frozen-harness"))
	records := phase5AggregateFixture(t, harness, 10*time.Nanosecond, 1000*time.Nanosecond)
	report, err := AggregatePhase5Campaign(records, harness)
	if err != nil {
		t.Fatal(err)
	}
	if report.EconomicsPassed || report.NoGoAction != "record_no_go_retain_original_execution_and_do_not_expand_transport_or_authority" {
		t.Fatalf("no-go report=%+v", report)
	}
	if _, err := AggregatePhase5Campaign(records[:len(records)-1], harness); err == nil {
		t.Fatal("incomplete campaign accepted")
	}
	duplicate := append([]Phase5TrialRecord(nil), records...)
	duplicate[len(duplicate)-1] = duplicate[0]
	if _, err := AggregatePhase5Campaign(duplicate, harness); err == nil {
		t.Fatal("duplicate campaign accepted")
	}
}

func TestAggregatePhase5CampaignSeparatesMatchedMechanismParityFromEconomics(t *testing.T) {
	harness := syntheticDigest([]byte("phase5-frozen-harness"))
	records := phase5AggregateFixture(t, harness, 1000*time.Nanosecond, 10*time.Nanosecond)
	for index := range records {
		if records[index].Treatment == "prepared_region_derived" {
			records[index].LogsSHA256 = syntheticDigest([]byte("logical-log-drift"))
			break
		}
	}
	report, err := AggregatePhase5Campaign(records, harness)
	if err != nil {
		t.Fatal(err)
	}
	if report.MechanismPassed || !report.EconomicsPassed || report.NoGoAction != "record_no_go_retain_original_execution_and_do_not_expand_transport_or_authority" {
		t.Fatalf("mechanism/economics separation drift: %+v", report)
	}
}

func phase5AggregateFixture(t *testing.T, harness string, originalDuration, derivedDuration time.Duration) []Phase5TrialRecord {
	t.Helper()
	cases := map[string]Phase5Case{}
	for _, candidate := range Phase5Cases() {
		cases[candidate.ID] = candidate
	}
	records := make([]Phase5TrialRecord, 0, 80)
	for _, coordinate := range Phase5CampaignCoordinates() {
		candidate := cases[coordinate.CaseID]
		clock := newPhase5FakeClock()
		adapter := &fakePhase5Operations{clock: clock, resultSHA: syntheticDigest(candidate.ExpectedResult), originalDuration: originalDuration, derivedDuration: derivedDuration}
		raw, err := RunPhase5Coordinate(context.Background(), coordinate, Phase5RunnerConfig{HarnessIdentity: harness, Clock: clock.Now, ResidentBytes: func() uint64 { return 4096 }}, adapter)
		if err != nil {
			t.Fatalf("coordinate=%+v: %v", coordinate, err)
		}
		record, err := DecodePhase5TrialRecord(raw, harness)
		if err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	return records
}

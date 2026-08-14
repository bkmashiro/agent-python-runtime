package main

import "testing"

func TestValidateReportRejectsInternallyConsistentMutationWithoutReseal(t *testing.T) {
	rows := []trial{
		{Condition: "baseline", DurationMicros: 2000, LogicalCalls: 1, PhysicalCalls: 1, ResultSHA256: digest([]byte(`"fixture"`))},
		{Condition: "semantic_pre_dispatch", DurationMicros: 1000, LogicalCalls: 1, PhysicalCalls: 1, PhysicalIssues: 1, PhysicalStarts: 1, PhysicalFinishes: 1, ResultSHA256: digest([]byte(`"fixture"`))},
		{Condition: "baseline", DurationMicros: 2100, LogicalCalls: 1, PhysicalCalls: 1, ResultSHA256: digest([]byte(`"fixture"`))},
		{Condition: "semantic_pre_dispatch", DurationMicros: 1100, LogicalCalls: 1, PhysicalCalls: 1, PhysicalIssues: 1, PhysicalStarts: 1, PhysicalFinishes: 1, ResultSHA256: digest([]byte(`"fixture"`))},
		{Condition: "baseline", DurationMicros: 2200, LogicalCalls: 1, PhysicalCalls: 1, ResultSHA256: digest([]byte(`"fixture"`))},
		{Condition: "semantic_pre_dispatch", DurationMicros: 1200, LogicalCalls: 1, PhysicalCalls: 1, PhysicalIssues: 1, PhysicalStarts: 1, PhysicalFinishes: 1, ResultSHA256: digest([]byte(`"fixture"`))},
	}
	report := report{
		SchemaVersion: reportSchema, ArtifactSHA256: digest([]byte("artifact")),
		SourceSHA256: digest([]byte("source")), CapabilityPlanSHA256: digest([]byte("plan")),
		TrialsPerCondition: 3, PhysicalDelayMicros: 1000,
		BaselineMedianMicros: 2100, OptimizedMedianMicros: 1100, MedianSavingsMicros: 1000,
		EquivalentResults: true, NoDuplicatePhysicalCall: true, Trials: rows,
	}
	report.ContentSHA256 = sealReport(report)
	if err := validateReport(report); err != nil {
		t.Fatalf("valid report: %v", err)
	}
	report.Trials[1].PhysicalStarts = 2
	if err := validateReport(report); err == nil {
		t.Fatal("mutated report unexpectedly validated")
	}
	report.Trials[1].PhysicalStarts = 1
	report.ArtifactSHA256 = ""
	report.ContentSHA256 = sealReport(report)
	if err := validateReport(report); err == nil {
		t.Fatal("resealed report without artifact provenance unexpectedly validated")
	}
}

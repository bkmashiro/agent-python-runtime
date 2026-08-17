package main

import "testing"

func TestValidateReportRejectsInternallyConsistentMutationWithoutReseal(t *testing.T) {
	rows := []trial{
		{Condition: "baseline", DurationMicros: 2000, LogicalCalls: 1, PhysicalCalls: 1, ResultSHA256: digest([]byte(`"` + dayTripTrainResult + `"`))},
		{Condition: "semantic_pre_dispatch", DurationMicros: 1000, LogicalCalls: 1, PhysicalCalls: 1, PhysicalIssues: 1, PhysicalStarts: 1, PhysicalFinishes: 1, ResultSHA256: digest([]byte(`"` + dayTripTrainResult + `"`))},
		{Condition: "baseline", DurationMicros: 2100, LogicalCalls: 1, PhysicalCalls: 1, ResultSHA256: digest([]byte(`"` + dayTripTrainResult + `"`))},
		{Condition: "semantic_pre_dispatch", DurationMicros: 1100, LogicalCalls: 1, PhysicalCalls: 1, PhysicalIssues: 1, PhysicalStarts: 1, PhysicalFinishes: 1, ResultSHA256: digest([]byte(`"` + dayTripTrainResult + `"`))},
		{Condition: "baseline", DurationMicros: 2200, LogicalCalls: 1, PhysicalCalls: 1, ResultSHA256: digest([]byte(`"` + dayTripTrainResult + `"`))},
		{Condition: "semantic_pre_dispatch", DurationMicros: 1200, LogicalCalls: 1, PhysicalCalls: 1, PhysicalIssues: 1, PhysicalStarts: 1, PhysicalFinishes: 1, ResultSHA256: digest([]byte(`"` + dayTripTrainResult + `"`))},
		{Condition: "baseline", DurationMicros: 2300, LogicalCalls: 1, PhysicalCalls: 1, ResultSHA256: digest([]byte(`"` + dayTripTrainResult + `"`))},
		{Condition: "semantic_pre_dispatch", DurationMicros: 1300, LogicalCalls: 1, PhysicalCalls: 1, PhysicalIssues: 1, PhysicalStarts: 1, PhysicalFinishes: 1, ResultSHA256: digest([]byte(`"` + dayTripTrainResult + `"`))},
		{Condition: "baseline", DurationMicros: 2400, LogicalCalls: 1, PhysicalCalls: 1, ResultSHA256: digest([]byte(`"` + dayTripTrainResult + `"`))},
		{Condition: "semantic_pre_dispatch", DurationMicros: 1400, LogicalCalls: 1, PhysicalCalls: 1, PhysicalIssues: 1, PhysicalStarts: 1, PhysicalFinishes: 1, ResultSHA256: digest([]byte(`"` + dayTripTrainResult + `"`))},
	}
	report := report{
		SchemaVersion: reportSchema, ArtifactSHA256: digest([]byte("artifact")),
		SourceSHA256: digest([]byte("source")), CapabilityPlanSHA256: digest([]byte("plan")),
		TrialsPerCondition: 5, PhysicalDelayMicros: 1000,
		BaselineMedianMicros: 2200, OptimizedMedianMicros: 1200, MedianSavingsMicros: 1000,
		EquivalentResults: true, NoDuplicatePhysicalCall: true, Trials: rows,
	}
	expected := reportProvenance{
		ArtifactSHA256: report.ArtifactSHA256, SourceSHA256: report.SourceSHA256,
		CapabilityPlanSHA256: report.CapabilityPlanSHA256,
	}
	report.ContentSHA256 = sealReport(report)
	if err := validateReport(report, expected); err != nil {
		t.Fatalf("valid report: %v", err)
	}
	short := report
	short.TrialsPerCondition = 3
	short.Trials = short.Trials[:6]
	short.BaselineMedianMicros = 2100
	short.OptimizedMedianMicros = 1100
	short.MedianSavingsMicros = 1000
	short.ContentSHA256 = sealReport(short)
	if err := validateReport(short, expected); err == nil {
		t.Fatal("resealed three-trial report unexpectedly validated")
	}
	report.Trials[1].PhysicalStarts = 2
	if err := validateReport(report, expected); err == nil {
		t.Fatal("mutated report unexpectedly validated")
	}
	report.Trials[1].PhysicalStarts = 1
	report.ArtifactSHA256 = digest([]byte("plausible-but-unbound-artifact"))
	report.ContentSHA256 = sealReport(report)
	if err := validateReport(report, expected); err == nil {
		t.Fatal("resealed report with unbound artifact provenance unexpectedly validated")
	}
}

func TestDayTripSourceUsesProvenTrainRead(t *testing.T) {
	if dayTripSource != "result = travel.trains('london', 'oxford', 'saturday')\n" {
		t.Fatalf("day-trip source=%q", dayTripSource)
	}
	spec := dayTripCapabilitySpec()
	if spec.Name != "travel.trains" || spec.Python == nil || spec.Python.Module != "travel" || spec.Python.Method != "trains" || spec.PreDispatch == nil {
		t.Fatalf("day-trip train spec=%+v", spec)
	}
}

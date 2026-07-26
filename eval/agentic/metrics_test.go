package agentic

import "testing"

func boolPointer(value bool) *bool { return &value }
func intPointer(value int) *int    { return &value }

func TestDeriveTrialMetricsSeparatesOutcomeFromStrictTrace(t *testing.T) {
	result := TrialResult{
		ErrorCode: "",
		ToolCalls: 5,
		Passed:    false,
		StatefulScore: &StatefulScore{
			Passed: false, TracePassed: false, FinalStatePassed: true,
			ExpectedCalls: 4, ActualCalls: 5,
		},
	}
	metrics, err := DeriveTrialMetrics(result)
	if err != nil {
		t.Fatal(err)
	}
	if !metrics.OutcomeSuccess || metrics.StrictPass || metrics.TraceExact == nil || *metrics.TraceExact ||
		metrics.FinalStateCorrect == nil || !*metrics.FinalStateCorrect || metrics.ExpectedCalls == nil ||
		*metrics.ExpectedCalls != 4 || metrics.ActualCalls != 5 || metrics.ExtraCalls != 1 {
		t.Fatalf("metrics=%+v", metrics)
	}
}

func TestDeriveTrialMetricsTreatsExecutionErrorAsOutcomeFailure(t *testing.T) {
	result := TrialResult{
		ErrorCode: "host_application_error",
		ToolCalls: 4,
		Passed:    false,
		StatefulScore: &StatefulScore{
			Passed: true, TracePassed: true, FinalStatePassed: true,
			ExpectedCalls: 4, ActualCalls: 4,
		},
	}
	metrics, err := DeriveTrialMetrics(result)
	if err != nil {
		t.Fatal(err)
	}
	if metrics.OutcomeSuccess || metrics.StrictPass || metrics.ExtraCalls != 0 {
		t.Fatalf("metrics=%+v", metrics)
	}
}

func TestDeriveTrialMetricsForStatelessSuccess(t *testing.T) {
	result := TrialResult{ToolCalls: 3, Passed: true, StatelessScore: &CallScore{Passed: true}}
	metrics, err := DeriveTrialMetrics(result)
	if err != nil {
		t.Fatal(err)
	}
	if !metrics.OutcomeSuccess || !metrics.StrictPass || metrics.TraceExact != nil ||
		metrics.FinalStateCorrect != nil || metrics.ExpectedCalls != nil || metrics.ActualCalls != 3 || metrics.ExtraCalls != 0 {
		t.Fatalf("metrics=%+v", metrics)
	}
}

func TestDeriveTrialMetricsRejectsAmbiguousScore(t *testing.T) {
	for _, result := range []TrialResult{
		{},
		{StatefulScore: &StatefulScore{}, StatelessScore: &CallScore{}},
	} {
		if _, err := DeriveTrialMetrics(result); err == nil {
			t.Fatalf("ambiguous result accepted: %+v", result)
		}
	}
}

func TestTrialMetricsEqualHandlesOptionalAxes(t *testing.T) {
	left := TrialMetrics{OutcomeSuccess: true, StrictPass: false, TraceExact: boolPointer(false), FinalStateCorrect: boolPointer(true), ExpectedCalls: intPointer(4), ActualCalls: 5, ExtraCalls: 1}
	right := left
	if !trialMetricsEqual(left, right) {
		t.Fatal("equal metrics rejected")
	}
	right.TraceExact = boolPointer(true)
	if trialMetricsEqual(left, right) {
		t.Fatal("different metrics accepted")
	}
}

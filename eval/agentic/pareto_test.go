package agentic

import "testing"

func TestCompareHybridCounterfactualsSeparatesOutcomeAndResourceRegret(t *testing.T) {
	direct := RouteOutcome{OutcomeSuccess: true, StrictPass: true, ProviderCalls: 3, TotalTokens: 300}
	python := RouteOutcome{OutcomeSuccess: true, StrictPass: false, ProviderCalls: 1, TotalTokens: 100}

	got, err := CompareHybridCounterfactuals(direct, python, RouteOutcome{OutcomeSuccess: true, StrictPass: false, ProviderCalls: 2, TotalTokens: 220})
	if err != nil {
		t.Fatal(err)
	}
	if got.OutcomeRegret != 0 || got.StrictRegret != 1 || got.ProviderCallRegret != 1 || got.TokenRegret != 120 || !got.ParetoDominated || got.DominatingArm != "python" {
		t.Fatalf("comparison=%+v", got)
	}

	failed, err := CompareHybridCounterfactuals(direct, python, RouteOutcome{OutcomeSuccess: false, StrictPass: false, ProviderCalls: 1, TotalTokens: 50})
	if err != nil || failed.OutcomeRegret != 1 || failed.ParetoDominated {
		t.Fatalf("failed=%+v err=%v", failed, err)
	}
}

func TestAggregateHybridRegretKeepsDimensionsSeparate(t *testing.T) {
	report, err := AggregateHybridRegret([]HybridCounterfactualComparison{
		{OutcomeRegret: 0, StrictRegret: 1, ProviderCallRegret: 1, TokenRegret: 120, ParetoDominated: true, DominatingArm: "python"},
		{OutcomeRegret: 1, StrictRegret: 0, ProviderCallRegret: 0, TokenRegret: 0},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Tasks != 2 || report.OutcomeRegretTasks != 1 || report.StrictRegretTasks != 1 || report.ParetoDominatedTasks != 1 || report.TotalProviderCallRegret != 1 || report.TotalTokenRegret != 120 {
		t.Fatalf("report=%+v", report)
	}
}

func TestCompareHybridCounterfactualsRejectsInvalidFacts(t *testing.T) {
	_, err := CompareHybridCounterfactuals(RouteOutcome{TotalTokens: -1}, RouteOutcome{}, RouteOutcome{})
	if err == nil {
		t.Fatal("negative usage accepted")
	}
	_, err = CompareHybridCounterfactuals(RouteOutcome{StrictPass: true}, RouteOutcome{}, RouteOutcome{})
	if err == nil {
		t.Fatal("strict pass without outcome success accepted")
	}
	_, err = AggregateHybridRegret(nil)
	if err == nil {
		t.Fatal("empty report accepted")
	}
}

func TestFailedCheapArmIsExcludedFromResourceFrontier(t *testing.T) {
	direct := RouteOutcome{OutcomeSuccess: false, ProviderCalls: 0, TotalTokens: 0}
	python := RouteOutcome{OutcomeSuccess: true, ProviderCalls: 2, TotalTokens: 100}
	hybrid := RouteOutcome{OutcomeSuccess: true, ProviderCalls: 3, TotalTokens: 220}
	got, err := CompareHybridCounterfactuals(direct, python, hybrid)
	if err != nil || got.ProviderCallRegret != 1 || got.TokenRegret != 120 || !got.ParetoDominated || got.DominatingArm != "python" {
		t.Fatalf("comparison=%+v err=%v", got, err)
	}
}

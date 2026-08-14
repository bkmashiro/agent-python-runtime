package semantic_test

import (
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

func TestBuildWholeRunPlanIsConservativeStableAndCensusBound(t *testing.T) {
	analysis := validAnalysis()
	analysis.ModuleEffects = semantic.EffectSummary{}
	analysis.Functions[0].Effects = semantic.EffectSummary{}
	plan, census, err := semantic.BuildWholeRunPlan(analysis, semantic.WholeRunConfig{
		Dependencies: []semantic.Dependency{
			{Kind: semantic.DependencyImmutableRoot, IdentitySHA256: digest('b')},
			{Kind: semantic.DependencyCanonicalInputs, IdentitySHA256: digest('a')},
		},
		InputsCanonical: true, OutputsCanonical: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	region := plan.Regions[0]
	if !region.Reusable() || census.ReusableRegions != 1 || census.RejectedRegions != 0 {
		t.Fatalf("region=%+v census=%+v", region, census)
	}
	if region.Dependencies[0].Kind != semantic.DependencyCanonicalInputs {
		t.Fatalf("dependencies not canonical: %+v", region.Dependencies)
	}
	second, secondCensus, err := semantic.BuildWholeRunPlan(analysis, semantic.WholeRunConfig{
		Dependencies: []semantic.Dependency{
			{Kind: semantic.DependencyCanonicalInputs, IdentitySHA256: digest('a')},
			{Kind: semantic.DependencyImmutableRoot, IdentitySHA256: digest('b')},
		},
		InputsCanonical: true, OutputsCanonical: true,
	})
	if err != nil || second.Regions[0].ID != region.ID || secondCensus.PlanSHA256 != census.PlanSHA256 {
		t.Fatalf("unstable plan err=%v second=%+v census=%+v", err, second, secondCensus)
	}
}

func TestBuildWholeRunPlanPropagatesUnusedUnknownFunctionAsBarrier(t *testing.T) {
	analysis := validAnalysis()
	analysis.Functions[0].Effects = semantic.EffectSummary{MayBeUnknown: true}
	analysis.Barriers = []semantic.Barrier{{
		Code: semantic.BarrierDynamicCall, FunctionID: analysis.Functions[0].ID,
		Span: analysis.Functions[0].Span,
	}}
	plan, census, err := semantic.BuildWholeRunPlan(analysis, semantic.WholeRunConfig{
		InputsCanonical: true, OutputsCanonical: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	region := plan.Regions[0]
	if region.Reusable() || !region.Effects.MayBeUnknown || len(region.RejectionReasons) != 1 || census.RejectedRegions != 1 {
		t.Fatalf("region=%+v census=%+v", region, census)
	}
}

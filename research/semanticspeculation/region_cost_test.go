package semanticspeculation

import (
	"strings"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

func TestBuildPhase4RegionCostProgramUsesOnlyDependencyClosureAndExactFocus(t *testing.T) {
	candidate := Phase4RegionCase{ID: "positive", Source: "seed = 3\nvalue = seed * 3 * 3\nremote = sources.demo_catalog()\nresult = value\n", FocusRegionIndex: 1, ExpectedLocalReusable: true}
	clean := semantic.EffectSummary{}
	analysis := semantic.Analysis{CandidateRegions: []semantic.CandidateRegion{
		{ID: "seed", Kind: semantic.CandidateRegionStraightLine, Span: semantic.SourceSpan{StartLine: 1, EndLine: 1, EndColumn: 8}, LiveInsCanonical: true, LiveOutsCanonical: true, Effects: clean, ControlPredecessors: []string{}, DataDependencies: []semantic.RegionDataDependency{}, LiveIns: []string{}, LiveOuts: []string{"seed"}, CapabilityOccurrences: []string{}, Barriers: []semantic.BarrierCode{}, RejectionReasons: []semantic.CandidateRejection{}},
		{ID: "focus", Kind: semantic.CandidateRegionStraightLine, Span: semantic.SourceSpan{StartLine: 2, EndLine: 2, EndColumn: uint32(len("value = seed * 3 * 3"))}, LiveInsCanonical: true, LiveOutsCanonical: true, Effects: clean, ControlPredecessors: []string{"seed"}, DataDependencies: []semantic.RegionDataDependency{{Name: "seed", ProducerRegionID: "seed"}}, LiveIns: []string{"seed"}, LiveOuts: []string{"value"}, CapabilityOccurrences: []string{}, Barriers: []semantic.BarrierCode{}, RejectionReasons: []semantic.CandidateRejection{}},
	}}
	program, err := BuildPhase4RegionCostProgram(candidate, analysis)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"seed = 3", "value = seed * 3 * 3", "perf_counter_ns", "constructed_region_execution_nanos"} {
		if !strings.Contains(program, required) {
			t.Fatalf("missing %q in %s", required, program)
		}
	}
	if strings.Contains(program, "sources.demo_catalog") || strings.Contains(program, "result = value") {
		t.Fatalf("non-closure source leaked: %s", program)
	}
}

func TestBuildPhase4RegionCostProgramRejectsNegativeFocus(t *testing.T) {
	candidate := Phase4RegionCase{ID: "negative", Source: "result = unknown()\n", FocusRegionIndex: 0}
	if _, err := BuildPhase4RegionCostProgram(candidate, semantic.Analysis{CandidateRegions: []semantic.CandidateRegion{{}}}); err == nil {
		t.Fatal("negative focus was accepted")
	}
}

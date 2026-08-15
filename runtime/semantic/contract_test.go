package semantic_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

func TestAnalysisAndPlanValidateCanonicalBoundedIdentity(t *testing.T) {
	analysis := validAnalysis()
	if err := analysis.Validate(); err != nil {
		t.Fatal(err)
	}
	analysisIdentity, _, err := analysis.Identity()
	if err != nil {
		t.Fatal(err)
	}
	plan := semantic.Plan{
		SchemaVersion: semantic.PlanSchemaVersion,
		Analysis:      analysis,
		Regions: []semantic.Region{{
			ID: digest('9'), Kind: semantic.RegionWholeFunction, FunctionID: digest('8'),
			Span: semantic.SourceSpan{StartLine: 1, EndLine: 2}, ASTSHA256: digest('7'),
			InputsCanonical: true, OutputsCanonical: true,
			Dependencies: []semantic.Dependency{{Kind: semantic.DependencyCanonicalInputs, IdentitySHA256: digest('6')}},
		}},
	}
	if err := plan.Validate(); err != nil {
		t.Fatal(err)
	}
	if !plan.Regions[0].Reusable() {
		t.Fatal("effect-free canonical region was not reusable")
	}
	planIdentity, _, err := plan.Identity()
	if err != nil || planIdentity == analysisIdentity {
		t.Fatalf("plan identity=%q analysis identity=%q err=%v", planIdentity, analysisIdentity, err)
	}
}

func TestSemanticDecodeRejectsUnknownTrailingAndOversizedDocuments(t *testing.T) {
	analysis := validAnalysis()
	raw, err := json.Marshal(analysis)
	if err != nil {
		t.Fatal(err)
	}
	unknown := append([]byte(nil), raw[:len(raw)-1]...)
	unknown = append(unknown, []byte(`,"unknown":true}`)...)
	for name, candidate := range map[string][]byte{
		"unknown":   unknown,
		"trailing":  append(append([]byte(nil), raw...), []byte(` {}`)...),
		"oversized": []byte(`{"padding":"` + strings.Repeat("x", semantic.MaxDocumentBytes) + `"}`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := semantic.DecodeAnalysis(candidate); !errors.Is(err, semantic.ErrInvalidAnalysis) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestAnalysisRejectsMalformedDuplicateAndInconsistentSummaries(t *testing.T) {
	for name, mutate := range map[string]func(*semantic.Analysis){
		"malformed digest":   func(value *semantic.Analysis) { value.SourceSHA256 = "sha256:no" },
		"duplicate function": func(value *semantic.Analysis) { value.Functions = append(value.Functions, value.Functions[0]) },
		"invalid span":       func(value *semantic.Analysis) { value.Functions[0].Span.EndLine = 0 },
		"unsorted calls":     func(value *semantic.Analysis) { value.Functions[0].Calls = []string{digest('3'), digest('2')} },
		"barrier without unknown": func(value *semantic.Analysis) {
			value.Barriers = []semantic.Barrier{{Code: semantic.BarrierDynamicCall, FunctionID: digest('8'), Span: semantic.SourceSpan{StartLine: 1, EndLine: 1}}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			value := validAnalysis()
			mutate(&value)
			if err := value.Validate(); !errors.Is(err, semantic.ErrInvalidAnalysis) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestPlanRejectsDuplicateRegionsAndWeakerEffectClaims(t *testing.T) {
	analysis := validAnalysis()
	analysis.Functions[0].Effects.MayPublish = true
	base := semantic.Region{
		ID: digest('9'), Kind: semantic.RegionWholeFunction, FunctionID: digest('8'),
		Span: semantic.SourceSpan{StartLine: 1, EndLine: 2}, ASTSHA256: digest('7'),
		InputsCanonical: true, OutputsCanonical: true,
	}
	for name, regions := range map[string][]semantic.Region{
		"weaker":    {base},
		"duplicate": {withPublish(base), withPublish(base)},
	} {
		t.Run(name, func(t *testing.T) {
			plan := semantic.Plan{SchemaVersion: semantic.PlanSchemaVersion, Analysis: analysis, Regions: regions}
			if err := plan.Validate(); !errors.Is(err, semantic.ErrInvalidPlan) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestEffectfulUnknownOrNoncanonicalRegionIsNotReusable(t *testing.T) {
	base := semantic.Region{InputsCanonical: true, OutputsCanonical: true}
	variants := []semantic.Region{
		func() semantic.Region { value := base; value.Effects.MayPublish = true; return value }(),
		func() semantic.Region { value := base; value.Effects.MayObserveLive = true; return value }(),
		func() semantic.Region { value := base; value.Effects.MayBeUnknown = true; return value }(),
		func() semantic.Region { value := base; value.InputsCanonical = false; return value }(),
		func() semantic.Region {
			value := base
			value.RejectionReasons = []semantic.BarrierCode{semantic.BarrierDynamicCall}
			return value
		}(),
	}
	for index, region := range variants {
		if region.Reusable() {
			t.Fatalf("variant %d was reusable: %+v", index, region)
		}
	}
}

func TestAnalysisRejectsMalformedSemanticCallSites(t *testing.T) {
	valid := validAnalysis()
	valid.CallSites = []semantic.CallSite{{
		ID: digest('a'), Span: semantic.SourceSpan{StartLine: 1, EndLine: 1, EndColumn: 1},
		Capability: "sources.read", ControlRegionID: digest('b'), NecessarilyReached: true,
		ArgumentsCanonical: true, CanonicalArguments: json.RawMessage(`{"key":"x"}`), DynamicOccurrence: 1,
	}}
	valid.CandidateRegionCount = 1
	valid.CandidateRegions = []semantic.CandidateRegion{{
		ID: digest('c'), Kind: semantic.CandidateRegionStraightLine, Span: semantic.SourceSpan{StartLine: 1, EndLine: 2},
		ControlRegionID: digest('b'), ControlPredecessors: []string{}, DataDependencies: []semantic.RegionDataDependency{},
		LiveIns: []string{}, LiveOuts: []string{}, LiveInsCanonical: true, LiveOutsCanonical: true,
		CapabilityOccurrences: []string{digest('a')}, Barriers: []semantic.BarrierCode{}, RejectionReasons: []semantic.CandidateRejection{},
	}}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	unicode := valid
	unicode.CallSites = append([]semantic.CallSite(nil), valid.CallSites...)
	unicode.CallSites[0].CanonicalArguments = json.RawMessage(`{"key":"<\u2028>"}`)
	if err := unicode.Validate(); err != nil {
		t.Fatalf("shared Python/Go canonical string rejected: %v", err)
	}
	mutations := map[string]func(*semantic.Analysis){
		"unknown capability": func(value *semantic.Analysis) { value.CallSites[0].Capability = "" },
		"non-canonical arguments": func(value *semantic.Analysis) {
			value.CallSites[0].CanonicalArguments = json.RawMessage(`{ "key":"x"}`)
		},
		"non-object arguments": func(value *semantic.Analysis) { value.CallSites[0].CanonicalArguments = json.RawMessage(`[]`) },
		"structured argument":  func(value *semantic.Analysis) { value.CallSites[0].CanonicalArguments = json.RawMessage(`{"key":[]}`) },
		"coverage":             func(value *semantic.Analysis) { value.CallSiteCoverage = "complete" },
		"dynamic occurrence":   func(value *semantic.Analysis) { value.CallSites[0].DynamicOccurrence = 2 },
		"missing region occurrence": func(value *semantic.Analysis) {
			value.CandidateRegions[0].CapabilityOccurrences = []string{}
		},
		"outside module": func(value *semantic.Analysis) {
			value.CallSites[0].Span.StartLine = 3
			value.CallSites[0].Span.EndLine = 3
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			candidate := valid
			candidate.CallSites = append([]semantic.CallSite(nil), valid.CallSites...)
			candidate.CandidateRegions = append([]semantic.CandidateRegion(nil), valid.CandidateRegions...)
			candidate.CandidateRegions[0].CapabilityOccurrences = append([]string(nil), valid.CandidateRegions[0].CapabilityOccurrences...)
			mutate(&candidate)
			if err := candidate.Validate(); !errors.Is(err, semantic.ErrInvalidAnalysis) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestCandidateRegionGraphIsBoundedCanonicalAndFailClosed(t *testing.T) {
	analysis := validAnalysis()
	analysis.CandidateRegionCount = 1
	analysis.CandidateRegions = []semantic.CandidateRegion{{
		ID: digest('a'), Kind: semantic.CandidateRegionStraightLine,
		Span:                semantic.SourceSpan{StartLine: 1, EndLine: 1, EndColumn: 8},
		ControlRegionID:     digest('b'),
		ControlPredecessors: []string{},
		DataDependencies:    []semantic.RegionDataDependency{},
		LiveIns:             []string{"inputs"}, LiveOuts: []string{"result"}, LiveInsCanonical: true, LiveOutsCanonical: true,
		CapabilityOccurrences: []string{}, Barriers: []semantic.BarrierCode{}, RejectionReasons: []semantic.CandidateRejection{},
	}}
	if err := analysis.Validate(); err != nil {
		t.Fatalf("valid candidate graph: %v", err)
	}
	deferredEffects := analysis
	deferredEffects.ModuleEffects.MayObserveLive = true
	if err := deferredEffects.Validate(); err != nil {
		t.Fatalf("module effects may include deferred function-body effects outside top-level candidate evaluation: %v", err)
	}
	for name, mutate := range map[string]func(*semantic.Analysis){
		"missing coverage": func(value *semantic.Analysis) { value.CandidateRegionCoverage = "" },
		"count mismatch":   func(value *semantic.Analysis) { value.CandidateRegionCount = 0 },
		"nil graph":        func(value *semantic.Analysis) { value.CandidateRegions = nil },
		"first control predecessor": func(value *semantic.Analysis) {
			value.CandidateRegions[0].ControlPredecessors = []string{digest('f')}
		},
		"unknown data producer": func(value *semantic.Analysis) {
			value.CandidateRegions[0].DataDependencies = []semantic.RegionDataDependency{{Name: "inputs", ProducerRegionID: digest('f')}}
		},
		"unsorted liveness":        func(value *semantic.Analysis) { value.CandidateRegions[0].LiveIns = []string{"z", "a"} },
		"unexplained noncanonical": func(value *semantic.Analysis) { value.CandidateRegions[0].LiveInsCanonical = false },
		"unknown occurrence": func(value *semantic.Analysis) {
			value.CandidateRegions[0].CapabilityOccurrences = []string{digest('c')}
		},
		"opaque without barrier": func(value *semantic.Analysis) {
			value.CandidateRegions[0].Kind = semantic.CandidateRegionOpaqueControl
			value.CandidateRegions[0].RejectionReasons = []semantic.CandidateRejection{semantic.CandidateRejectOpaqueControl}
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := analysis
			candidate.CandidateRegions = append([]semantic.CandidateRegion(nil), analysis.CandidateRegions...)
			candidate.CandidateRegions[0].LiveIns = append([]string(nil), analysis.CandidateRegions[0].LiveIns...)
			candidate.CandidateRegions[0].ControlPredecessors = append([]string(nil), analysis.CandidateRegions[0].ControlPredecessors...)
			candidate.CandidateRegions[0].DataDependencies = append([]semantic.RegionDataDependency(nil), analysis.CandidateRegions[0].DataDependencies...)
			candidate.CandidateRegions[0].CapabilityOccurrences = append([]string(nil), analysis.CandidateRegions[0].CapabilityOccurrences...)
			candidate.CandidateRegions[0].RejectionReasons = append([]semantic.CandidateRejection(nil), analysis.CandidateRegions[0].RejectionReasons...)
			mutate(&candidate)
			if err := candidate.Validate(); !errors.Is(err, semantic.ErrInvalidAnalysis) {
				t.Fatalf("error=%v candidate=%+v", err, candidate.CandidateRegions)
			}
		})
	}
}

func validAnalysis() semantic.Analysis {
	return semantic.Analysis{
		SchemaVersion: semantic.AnalysisSchemaVersion,
		SourceSHA256:  digest('1'), ASTSHA256: digest('2'), AnalyzerSHA256: digest('3'),
		ArtifactSHA256: digest('4'), ExecutionProfileSHA256: digest('5'),
		ImportClosureSHA256: digest('6'), CapabilityPlanSHA256: digest('7'),
		ModuleSpan: semantic.SourceSpan{StartLine: 1, EndLine: 2},
		Functions: []semantic.FunctionSummary{{
			ID: digest('8'), Name: "compute", SCCID: digest('8'),
			Span: semantic.SourceSpan{StartLine: 1, EndLine: 2},
		}},
		CallSiteCoverage: "positive_only", CandidateRegionCoverage: "module_top_level_complete", CallSites: []semantic.CallSite{}, CandidateRegions: []semantic.CandidateRegion{},
	}
}

func withPublish(region semantic.Region) semantic.Region {
	region.Effects.MayPublish = true
	return region
}

func digest(character byte) string {
	return "sha256:" + strings.Repeat(string(character), 64)
}

package workflowbench

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

func censusDigest(label string) string {
	digest := sha256.Sum256([]byte(label))
	return fmt.Sprintf("sha256:%x", digest[:])
}

func censusAnalysis(regionCount int, readIndex int) semantic.Analysis {
	regions := make([]semantic.CandidateRegion, regionCount)
	for index := range regions {
		regions[index] = semantic.CandidateRegion{
			ID: censusDigest(fmt.Sprintf("region-%d", index)), Kind: semantic.CandidateRegionStraightLine,
			Span:            semantic.SourceSpan{StartLine: uint32(index + 1), EndLine: uint32(index + 1), EndColumn: 10},
			ControlRegionID: censusDigest("control"), ControlPredecessors: []string{}, DataDependencies: []semantic.RegionDataDependency{},
			LiveIns: []string{}, LiveOuts: []string{}, CapabilityOccurrences: []string{}, Barriers: []semantic.BarrierCode{}, RejectionReasons: []semantic.CandidateRejection{},
		}
		if index > 0 {
			regions[index].ControlPredecessors = []string{regions[index-1].ID}
		}
	}
	site := semantic.CallSite{
		ID: censusDigest("call"), Span: regions[readIndex].Span, Capability: "tools.lookup", ControlRegionID: censusDigest("control"),
		NecessarilyReached: readIndex == 0, ArgumentsCanonical: true, CanonicalArguments: json.RawMessage(`{"key":"alpha"}`), DynamicOccurrence: 1,
	}
	regions[readIndex].CapabilityOccurrences = []string{site.ID}
	return semantic.Analysis{
		SchemaVersion: semantic.AnalysisSchemaVersion, SourceSHA256: censusDigest("source"), ASTSHA256: censusDigest("ast"),
		AnalyzerSHA256: censusDigest("analyzer"), ArtifactSHA256: censusDigest("artifact"), ExecutionProfileSHA256: censusDigest("profile"),
		ImportClosureSHA256: censusDigest("imports"), CapabilityPlanSHA256: censusDigest("plan"), ModuleSpan: semantic.SourceSpan{StartLine: 1, EndLine: uint32(regionCount), EndColumn: 10},
		CallSiteCoverage: "positive_only", CallSites: []semantic.CallSite{site}, CandidateRegionCoverage: "module_top_level_complete",
		CandidateRegionCount: regionCount, CandidateRegions: regions, Functions: []semantic.FunctionSummary{}, Barriers: []semantic.Barrier{},
	}
}

func TestClassifySourcePrefixOpportunityUsesGuestRegions(t *testing.T) {
	projection := map[string]string{"tools.lookup": capability.EffectExternalRead}
	eligible, err := ClassifySourcePrefixOpportunity(SourcePrefixCensusInput{
		ItemID: censusDigest("event-1"), SourceBytes: 120, Analysis: censusAnalysis(3, 0), EffectClasses: projection,
	})
	if err != nil {
		t.Fatal(err)
	}
	if eligible.StructuralStatus != SourcePrefixStructurallyEligible || eligible.Reason != SourcePrefixReasonReadHasTrailingSuite || eligible.ReadRegionIndex != 0 || eligible.TrailingRegions != 2 || eligible.TimingStatus != SourcePrefixTimingNotRecorded {
		t.Fatalf("eligible=%+v", eligible)
	}
	ineligible, err := ClassifySourcePrefixOpportunity(SourcePrefixCensusInput{
		ItemID: censusDigest("event-2"), SourceBytes: 57, Analysis: censusAnalysis(1, 0), EffectClasses: projection,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ineligible.StructuralStatus != SourcePrefixStructurallyIneligible || ineligible.Reason != SourcePrefixReasonReadFinalOrOnlySuite || ineligible.TrailingRegions != 0 {
		t.Fatalf("ineligible=%+v", ineligible)
	}
}

func TestClassifySourcePrefixOpportunityFailsClosed(t *testing.T) {
	tests := []struct {
		name string
		edit func(*SourcePrefixCensusInput)
	}{
		{"missing effect", func(input *SourcePrefixCensusInput) { input.EffectClasses = map[string]string{} }},
		{"wrong effect", func(input *SourcePrefixCensusInput) {
			input.EffectClasses["tools.lookup"] = capability.EffectWorkspaceWrite
		}},
		{"multiple reads", func(input *SourcePrefixCensusInput) {
			input.Analysis.CallSites = append(input.Analysis.CallSites, input.Analysis.CallSites[0])
		}},
		{"missing occurrence", func(input *SourcePrefixCensusInput) {
			input.Analysis.CandidateRegions[0].CapabilityOccurrences = []string{}
		}},
		{"source bytes zero", func(input *SourcePrefixCensusInput) { input.SourceBytes = 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := SourcePrefixCensusInput{ItemID: censusDigest("event"), SourceBytes: 57, Analysis: censusAnalysis(1, 0), EffectClasses: map[string]string{"tools.lookup": capability.EffectExternalRead}}
			test.edit(&input)
			if _, err := ClassifySourcePrefixOpportunity(input); err == nil {
				t.Fatal("expected fail-closed error")
			}
		})
	}
}

func validSourcePrefixCensus(t *testing.T) SourcePrefixCensusEvidence {
	t.Helper()
	rows := []SourcePrefixCensusCase{}
	for index := 0; index < 36; index++ {
		row, err := ClassifySourcePrefixOpportunity(SourcePrefixCensusInput{
			ItemID: censusDigest(fmt.Sprintf("event-%02d", index)), SourceBytes: 48 + index%10, Analysis: censusAnalysis(1, 0),
			EffectClasses: map[string]string{"tools.lookup": capability.EffectExternalRead},
		})
		if err != nil {
			t.Fatal(err)
		}
		row.SourceSHA256 = censusDigest(fmt.Sprintf("source-%02d", index))
		if index == 35 {
			row.SourceSHA256 = rows[0].SourceSHA256
		}
		rows = append(rows, row)
	}
	evidence, err := BuildSourcePrefixCensusEvidence(SourcePrefixCensusBuild{
		ParentRemediationIdentity: censusDigest("parent"), PreregistrationSHA256: censusDigest("preregistration"), ArtifactSourceCommit: "0123456789abcdef0123456789abcdef01234567",
		ArtifactSHA256: censusDigest("artifact"), HarnessSourceCommit: "89abcdef0123456789abcdef0123456789abcdef",
		Cases: rows,
	})
	if err != nil {
		t.Fatal(err)
	}
	return evidence
}

func TestSourcePrefixCensusEvidenceBindsDenominatorAndClaimBoundary(t *testing.T) {
	evidence := validSourcePrefixCensus(t)
	if evidence.Denominator.Events != 36 || evidence.Denominator.UniqueSources != 35 || evidence.Counts.StructurallyIneligible != 36 || evidence.Counts.StructurallyEligible != 0 || evidence.Counts.TimingNotRecorded != 36 {
		t.Fatalf("aggregate=%+v counts=%+v", evidence.Denominator, evidence.Counts)
	}
	if err := ValidateSourcePrefixCensusEvidence(evidence); err != nil {
		t.Fatal(err)
	}
}

func TestDecodeSourcePrefixCensusEvidenceRejectsUnknownAndDuplicateFields(t *testing.T) {
	evidence := validSourcePrefixCensus(t)
	raw, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeSourcePrefixCensusEvidence(raw); err != nil {
		t.Fatal(err)
	}
	unknown := append([]byte(nil), raw[:len(raw)-1]...)
	unknown = append(unknown, []byte(`,"speedup_milli":1000}`)...)
	if _, err := DecodeSourcePrefixCensusEvidence(unknown); err == nil {
		t.Fatal("expected unknown performance field rejection")
	}
	duplicate := append([]byte(`{"schema_version":"pysolate.source-prefix-opportunity-census.v1",`), raw[1:]...)
	if _, err := DecodeSourcePrefixCensusEvidence(duplicate); err == nil {
		t.Fatal("expected duplicate field rejection")
	}
}

func TestSourcePrefixCensusEvidenceRejectsPerformanceAndAggregateDrift(t *testing.T) {
	base := validSourcePrefixCensus(t)
	tests := []struct {
		name string
		edit func(*SourcePrefixCensusEvidence)
	}{
		{"denominator drift", func(value *SourcePrefixCensusEvidence) { value.Denominator.Events-- }},
		{"count drift", func(value *SourcePrefixCensusEvidence) { value.Counts.StructurallyEligible++ }},
		{"timing claim", func(value *SourcePrefixCensusEvidence) { value.Cases[0].TimingStatus = "measured" }},
		{"speedup claim", func(value *SourcePrefixCensusEvidence) { value.PerformanceComparisonSupported = true }},
		{"identity drift", func(value *SourcePrefixCensusEvidence) { value.Identity = censusDigest("false") }},
		{"duplicate event", func(value *SourcePrefixCensusEvidence) { value.Cases[1].ItemID = value.Cases[0].ItemID }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := base
			value.Cases = append([]SourcePrefixCensusCase{}, base.Cases...)
			test.edit(&value)
			if err := ValidateSourcePrefixCensusEvidence(value); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

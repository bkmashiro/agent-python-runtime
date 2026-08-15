package regioncensus

import (
	"crypto/sha256"
	"fmt"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

func TestBuildCountsExactSameAndCrossProgramMaterializableOverlap(t *testing.T) {
	firstSource := []byte("result = 1\nresult = 1\n")
	secondSource := []byte("result = 1\n")
	report, err := build(testDigest("corpus"), []observation{
		{ProgramID: "agent-a", Source: firstSource, Analysis: testAnalysis(firstSource, []semantic.CandidateRegion{
			testRegion("a1", 1, len([]byte("result = 1"))),
			testRegion("a2", 2, len([]byte("result = 1"))),
		})},
		{ProgramID: "agent-b", Source: secondSource, Analysis: testAnalysis(secondSource, []semantic.CandidateRegion{
			testRegion("b1", 1, len([]byte("result = 1"))),
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.RegionsAnalyzed != 3 || report.MaterializableRegions != 3 || report.Effects.Pure != 3 {
		t.Fatalf("counts=%+v", report)
	}
	if report.MaterializableOverlap.UniqueFingerprints != 1 || report.MaterializableOverlap.RepeatedFingerprints != 1 ||
		report.MaterializableOverlap.SameProgramFingerprints != 1 || report.MaterializableOverlap.SameProgramExcessOccurrences != 1 ||
		report.MaterializableOverlap.CrossProgramFingerprints != 1 || report.MaterializableOverlap.CrossProgramCoveredOccurrences != 3 {
		t.Fatalf("overlap=%+v", report.MaterializableOverlap)
	}
	if report.Decision.Status != "go_for_executable_region_spike" || report.Decision.ConsumerAdmitted {
		t.Fatalf("decision=%+v", report.Decision)
	}
	if _, err := Encode(report); err != nil {
		t.Fatal(err)
	}
}

func TestBuildReturnsNoGoWithoutMaterializableCrossProgramOverlap(t *testing.T) {
	source := []byte("pass\n")
	region := testRegion("only", 1, len([]byte("pass")))
	region.LiveOuts = []string{}
	report, err := build(testDigest("corpus"), []observation{{
		ProgramID: "agent-a", Source: source, Analysis: testAnalysis(source, []semantic.CandidateRegion{region}),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision.Status != "no_go" || len(report.Decision.ReasonCodes) != 2 ||
		report.Decision.ReasonCodes[0] != "no_cross_program_exact_materializable_overlap" ||
		report.Decision.ReasonCodes[1] != "no_statically_materializable_regions" {
		t.Fatalf("decision=%+v", report.Decision)
	}
}

func TestFingerprintBindsCanonicalityAndPreventsFalseCrossProgramOverlap(t *testing.T) {
	source := []byte("result = 1\n")
	first := testRegion("first", 1, len([]byte("result = 1")))
	second := testRegion("second", 1, len([]byte("result = 1")))
	second.LiveOutsCanonical = false
	second.RejectionReasons = []semantic.CandidateRejection{semantic.CandidateRejectLiveOutNotCanonical}
	report, err := build(testDigest("corpus"), []observation{
		{ProgramID: "agent-a", Source: source, Analysis: testAnalysis(source, []semantic.CandidateRegion{first})},
		{ProgramID: "agent-b", Source: source, Analysis: testAnalysis(source, []semantic.CandidateRegion{second})},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.ExactOverlap.UniqueFingerprints != 2 || report.ExactOverlap.CrossProgramFingerprints != 0 ||
		report.MaterializableRegions != 1 || report.Decision.Status != "no_go" {
		t.Fatalf("report=%+v", report)
	}
}

func TestDeferredModuleEffectConservativelyBlocksMaterializability(t *testing.T) {
	source := []byte("result = 1\n")
	analysis := testAnalysis(source, []semantic.CandidateRegion{testRegion("region", 1, len([]byte("result = 1")))})
	analysis.ModuleEffects.MayObserveLive = true
	report, err := build(testDigest("corpus"), []observation{{ProgramID: "agent-a", Source: source, Analysis: analysis}})
	if err != nil {
		t.Fatal(err)
	}
	if report.MaterializableRegions != 0 || report.Programs[0].Regions[0].Materializable {
		t.Fatalf("module-level effect did not fail closed: %+v", report)
	}
}

func TestBuildRejectsTargetIdentityDrift(t *testing.T) {
	source := []byte("result = 1\n")
	first := testAnalysis(source, []semantic.CandidateRegion{testRegion("first", 1, len([]byte("result = 1")))})
	second := testAnalysis(source, []semantic.CandidateRegion{testRegion("second", 1, len([]byte("result = 1")))})
	second.ArtifactSHA256 = testDigest("different-artifact")
	if _, err := build(testDigest("corpus"), []observation{
		{ProgramID: "agent-a", Source: source, Analysis: first},
		{ProgramID: "agent-b", Source: source, Analysis: second},
	}); err == nil {
		t.Fatal("mixed artifact identities were accepted")
	}
}

func TestBuildRejectsUnsortedProgramsAndSealedReportMutation(t *testing.T) {
	source := []byte("result = 1\n")
	analysis := testAnalysis(source, []semantic.CandidateRegion{testRegion("one", 1, len([]byte("result = 1")))})
	if _, err := build(testDigest("corpus"), []observation{
		{ProgramID: "agent-b", Source: source, Analysis: analysis},
		{ProgramID: "agent-a", Source: source, Analysis: analysis},
	}); err == nil {
		t.Fatal("unsorted observations accepted")
	}
	report, err := build(testDigest("corpus"), []observation{{ProgramID: "agent-a", Source: source, Analysis: analysis}})
	if err != nil {
		t.Fatal(err)
	}
	report.MaterializableRegions++
	if report.Validate() == nil {
		t.Fatal("mutated sealed report accepted")
	}
}

func TestBuildVerifiedRejectsZeroVerifiedHandle(t *testing.T) {
	if _, err := BuildVerified(testDigest("corpus"), []VerifiedObservation{{
		ProgramID: "agent-a", Source: []byte("pass\n"), Verified: semantic.VerifiedAnalysis{},
	}}); err == nil {
		t.Fatal("zero verified handle accepted")
	}
}

func TestSourceSliceUsesUTF8ByteColumns(t *testing.T) {
	source := []byte("result = '✓'\n")
	span := semantic.SourceSpan{StartLine: 1, EndLine: 1, EndColumn: uint32(len([]byte("result = '✓'")))}
	fragment, err := sourceSlice(source, span)
	if err != nil || string(fragment) != "result = '✓'" {
		t.Fatalf("fragment=%q err=%v", fragment, err)
	}
}

func testAnalysis(source []byte, regions []semantic.CandidateRegion) semantic.Analysis {
	for index := range regions {
		if index == 0 {
			regions[index].ControlPredecessors = []string{}
		} else {
			regions[index].ControlPredecessors = []string{regions[index-1].ID}
		}
	}
	moduleEnd := uint32(0)
	lines := uint32(1)
	for _, value := range source {
		if value == '\n' {
			lines++
			moduleEnd = 0
		} else {
			moduleEnd++
		}
	}
	return semantic.Analysis{
		SchemaVersion: semantic.AnalysisSchemaVersion,
		SourceSHA256:  sourceDigest(source), ASTSHA256: testDigest("ast"), AnalyzerSHA256: testDigest("analyzer"),
		ArtifactSHA256: testDigest("artifact"), ExecutionProfileSHA256: testDigest("profile"),
		ImportClosureSHA256: testDigest("imports"), CapabilityPlanSHA256: testDigest("plan"),
		ModuleSpan: semantic.SourceSpan{StartLine: 1, EndLine: lines, EndColumn: moduleEnd},
		Functions:  []semantic.FunctionSummary{}, Barriers: []semantic.Barrier{},
		CallSiteCoverage: "positive_only", CallSites: []semantic.CallSite{},
		CandidateRegionCoverage: "module_top_level_complete", CandidateRegionCount: len(regions), CandidateRegions: regions,
	}
}

func testRegion(seed string, line, end int) semantic.CandidateRegion {
	return semantic.CandidateRegion{
		ID: testDigest(seed), Kind: semantic.CandidateRegionStraightLine,
		Span:            semantic.SourceSpan{StartLine: uint32(line), EndLine: uint32(line), EndColumn: uint32(end)},
		ControlRegionID: testDigest("control"), ControlPredecessors: []string{},
		DataDependencies: []semantic.RegionDataDependency{}, LiveIns: []string{}, LiveOuts: []string{"result"},
		LiveInsCanonical: true, LiveOutsCanonical: true,
		CapabilityOccurrences: []string{}, Barriers: []semantic.BarrierCode{}, RejectionReasons: []semantic.CandidateRejection{},
	}
}

func testDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return fmt.Sprintf("sha256:%x", digest[:])
}

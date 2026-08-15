package effectgraph_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/effectgraph"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

func TestRunCensusSeparatesStructuralCandidatesFromCurrentProof(t *testing.T) {
	root := t.TempDir()
	source := []byte("result = inputs['x'] * 2\n")
	if err := os.WriteFile(filepath.Join(root, "pure.py"), source, 0o600); err != nil {
		t.Fatal(err)
	}
	corpus := testCorpus(effectgraph.Program{
		ID: "pure", Provenance: effectgraph.ProvenancePublicSynthetic, SourcePath: "pure.py", SourceSHA256: sha(source),
		OracleClass: effectgraph.OraclePureResult,
		StructuralCandidates: []effectgraph.Candidate{
			{Kind: effectgraph.CandidateExactRegionReuse, Occurrences: 1},
			{Kind: effectgraph.CandidatePreDispatch, Occurrences: 2},
			{Kind: effectgraph.CandidateWASMPlacement, Occurrences: 1},
		},
		InputsCanonical: true, OutputsCanonical: true,
	})
	analyzer := func(_ context.Context, _ []byte) (semantic.Analysis, error) {
		analysis := validAnalysis(semantic.EffectSummary{}, nil)
		analysis.SourceSHA256 = sha(source)
		analysis.CallSites = []semantic.CallSite{{
			ID: sha([]byte("call")), Span: semantic.SourceSpan{StartLine: 1, EndLine: 1, EndColumn: 10},
			Capability: "sources.read", ControlRegionID: sha([]byte("control")), NecessarilyReached: true,
			ArgumentsCanonical: true, CanonicalArguments: []byte(`{}`), DynamicOccurrence: 1,
		}}
		analysis.CandidateRegions[0].CapabilityOccurrences = []string{sha([]byte("call"))}
		return analysis, nil
	}
	report, err := effectgraph.RunCensus(context.Background(), corpus, root, analyzer, func([]byte) (string, error) {
		return effectgraph.PlacementWASM, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.ProgramsAnalyzed != 1 || report.ProgramsOpaque != 0 || report.WholeRunReusable != 1 ||
		report.OverlayCallSites != 1 || report.NecessarilyReachedCallSites != 1 ||
		report.Programs[0].OverlayCallSites != 1 || report.Programs[0].NecessarilyReachedCallSites != 1 {
		t.Fatalf("report=%+v", report)
	}
	counts := map[string]effectgraph.OpportunityCount{}
	for _, count := range report.Opportunities {
		counts[count.Kind] = count
	}
	if counts[effectgraph.CandidatePreDispatch].Structural != 2 || counts[effectgraph.CandidatePreDispatch].ProvedLegal != 0 {
		t.Fatalf("pre-dispatch count=%+v", counts[effectgraph.CandidatePreDispatch])
	}
	if counts[effectgraph.CandidatePreDispatch].Legality != effectgraph.NotEvaluated || counts[effectgraph.CandidatePreDispatch].Equivalence != effectgraph.NotEvaluated {
		t.Fatalf("pre-dispatch disposition=%+v", counts[effectgraph.CandidatePreDispatch])
	}
	if counts[effectgraph.CandidateExactRegionReuse].ProvedLegal != 0 || report.WholeRunReusable != 1 {
		t.Fatalf("reuse count=%+v whole_run=%d", counts[effectgraph.CandidateExactRegionReuse], report.WholeRunReusable)
	}
	if report.PlacementCounts != (effectgraph.PlacementCounts{WASM: 1}) {
		t.Fatalf("placement=%+v", report.PlacementCounts)
	}
	forged := report
	forged.Programs[0].Placement = effectgraph.PlacementNative
	forged.PlacementCounts = effectgraph.PlacementCounts{Native: 1}
	if _, err := effectgraph.EncodeReport(forged); err == nil {
		t.Fatal("mutated sealed census report encoded")
	}
}

func TestRunCensusKeepsUnclassifiableProgramsInDenominator(t *testing.T) {
	root := t.TempDir()
	source := []byte("result = 1\n")
	if err := os.WriteFile(filepath.Join(root, "program.py"), source, 0o600); err != nil {
		t.Fatal(err)
	}
	program := effectgraph.Program{
		ID: "program", Provenance: effectgraph.ProvenancePublicSynthetic, SourcePath: "program.py",
		SourceSHA256: sha(source), OracleClass: "pure_result", StructuralCandidates: []effectgraph.Candidate{},
		InputsCanonical: true, OutputsCanonical: true,
	}
	report, err := effectgraph.RunCensus(context.Background(), testCorpus(program), root,
		func(context.Context, []byte) (semantic.Analysis, error) {
			return semantic.Analysis{}, semantic.ErrInvalidAnalysis
		},
		func([]byte) (string, error) { return effectgraph.PlacementWASM, nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.ProgramsAnalyzed != 1 || report.ProgramsUnclassifiable != 1 || report.ProgramsOpaque != 1 ||
		len(report.Programs) != 1 || report.Programs[0].AnalysisStatus != effectgraph.AnalysisUnclassifiable ||
		report.Programs[0].FailureClass != effectgraph.FailureAnalysisInvalid {
		t.Fatalf("unexpected unclassifiable report: %+v", report)
	}
}

func TestRunCensusReportsOpaqueBarriersAndRejectsSourceDigestDrift(t *testing.T) {
	root := t.TempDir()
	source := []byte("result = eval('1+1')\n")
	if err := os.WriteFile(filepath.Join(root, "opaque.py"), source, 0o600); err != nil {
		t.Fatal(err)
	}
	program := effectgraph.Program{
		ID: "opaque", Provenance: effectgraph.ProvenancePublicSynthetic, SourcePath: "opaque.py", SourceSHA256: sha(source),
		OracleClass: effectgraph.OracleOpaque, StructuralCandidates: []effectgraph.Candidate{{Kind: effectgraph.CandidateNativePlacement, Occurrences: 1}},
		InputsCanonical: true, OutputsCanonical: true,
	}
	analysis := validAnalysis(semantic.EffectSummary{MayBeUnknown: true}, []semantic.Barrier{{Code: semantic.BarrierEvalExec, Span: semantic.SourceSpan{StartLine: 1, EndLine: 1, EndColumn: 20}}})
	analysis.SourceSHA256 = sha(source)
	report, err := effectgraph.RunCensus(context.Background(), testCorpus(program), root, func(context.Context, []byte) (semantic.Analysis, error) {
		return analysis, nil
	}, func([]byte) (string, error) { return effectgraph.PlacementNative, nil })
	if err != nil {
		t.Fatal(err)
	}
	if report.ProgramsOpaque != 1 || report.WholeRunReusable != 0 || len(report.BarrierCounts) != 1 || report.BarrierCounts[0].Code != semantic.BarrierEvalExec {
		t.Fatalf("report=%+v", report)
	}
	program.SourceSHA256 = "sha256:" + fmt.Sprintf("%064x", 1)
	if _, err := effectgraph.RunCensus(context.Background(), testCorpus(program), root, nil, nil); err == nil {
		t.Fatal("source digest drift accepted")
	}
}

func testCorpus(programs ...effectgraph.Program) effectgraph.Corpus {
	return effectgraph.Corpus{
		SchemaVersion: effectgraph.CorpusSchemaVersion,
		Target: effectgraph.Target{
			ArtifactSourceCommit: "950249a92eaec648b88850300c5653ab62aff888",
			ArtifactSHA256:       sha([]byte("artifact")), ExecutionProfileSHA256: sha([]byte("profile")),
			ImportClosureSHA256: sha([]byte("imports")), CapabilityPlanSHA256: sha([]byte("plan")), ContractSetSHA256: sha([]byte("contracts")),
		},
		Programs: programs, HistoricalSeeds: []effectgraph.HistoricalSeed{},
	}
}

func validAnalysis(effects semantic.EffectSummary, barriers []semantic.Barrier) semantic.Analysis {
	rejections := []semantic.CandidateRejection{}
	if effects.MayBeUnknown {
		rejections = append(rejections, semantic.CandidateRejectUnknownEffect)
	}
	return semantic.Analysis{
		SchemaVersion: semantic.AnalysisSchemaVersion,
		SourceSHA256:  sha([]byte("source")), ASTSHA256: sha([]byte("ast")), AnalyzerSHA256: sha([]byte("analyzer")),
		ArtifactSHA256: sha([]byte("artifact")), ExecutionProfileSHA256: sha([]byte("profile")),
		ImportClosureSHA256: sha([]byte("imports")), CapabilityPlanSHA256: sha([]byte("plan")),
		ModuleSpan: semantic.SourceSpan{StartLine: 1, EndLine: 1, EndColumn: 30}, ModuleEffects: effects,
		Functions: []semantic.FunctionSummary{}, Barriers: barriers,
		CallSiteCoverage: "positive_only", CandidateRegionCoverage: "module_top_level_complete", CallSites: []semantic.CallSite{}, CandidateRegionCount: 1,
		CandidateRegions: []semantic.CandidateRegion{{
			ID: sha([]byte("region")), Kind: semantic.CandidateRegionStraightLine, Span: semantic.SourceSpan{StartLine: 1, EndLine: 1, EndColumn: 30},
			ControlRegionID: sha([]byte("control")), ControlPredecessors: []string{}, DataDependencies: []semantic.RegionDataDependency{},
			LiveIns: []string{}, LiveOuts: []string{}, LiveInsCanonical: true, LiveOutsCanonical: true,
			Effects: effects, CapabilityOccurrences: []string{}, Barriers: []semantic.BarrierCode{}, RejectionReasons: rejections,
		}},
	}
}

func sha(value []byte) string {
	digest := sha256.Sum256(value)
	return fmt.Sprintf("sha256:%x", digest[:])
}

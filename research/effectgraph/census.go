package effectgraph

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

const ReportSchemaVersion = "pysolate.effectgraph-opportunity-census.v0"

var ErrCensus = errors.New("effect-aware opportunity census failed")

const (
	PlacementWASM    = "pysolate_wasm"
	PlacementNative  = "native_sandbox"
	PlacementUnknown = "unknown"
)

const NotEvaluated = "not_evaluated"

const (
	AnalysisAccepted       = "accepted"
	AnalysisUnclassifiable = "unclassifiable"
	FailureAnalysisInvalid = "analysis_invalid"
	FailurePlanInvalid     = "plan_invalid"
)

type AnalyzeFunc func(context.Context, []byte) (semantic.Analysis, error)
type PlacementFunc func([]byte) (string, error)

type OpportunityCount struct {
	Kind        string `json:"kind"`
	Structural  uint32 `json:"structural_candidates"`
	ProvedLegal uint32 `json:"proved_legal"`
	Legality    string `json:"legality"`
	Equivalence string `json:"equivalence"`
}

type PlacementCounts struct {
	WASM    uint32 `json:"pysolate_wasm"`
	Native  uint32 `json:"native_sandbox"`
	Unknown uint32 `json:"unknown"`
}

type ReportBarrierCount struct {
	Code  semantic.BarrierCode `json:"code"`
	Count uint32               `json:"count"`
}

type ProgramResult struct {
	ID                                   string                 `json:"id"`
	SourceSHA256                         string                 `json:"source_sha256"`
	Opaque                               bool                   `json:"opaque"`
	AnalysisStatus                       string                 `json:"analysis_status"`
	FailureClass                         string                 `json:"failure_class,omitempty"`
	BarrierCodes                         []semantic.BarrierCode `json:"barrier_codes"`
	FunctionCount                        uint32                 `json:"function_count"`
	DistinctFunctionCapabilityReferences uint32                 `json:"distinct_function_capability_references"`
	WholeRunReusable                     bool                   `json:"whole_run_reusable"`
	Placement                            string                 `json:"placement"`
}

type Report struct {
	SchemaVersion                        string               `json:"schema_version"`
	CorpusSHA256                         string               `json:"corpus_sha256"`
	AnalyzerSHA256                       string               `json:"analyzer_sha256"`
	Target                               Target               `json:"target"`
	ProgramsAnalyzed                     uint32               `json:"programs_analyzed"`
	ProgramsOpaque                       uint32               `json:"programs_opaque"`
	ProgramsUnclassifiable               uint32               `json:"programs_unclassifiable"`
	ProgramsWithoutBarriers              uint32               `json:"programs_without_barriers"`
	WholeRunReusable                     uint32               `json:"whole_run_reusable"`
	DistinctFunctionCapabilityReferences uint32               `json:"distinct_function_capability_references"`
	HistoricalSeeds                      uint32               `json:"body_free_historical_seeds"`
	PlacementCounts                      PlacementCounts      `json:"placement_counts"`
	BarrierCounts                        []ReportBarrierCount `json:"barrier_counts"`
	Opportunities                        []OpportunityCount   `json:"opportunities"`
	Programs                             []ProgramResult      `json:"programs"`
}

func (corpus Corpus) Identity() (string, error) {
	if err := corpus.Validate(); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(corpus)
	if err != nil {
		return "", ErrInvalidCorpus
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func RunCensus(ctx context.Context, corpus Corpus, root string, analyze AnalyzeFunc, place PlacementFunc) (Report, error) {
	if err := corpus.Validate(); err != nil || analyze == nil || place == nil || root == "" {
		return Report{}, ErrCensus
	}
	corpusIdentity, err := corpus.Identity()
	if err != nil {
		return Report{}, ErrCensus
	}
	report := Report{
		SchemaVersion: ReportSchemaVersion, CorpusSHA256: corpusIdentity, Target: corpus.Target,
		HistoricalSeeds: uint32(len(corpus.HistoricalSeeds)),
		BarrierCounts:   []ReportBarrierCount{}, Opportunities: []OpportunityCount{}, Programs: []ProgramResult{},
	}
	structural := map[string]uint32{}
	barriers := map[semantic.BarrierCode]uint32{}
	for _, program := range corpus.Programs {
		if err := ctx.Err(); err != nil {
			return Report{}, err
		}
		source, err := loadSource(root, program)
		if err != nil {
			return Report{}, fmt.Errorf("%w: source %s", err, program.ID)
		}
		placement, err := place(source)
		if err != nil || !validPlacement(placement) {
			return Report{}, fmt.Errorf("%w: placement %s", ErrCensus, program.ID)
		}
		for _, candidate := range program.StructuralCandidates {
			structural[candidate.Kind] += candidate.Occurrences
		}
		report.ProgramsAnalyzed++
		incrementPlacement(&report.PlacementCounts, placement)

		analysis, analysisErr := analyze(ctx, source)
		if analysisErr != nil || analysis.Validate() != nil {
			report.ProgramsUnclassifiable++
			report.ProgramsOpaque++
			report.Programs = append(report.Programs, ProgramResult{
				ID: program.ID, SourceSHA256: program.SourceSHA256, Opaque: true,
				AnalysisStatus: AnalysisUnclassifiable, FailureClass: FailureAnalysisInvalid,
				BarrierCodes: []semantic.BarrierCode{}, Placement: placement,
			})
			continue
		}
		if analysis.SourceSHA256 != program.SourceSHA256 ||
			analysis.ArtifactSHA256 != corpus.Target.ArtifactSHA256 ||
			analysis.ExecutionProfileSHA256 != corpus.Target.ExecutionProfileSHA256 ||
			analysis.ImportClosureSHA256 != corpus.Target.ImportClosureSHA256 ||
			analysis.CapabilityPlanSHA256 != corpus.Target.CapabilityPlanSHA256 {
			return Report{}, fmt.Errorf("%w: bind analysis %s", ErrCensus, program.ID)
		}
		if report.AnalyzerSHA256 == "" {
			report.AnalyzerSHA256 = analysis.AnalyzerSHA256
		} else if report.AnalyzerSHA256 != analysis.AnalyzerSHA256 {
			return Report{}, fmt.Errorf("%w: analyzer identity %s", ErrCensus, program.ID)
		}
		codes := make([]semantic.BarrierCode, 0, len(analysis.Barriers))
		seenCodes := map[semantic.BarrierCode]struct{}{}
		for _, barrier := range analysis.Barriers {
			barriers[barrier.Code]++
			if _, exists := seenCodes[barrier.Code]; !exists {
				seenCodes[barrier.Code] = struct{}{}
				codes = append(codes, barrier.Code)
			}
		}
		sort.Slice(codes, func(i, j int) bool { return codes[i] < codes[j] })
		directReferences := uint32(0)
		for _, function := range analysis.Functions {
			directReferences += uint32(len(function.DirectCapabilities))
		}
		plan, _, planErr := semantic.BuildWholeRunPlan(analysis, semantic.WholeRunConfig{
			InputsCanonical: program.InputsCanonical, OutputsCanonical: program.OutputsCanonical,
		})
		if planErr != nil || len(plan.Regions) != 1 {
			report.ProgramsUnclassifiable++
			report.ProgramsOpaque++
			report.Programs = append(report.Programs, ProgramResult{
				ID: program.ID, SourceSHA256: program.SourceSHA256, Opaque: true,
				AnalysisStatus: AnalysisUnclassifiable, FailureClass: FailurePlanInvalid,
				BarrierCodes: codes, FunctionCount: uint32(len(analysis.Functions)),
				DistinctFunctionCapabilityReferences: directReferences, Placement: placement,
			})
			continue
		}
		reusable := plan.Regions[0].Reusable()
		opaque := analysis.ModuleEffects.MayBeUnknown || len(analysis.Barriers) != 0
		row := ProgramResult{
			ID: program.ID, SourceSHA256: program.SourceSHA256, Opaque: opaque,
			AnalysisStatus: AnalysisAccepted, BarrierCodes: codes,
			FunctionCount: uint32(len(analysis.Functions)), DistinctFunctionCapabilityReferences: directReferences,
			WholeRunReusable: reusable, Placement: placement,
		}
		report.Programs = append(report.Programs, row)
		if opaque {
			report.ProgramsOpaque++
		}
		if len(analysis.Barriers) == 0 {
			report.ProgramsWithoutBarriers++
		}
		if reusable {
			report.WholeRunReusable++
		}
		report.DistinctFunctionCapabilityReferences += directReferences

	}
	for code, count := range barriers {
		report.BarrierCounts = append(report.BarrierCounts, ReportBarrierCount{Code: code, Count: count})
	}
	sort.Slice(report.BarrierCounts, func(i, j int) bool { return report.BarrierCounts[i].Code < report.BarrierCounts[j].Code })
	for _, kind := range []string{
		CandidateExactRegionReuse, CandidateNativePlacement, CandidateOverlapWindow, CandidatePreDispatch, CandidateWASMPlacement,
	} {
		report.Opportunities = append(report.Opportunities, OpportunityCount{
			Kind: kind, Structural: structural[kind], ProvedLegal: 0,
			Legality: NotEvaluated, Equivalence: NotEvaluated,
		})
	}
	return report, nil
}

func EncodeReport(report Report) ([]byte, error) {
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, ErrCensus
	}
	return append(encoded, '\n'), nil
}

func loadSource(root string, program Program) ([]byte, error) {
	source, err := readCorpusSource(root, program.SourcePath)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(source)
	if fmt.Sprintf("sha256:%x", digest[:]) != program.SourceSHA256 {
		return nil, ErrCensus
	}
	return source, nil
}

func incrementPlacement(counts *PlacementCounts, placement string) {
	switch placement {
	case PlacementWASM:
		counts.WASM++
	case PlacementNative:
		counts.Native++
	case PlacementUnknown:
		counts.Unknown++
	}
}

func validPlacement(value string) bool {
	return value == PlacementWASM || value == PlacementNative || value == PlacementUnknown
}

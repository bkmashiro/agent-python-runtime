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

const ReportSchemaVersion = "pysolate.effectgraph-opportunity-census.v3"

var ErrCensus = errors.New("effect-aware opportunity census failed")

const (
	PlacementWASM    = "pysolate_wasm"
	PlacementNative  = "native_sandbox"
	PlacementUnknown = "unknown"
)

const (
	NotEvaluated = "not_evaluated"
	Evaluated    = "evaluated"
)

const (
	AnalysisAccepted       = "accepted"
	AnalysisUnclassifiable = "unclassifiable"
	FailureAnalysisInvalid = "analysis_invalid"
	FailurePlanInvalid     = "plan_invalid"
)

type AnalyzeFunc func(context.Context, []byte) (semantic.Analysis, error)
type PlacementFunc func([]byte) (string, error)
type LegalityFunc func(semantic.Analysis) (ProgramLegality, error)

type LegalityRejectionCount struct {
	Reason semantic.RejectionReason `json:"reason"`
	Count  uint32                   `json:"count"`
}

type ProgramLegality struct {
	CallLevelQualified uint32
	PreissueLegal      uint32
	PreissueRejected   uint32
	Rejections         []LegalityRejectionCount
}

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
	ID                                   string                   `json:"id"`
	SourceSHA256                         string                   `json:"source_sha256"`
	Opaque                               bool                     `json:"opaque"`
	AnalysisStatus                       string                   `json:"analysis_status"`
	FailureClass                         string                   `json:"failure_class,omitempty"`
	BarrierCodes                         []semantic.BarrierCode   `json:"barrier_codes"`
	FunctionCount                        uint32                   `json:"function_count"`
	DistinctFunctionCapabilityReferences uint32                   `json:"distinct_function_capability_references"`
	OverlayCallSites                     uint32                   `json:"overlay_call_sites"`
	NecessarilyReachedCallSites          uint32                   `json:"necessarily_reached_call_sites"`
	CallLevelQualified                   uint32                   `json:"call_level_qualified"`
	PreissueLegal                        uint32                   `json:"preissue_legal"`
	PreissueRejected                     uint32                   `json:"preissue_rejected"`
	LegalityRejections                   []LegalityRejectionCount `json:"legality_rejections"`
	WholeRunReusable                     bool                     `json:"whole_run_reusable"`
	Placement                            string                   `json:"placement"`
}

type Report struct {
	SchemaVersion                        string                   `json:"schema_version"`
	CorpusSHA256                         string                   `json:"corpus_sha256"`
	AnalyzerSHA256                       string                   `json:"analyzer_sha256"`
	Target                               Target                   `json:"target"`
	ProgramsAnalyzed                     uint32                   `json:"programs_analyzed"`
	ProgramsOpaque                       uint32                   `json:"programs_opaque"`
	ProgramsUnclassifiable               uint32                   `json:"programs_unclassifiable"`
	ProgramsWithoutBarriers              uint32                   `json:"programs_without_barriers"`
	WholeRunReusable                     uint32                   `json:"whole_run_reusable"`
	DistinctFunctionCapabilityReferences uint32                   `json:"distinct_function_capability_references"`
	OverlayCallSites                     uint32                   `json:"overlay_call_sites"`
	NecessarilyReachedCallSites          uint32                   `json:"necessarily_reached_call_sites"`
	CallLevelQualified                   uint32                   `json:"call_level_qualified"`
	PreissueLegal                        uint32                   `json:"preissue_legal"`
	PreissueRejected                     uint32                   `json:"preissue_rejected"`
	HistoricalSeeds                      uint32                   `json:"body_free_historical_seeds"`
	PlacementCounts                      PlacementCounts          `json:"placement_counts"`
	BarrierCounts                        []ReportBarrierCount     `json:"barrier_counts"`
	LegalityRejections                   []LegalityRejectionCount `json:"legality_rejections"`
	Opportunities                        []OpportunityCount       `json:"opportunities"`
	Programs                             []ProgramResult          `json:"programs"`
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

func RunCensus(ctx context.Context, corpus Corpus, root string, analyze AnalyzeFunc, place PlacementFunc, legality ...LegalityFunc) (Report, error) {
	if err := corpus.Validate(); err != nil || analyze == nil || place == nil || root == "" || len(legality) > 1 ||
		(len(legality) == 1 && legality[0] == nil) {
		return Report{}, ErrCensus
	}
	corpusIdentity, err := corpus.Identity()
	if err != nil {
		return Report{}, ErrCensus
	}
	report := Report{
		SchemaVersion: ReportSchemaVersion, CorpusSHA256: corpusIdentity, Target: corpus.Target,
		HistoricalSeeds: uint32(len(corpus.HistoricalSeeds)),
		BarrierCounts:   []ReportBarrierCount{}, LegalityRejections: []LegalityRejectionCount{},
		Opportunities: []OpportunityCount{}, Programs: []ProgramResult{},
	}
	structural := map[string]uint32{}
	barriers := map[semantic.BarrierCode]uint32{}
	legalityRejections := map[semantic.RejectionReason]uint32{}
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
		reachedCallSites := uint32(0)
		for _, site := range analysis.CallSites {
			if site.NecessarilyReached {
				reachedCallSites++
			}
		}
		report.OverlayCallSites += uint32(len(analysis.CallSites))
		report.NecessarilyReachedCallSites += reachedCallSites
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
				DistinctFunctionCapabilityReferences: directReferences,
				OverlayCallSites:                     uint32(len(analysis.CallSites)), NecessarilyReachedCallSites: reachedCallSites,
				Placement: placement,
			})
			continue
		}
		reusable := plan.Regions[0].Reusable()
		opaque := analysis.ModuleEffects.MayBeUnknown || len(analysis.Barriers) != 0
		row := ProgramResult{
			ID: program.ID, SourceSHA256: program.SourceSHA256, Opaque: opaque,
			AnalysisStatus: AnalysisAccepted, BarrierCodes: codes,
			FunctionCount: uint32(len(analysis.Functions)), DistinctFunctionCapabilityReferences: directReferences,
			OverlayCallSites: uint32(len(analysis.CallSites)), NecessarilyReachedCallSites: reachedCallSites,
			WholeRunReusable: reusable, Placement: placement, LegalityRejections: []LegalityRejectionCount{},
		}
		if len(legality) == 1 {
			programLegality, legalityErr := legality[0](analysis)
			if legalityErr != nil || !validProgramLegality(programLegality, uint32(len(analysis.CallSites))) {
				return Report{}, fmt.Errorf("%w: legality %s", ErrCensus, program.ID)
			}
			row.CallLevelQualified = programLegality.CallLevelQualified
			row.PreissueLegal = programLegality.PreissueLegal
			row.PreissueRejected = programLegality.PreissueRejected
			row.LegalityRejections = append([]LegalityRejectionCount{}, programLegality.Rejections...)
			report.CallLevelQualified += row.CallLevelQualified
			report.PreissueLegal += row.PreissueLegal
			report.PreissueRejected += row.PreissueRejected
			for _, rejection := range programLegality.Rejections {
				legalityRejections[rejection.Reason] += rejection.Count
			}
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
	for reason, count := range legalityRejections {
		report.LegalityRejections = append(report.LegalityRejections, LegalityRejectionCount{Reason: reason, Count: count})
	}
	sort.Slice(report.LegalityRejections, func(i, j int) bool {
		return report.LegalityRejections[i].Reason < report.LegalityRejections[j].Reason
	})
	for _, kind := range []string{
		CandidateExactRegionReuse, CandidateNativePlacement, CandidateOverlapWindow, CandidatePreDispatch, CandidateWASMPlacement,
	} {
		count := OpportunityCount{Kind: kind, Structural: structural[kind], Legality: NotEvaluated, Equivalence: NotEvaluated}
		if kind == CandidatePreDispatch && len(legality) == 1 {
			count.ProvedLegal = report.PreissueLegal
			count.Legality = Evaluated
		}
		report.Opportunities = append(report.Opportunities, count)
	}
	return report, nil
}

func validProgramLegality(value ProgramLegality, callSites uint32) bool {
	rejectionReasons := uint32(0)
	for _, rejection := range value.Rejections {
		rejectionReasons += rejection.Count
	}
	if value.PreissueLegal+value.PreissueRejected != callSites ||
		value.PreissueLegal > value.CallLevelQualified || value.CallLevelQualified > callSites ||
		rejectionReasons < value.PreissueRejected ||
		!sort.SliceIsSorted(value.Rejections, func(i, j int) bool { return value.Rejections[i].Reason < value.Rejections[j].Reason }) {
		return false
	}
	for index, rejection := range value.Rejections {
		if rejection.Reason == "" || rejection.Count == 0 ||
			(index > 0 && value.Rejections[index-1].Reason == rejection.Reason) {
			return false
		}
	}
	return true
}

func EncodeReport(report Report) ([]byte, error) {
	if err := report.Validate(); err != nil {
		return nil, err
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func (report Report) Validate() error {
	if report.SchemaVersion != ReportSchemaVersion || !digestPattern.MatchString(report.CorpusSHA256) ||
		report.ProgramsAnalyzed == 0 || len(report.Programs) != int(report.ProgramsAnalyzed) ||
		report.ProgramsUnclassifiable > report.ProgramsAnalyzed || report.ProgramsOpaque > report.ProgramsAnalyzed ||
		report.NecessarilyReachedCallSites > report.OverlayCallSites || report.CallLevelQualified > report.OverlayCallSites ||
		report.PreissueLegal > report.CallLevelQualified || report.PreissueLegal+report.PreissueRejected > report.OverlayCallSites {
		return ErrInvalidCorpus
	}
	legalityEvaluated := false
	lastOpportunity := ""
	for _, opportunity := range report.Opportunities {
		if opportunity.Kind <= lastOpportunity || opportunity.Structural > report.ProgramsAnalyzed ||
			opportunity.ProvedLegal > opportunity.Structural ||
			(opportunity.Legality != NotEvaluated && opportunity.Legality != Evaluated) ||
			opportunity.Equivalence != NotEvaluated {
			return ErrInvalidCorpus
		}
		if opportunity.Kind == CandidatePreDispatch {
			if opportunity.ProvedLegal != report.PreissueLegal {
				return ErrInvalidCorpus
			}
			legalityEvaluated = opportunity.Legality == Evaluated
		}
		lastOpportunity = opportunity.Kind
	}
	if len(report.Opportunities) != 5 {
		return ErrInvalidCorpus
	}
	var programs, unclassifiable, opaque, calls, reached, baseline, legal, rejected uint32
	placements := PlacementCounts{}
	rejectionTotals := map[semantic.RejectionReason]uint32{}
	lastProgram := ""
	for _, row := range report.Programs {
		if !identifierPattern.MatchString(row.ID) || row.ID <= lastProgram || !digestPattern.MatchString(row.SourceSHA256) ||
			row.NecessarilyReachedCallSites > row.OverlayCallSites || row.CallLevelQualified > row.OverlayCallSites ||
			row.PreissueLegal > row.CallLevelQualified || row.PreissueLegal+row.PreissueRejected > row.OverlayCallSites {
			return ErrInvalidCorpus
		}
		programs++
		if row.AnalysisStatus == AnalysisUnclassifiable {
			unclassifiable++
		} else if row.AnalysisStatus != AnalysisAccepted {
			return ErrInvalidCorpus
		}
		if row.Opaque {
			opaque++
		}
		calls += row.OverlayCallSites
		reached += row.NecessarilyReachedCallSites
		baseline += row.CallLevelQualified
		legal += row.PreissueLegal
		rejected += row.PreissueRejected
		switch row.Placement {
		case PlacementWASM:
			placements.WASM++
		case PlacementNative:
			placements.Native++
		case PlacementUnknown:
			placements.Unknown++
		default:
			return ErrInvalidCorpus
		}
		lastReason := semantic.RejectionReason("")
		for _, item := range row.LegalityRejections {
			if item.Reason == "" || item.Reason <= lastReason || item.Count == 0 {
				return ErrInvalidCorpus
			}
			rejectionTotals[item.Reason] += item.Count
			lastReason = item.Reason
		}
		lastProgram = row.ID
	}
	if programs != report.ProgramsAnalyzed || unclassifiable != report.ProgramsUnclassifiable || opaque != report.ProgramsOpaque ||
		calls != report.OverlayCallSites || reached != report.NecessarilyReachedCallSites ||
		baseline != report.CallLevelQualified || legal != report.PreissueLegal || rejected != report.PreissueRejected ||
		placements != report.PlacementCounts {
		return ErrInvalidCorpus
	}
	if legalityEvaluated {
		if legal+rejected != calls {
			return ErrInvalidCorpus
		}
	} else if baseline != 0 || legal != 0 || rejected != 0 || len(report.LegalityRejections) != 0 {
		return ErrInvalidCorpus
	}
	lastReason := semantic.RejectionReason("")
	for _, item := range report.LegalityRejections {
		if item.Reason == "" || item.Reason <= lastReason || item.Count == 0 || rejectionTotals[item.Reason] != item.Count {
			return ErrInvalidCorpus
		}
		delete(rejectionTotals, item.Reason)
		lastReason = item.Reason
	}
	if len(rejectionTotals) != 0 {
		return ErrInvalidCorpus
	}
	return nil
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

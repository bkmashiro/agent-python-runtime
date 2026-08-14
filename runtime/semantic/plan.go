package semantic

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
)

const CensusSchemaVersion = "pysolate.semantic-census.v0"

type WholeRunConfig struct {
	Dependencies     []Dependency
	InputsCanonical  bool
	OutputsCanonical bool
}

type BarrierCount struct {
	Code  BarrierCode `json:"code"`
	Count uint32      `json:"count"`
}

type Census struct {
	SchemaVersion   string         `json:"schema_version"`
	AnalysisSHA256  string         `json:"analysis_sha256"`
	PlanSHA256      string         `json:"plan_sha256"`
	Functions       uint32         `json:"functions"`
	Regions         uint32         `json:"regions"`
	ReusableRegions uint32         `json:"reusable_regions"`
	RejectedRegions uint32         `json:"rejected_regions"`
	BarrierCounts   []BarrierCount `json:"barrier_counts"`
}

func BuildWholeRunPlan(analysis Analysis, config WholeRunConfig) (Plan, Census, error) {
	if err := analysis.Validate(); err != nil {
		return Plan{}, Census{}, err
	}
	analysisIdentity, _, err := analysis.Identity()
	if err != nil {
		return Plan{}, Census{}, err
	}
	dependencies := append([]Dependency{}, config.Dependencies...)
	sort.Slice(dependencies, func(i, j int) bool {
		if dependencies[i].Kind != dependencies[j].Kind {
			return dependencies[i].Kind < dependencies[j].Kind
		}
		return dependencies[i].IdentitySHA256 < dependencies[j].IdentitySHA256
	})
	effects := analysis.ModuleEffects
	for _, function := range analysis.Functions {
		effects = mergeEffects(effects, function.Effects)
	}
	reasons := make([]BarrierCode, 0, len(analysis.Barriers))
	seen := map[BarrierCode]struct{}{}
	for _, barrier := range analysis.Barriers {
		if _, exists := seen[barrier.Code]; exists {
			continue
		}
		seen[barrier.Code] = struct{}{}
		reasons = append(reasons, barrier.Code)
	}
	sort.Slice(reasons, func(i, j int) bool { return reasons[i] < reasons[j] })
	regionDigest := sha256.Sum256([]byte("pysolate.semantic-region.v0\x00" + analysisIdentity + "\x00whole_run"))
	region := Region{
		ID: "sha256:" + hex.EncodeToString(regionDigest[:]), Kind: RegionWholeRun,
		Span: analysis.ModuleSpan, ASTSHA256: analysis.ASTSHA256, Effects: effects,
		Dependencies: dependencies, InputsCanonical: config.InputsCanonical,
		OutputsCanonical: config.OutputsCanonical, RejectionReasons: reasons,
	}
	plan := Plan{SchemaVersion: PlanSchemaVersion, Analysis: analysis, Regions: []Region{region}}
	planIdentity, _, err := plan.Identity()
	if err != nil {
		return Plan{}, Census{}, err
	}
	counts := map[BarrierCode]uint32{}
	for _, barrier := range analysis.Barriers {
		counts[barrier.Code]++
	}
	codes := make([]BarrierCode, 0, len(counts))
	for code := range counts {
		codes = append(codes, code)
	}
	sort.Slice(codes, func(i, j int) bool { return codes[i] < codes[j] })
	barrierCounts := make([]BarrierCount, 0, len(codes))
	for _, code := range codes {
		barrierCounts = append(barrierCounts, BarrierCount{Code: code, Count: counts[code]})
	}
	census := Census{
		SchemaVersion: CensusSchemaVersion, AnalysisSHA256: analysisIdentity, PlanSHA256: planIdentity,
		Functions: uint32(len(analysis.Functions)), Regions: 1, BarrierCounts: barrierCounts,
	}
	if region.Reusable() {
		census.ReusableRegions = 1
	} else {
		census.RejectedRegions = 1
	}
	return plan, census, nil
}

func mergeEffects(left, right EffectSummary) EffectSummary {
	return EffectSummary{
		MayPublish:     left.MayPublish || right.MayPublish,
		MayObserveLive: left.MayObserveLive || right.MayObserveLive,
		MaySuspend:     left.MaySuspend || right.MaySuspend,
		MayBeUnknown:   left.MayBeUnknown || right.MayBeUnknown,
	}
}

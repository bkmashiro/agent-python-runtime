package workflowbench

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

const (
	SourcePrefixCensusSchema               = "pysolate.source-prefix-opportunity-census.v1"
	SourcePrefixCensusClassification       = "NATURAL_STRUCTURAL_OPPORTUNITY_CENSUS_NOT_PERFORMANCE_EVIDENCE"
	SourcePrefixStructurallyEligible       = "structurally_eligible"
	SourcePrefixStructurallyIneligible     = "structurally_ineligible"
	SourcePrefixReasonReadHasTrailingSuite = "read_has_trailing_suite"
	SourcePrefixReasonReadFinalOrOnlySuite = "read_final_or_only_suite"
	SourcePrefixTimingNotRecorded          = "not_recorded"
)

var sourcePrefixCensusCommit = regexp.MustCompile(`^[0-9a-f]{40}$`)

type SourcePrefixCensusInput struct {
	ItemID        string
	SourceBytes   int
	Analysis      semantic.Analysis
	EffectClasses map[string]string
}

type SourcePrefixCensusCase struct {
	ItemID                 string `json:"item_id"`
	SourceSHA256           string `json:"source_sha256"`
	SourceBytes            int    `json:"source_bytes"`
	ASTSHA256              string `json:"ast_sha256"`
	AnalyzerSHA256         string `json:"analyzer_sha256"`
	ArtifactSHA256         string `json:"artifact_sha256"`
	ExecutionProfileSHA256 string `json:"execution_profile_sha256"`
	CapabilityPlanSHA256   string `json:"capability_plan_sha256"`
	CandidateRegions       int    `json:"candidate_regions"`
	ReadRegionIndex        int    `json:"read_region_index"`
	TrailingRegions        int    `json:"trailing_regions"`
	StructuralStatus       string `json:"structural_status"`
	Reason                 string `json:"reason"`
	TimingStatus           string `json:"timing_status"`
}

type SourcePrefixCensusDenominator struct {
	Events        int `json:"events"`
	UniqueSources int `json:"unique_sources"`
}

type SourcePrefixCensusCounts struct {
	StructurallyEligible   int `json:"structurally_eligible"`
	StructurallyIneligible int `json:"structurally_ineligible"`
	TimingNotRecorded      int `json:"timing_not_recorded"`
}

type SourcePrefixCensusClaimBoundary struct {
	Supports       string   `json:"supports"`
	DoesNotSupport []string `json:"does_not_support"`
}

type SourcePrefixCensusEvidence struct {
	SchemaVersion                  string                          `json:"schema_version"`
	Classification                 string                          `json:"classification"`
	Identity                       string                          `json:"identity"`
	ParentRemediationIdentity      string                          `json:"parent_remediation_identity"`
	PreregistrationSHA256          string                          `json:"preregistration_sha256"`
	ArtifactSourceCommit           string                          `json:"artifact_source_commit"`
	ArtifactSHA256                 string                          `json:"artifact_sha256"`
	HarnessSourceCommit            string                          `json:"harness_source_commit"`
	PerformanceComparisonSupported bool                            `json:"performance_comparison_supported"`
	Denominator                    SourcePrefixCensusDenominator   `json:"denominator"`
	Counts                         SourcePrefixCensusCounts        `json:"counts"`
	ClaimBoundary                  SourcePrefixCensusClaimBoundary `json:"claim_boundary"`
	Cases                          []SourcePrefixCensusCase        `json:"cases"`
}

type SourcePrefixCensusBuild struct {
	ParentRemediationIdentity string
	PreregistrationSHA256     string
	ArtifactSourceCommit      string
	ArtifactSHA256            string
	HarnessSourceCommit       string
	Cases                     []SourcePrefixCensusCase
}

func censusValidDigest(value string) bool {
	return regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(value)
}

func sourcePrefixSpanContains(parent, child semantic.SourceSpan) bool {
	startsBefore := parent.StartLine < child.StartLine || parent.StartLine == child.StartLine && parent.StartColumn <= child.StartColumn
	endsAfter := parent.EndLine > child.EndLine || parent.EndLine == child.EndLine && parent.EndColumn >= child.EndColumn
	return startsBefore && endsAfter
}

func ClassifySourcePrefixOpportunity(input SourcePrefixCensusInput) (SourcePrefixCensusCase, error) {
	analysis := input.Analysis
	if !censusValidDigest(input.ItemID) || input.SourceBytes <= 0 || analysis.CandidateRegionCount <= 0 || analysis.CandidateRegionCount != len(analysis.CandidateRegions) || analysis.CandidateRegionCoverage != "module_top_level_complete" || analysis.CallSiteCoverage != "positive_only" {
		return SourcePrefixCensusCase{}, errors.New("invalid source-prefix census input")
	}
	for _, digest := range []string{analysis.SourceSHA256, analysis.ASTSHA256, analysis.AnalyzerSHA256, analysis.ArtifactSHA256, analysis.ExecutionProfileSHA256, analysis.CapabilityPlanSHA256} {
		if !censusValidDigest(digest) {
			return SourcePrefixCensusCase{}, errors.New("invalid source-prefix census analysis identity")
		}
	}
	reads := []semantic.CallSite{}
	for _, site := range analysis.CallSites {
		effect, ok := input.EffectClasses[site.Capability]
		if !ok {
			return SourcePrefixCensusCase{}, errors.New("semantic call site lacks a bound effect class")
		}
		if effect == capability.EffectExternalRead {
			reads = append(reads, site)
		}
	}
	if len(reads) != 1 {
		return SourcePrefixCensusCase{}, fmt.Errorf("expected exactly one verified external READ, got %d", len(reads))
	}
	read := reads[0]
	regionIndex := -1
	for index, region := range analysis.CandidateRegions {
		contained := sourcePrefixSpanContains(region.Span, read.Span)
		listed := false
		for _, occurrence := range region.CapabilityOccurrences {
			if occurrence == read.ID {
				listed = true
				break
			}
		}
		if contained != listed {
			return SourcePrefixCensusCase{}, errors.New("READ span and candidate occurrence disagree")
		}
		if listed {
			if regionIndex >= 0 {
				return SourcePrefixCensusCase{}, errors.New("READ belongs to multiple candidate regions")
			}
			regionIndex = index
		}
	}
	if regionIndex < 0 {
		return SourcePrefixCensusCase{}, errors.New("READ is absent from exact Guest candidate regions")
	}
	trailing := len(analysis.CandidateRegions) - regionIndex - 1
	status, reason := SourcePrefixStructurallyEligible, SourcePrefixReasonReadHasTrailingSuite
	if trailing == 0 {
		status, reason = SourcePrefixStructurallyIneligible, SourcePrefixReasonReadFinalOrOnlySuite
	}
	return SourcePrefixCensusCase{
		ItemID: input.ItemID, SourceSHA256: analysis.SourceSHA256, SourceBytes: input.SourceBytes,
		ASTSHA256: analysis.ASTSHA256, AnalyzerSHA256: analysis.AnalyzerSHA256, ArtifactSHA256: analysis.ArtifactSHA256,
		ExecutionProfileSHA256: analysis.ExecutionProfileSHA256, CapabilityPlanSHA256: analysis.CapabilityPlanSHA256,
		CandidateRegions: len(analysis.CandidateRegions), ReadRegionIndex: regionIndex, TrailingRegions: trailing,
		StructuralStatus: status, Reason: reason, TimingStatus: SourcePrefixTimingNotRecorded,
	}, nil
}

func sourcePrefixCensusIdentity(value SourcePrefixCensusEvidence) (string, error) {
	value.Identity = ""
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", digest[:]), nil
}

func BuildSourcePrefixCensusEvidence(build SourcePrefixCensusBuild) (SourcePrefixCensusEvidence, error) {
	cases := append([]SourcePrefixCensusCase{}, build.Cases...)
	sort.Slice(cases, func(i, j int) bool { return cases[i].ItemID < cases[j].ItemID })
	sources := map[string]struct{}{}
	counts := SourcePrefixCensusCounts{}
	for _, row := range cases {
		sources[row.SourceSHA256] = struct{}{}
		if row.StructuralStatus == SourcePrefixStructurallyEligible {
			counts.StructurallyEligible++
		} else if row.StructuralStatus == SourcePrefixStructurallyIneligible {
			counts.StructurallyIneligible++
		}
		if row.TimingStatus == SourcePrefixTimingNotRecorded {
			counts.TimingNotRecorded++
		}
	}
	evidence := SourcePrefixCensusEvidence{
		SchemaVersion: SourcePrefixCensusSchema, Classification: SourcePrefixCensusClassification,
		ParentRemediationIdentity: build.ParentRemediationIdentity, PreregistrationSHA256: build.PreregistrationSHA256,
		ArtifactSourceCommit: build.ArtifactSourceCommit,
		ArtifactSHA256:       build.ArtifactSHA256, HarnessSourceCommit: build.HarnessSourceCommit,
		PerformanceComparisonSupported: false,
		Denominator:                    SourcePrefixCensusDenominator{Events: len(cases), UniqueSources: len(sources)}, Counts: counts,
		ClaimBoundary: SourcePrefixCensusClaimBoundary{
			Supports:       "structural source-prefix overlap opportunity frequency in the frozen remediation-v2 READ events",
			DoesNotSupport: []string{"latency or speedup", "provider generation timing", "natural benchmark performance uplift", "dynamic DAG scheduling", "production external effects"},
		},
		Cases: cases,
	}
	identity, err := sourcePrefixCensusIdentity(evidence)
	if err != nil {
		return evidence, err
	}
	evidence.Identity = identity
	return evidence, ValidateSourcePrefixCensusEvidence(evidence)
}

func ValidateSourcePrefixCensusEvidence(evidence SourcePrefixCensusEvidence) error {
	if evidence.SchemaVersion != SourcePrefixCensusSchema || evidence.Classification != SourcePrefixCensusClassification || evidence.PerformanceComparisonSupported ||
		!censusValidDigest(evidence.Identity) || !censusValidDigest(evidence.ParentRemediationIdentity) || !censusValidDigest(evidence.PreregistrationSHA256) || !censusValidDigest(evidence.ArtifactSHA256) ||
		!sourcePrefixCensusCommit.MatchString(evidence.ArtifactSourceCommit) || !sourcePrefixCensusCommit.MatchString(evidence.HarnessSourceCommit) || len(evidence.Cases) != 36 {
		return errors.New("invalid source-prefix census envelope")
	}
	identity, err := sourcePrefixCensusIdentity(evidence)
	if err != nil || identity != evidence.Identity {
		return errors.New("source-prefix census identity mismatch")
	}
	if evidence.ClaimBoundary.Supports != "structural source-prefix overlap opportunity frequency in the frozen remediation-v2 READ events" ||
		len(evidence.ClaimBoundary.DoesNotSupport) != 5 {
		return errors.New("invalid source-prefix census claim boundary")
	}
	sources := map[string]struct{}{}
	seen := map[string]struct{}{}
	counts := SourcePrefixCensusCounts{}
	last := ""
	for _, row := range evidence.Cases {
		if !censusValidDigest(row.ItemID) || !censusValidDigest(row.SourceSHA256) || !censusValidDigest(row.ASTSHA256) || !censusValidDigest(row.AnalyzerSHA256) ||
			!censusValidDigest(row.ArtifactSHA256) || row.ArtifactSHA256 != evidence.ArtifactSHA256 || !censusValidDigest(row.ExecutionProfileSHA256) || !censusValidDigest(row.CapabilityPlanSHA256) ||
			row.SourceBytes <= 0 || row.CandidateRegions <= 0 || row.ReadRegionIndex < 0 || row.ReadRegionIndex >= row.CandidateRegions || row.TrailingRegions != row.CandidateRegions-row.ReadRegionIndex-1 ||
			row.TimingStatus != SourcePrefixTimingNotRecorded || row.ItemID <= last {
			return errors.New("invalid source-prefix census case")
		}
		if _, exists := seen[row.ItemID]; exists {
			return errors.New("duplicate source-prefix census event")
		}
		seen[row.ItemID] = struct{}{}
		last = row.ItemID
		sources[row.SourceSHA256] = struct{}{}
		switch {
		case row.StructuralStatus == SourcePrefixStructurallyEligible && row.Reason == SourcePrefixReasonReadHasTrailingSuite && row.TrailingRegions > 0:
			counts.StructurallyEligible++
		case row.StructuralStatus == SourcePrefixStructurallyIneligible && row.Reason == SourcePrefixReasonReadFinalOrOnlySuite && row.TrailingRegions == 0:
			counts.StructurallyIneligible++
		default:
			return errors.New("inconsistent source-prefix census classification")
		}
		counts.TimingNotRecorded++
	}
	if evidence.Denominator != (SourcePrefixCensusDenominator{Events: len(evidence.Cases), UniqueSources: len(sources)}) || evidence.Counts != counts || counts.TimingNotRecorded != len(evidence.Cases) {
		return errors.New("source-prefix census aggregate mismatch")
	}
	return nil
}

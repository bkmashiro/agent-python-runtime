package sourceboundpasses

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
)

const AuthoredWorkloadEvidenceSchemaVersion = "pysolate.source-bound-pass-authored-workload-evidence.v1"

var ErrInvalidAuthoredWorkloadEvidence = errors.New("invalid authored source-bound pass workload evidence")

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

type AuthoredWorkloadEvidenceInput struct {
	ID                     string
	SourceSHA256           string
	ASTSHA256              string
	AnalyzerSHA256         string
	CandidateRegions       uint32
	LocallyReusableRegions uint32
	CallSites              uint32
	SemanticAdmitted       uint32
	SemanticRejected       uint32
}

type AuthoredWorkloadEvidenceBuild struct {
	Preregistration        AuthoredWorkloadPreregistration
	PreregistrationSHA256  string
	ArtifactSourceCommit   string
	ArtifactSHA256         string
	ArtifactManifestSHA256 string
	HarnessSourceCommit    string
	CapabilityPlanSHA256   string
	ExecutionProfileSHA256 string
	Rows                   []AuthoredWorkloadEvidenceInput
}

type AuthoredWorkloadEvidenceRow struct {
	ID                     string `json:"id"`
	Category               string `json:"category"`
	SourceSHA256           string `json:"source_sha256"`
	ASTSHA256              string `json:"ast_sha256"`
	AnalyzerSHA256         string `json:"analyzer_sha256"`
	CandidateRegions       uint32 `json:"candidate_regions"`
	LocallyReusableRegions uint32 `json:"locally_reusable_regions"`
	CallSites              uint32 `json:"call_sites"`
	SemanticAdmitted       uint32 `json:"semantic_pre_dispatch_admitted"`
	SemanticRejected       uint32 `json:"semantic_pre_dispatch_rejected"`
	PreparedStatus         string `json:"prepared_pure_region_status"`
	AllAdmittedStatus      string `json:"all_admitted_status"`
}

type AuthoredWorkloadEvidenceCounts struct {
	Cases                  uint32 `json:"cases"`
	CandidateRegions       uint32 `json:"candidate_regions"`
	LocallyReusableRegions uint32 `json:"locally_reusable_regions"`
	CallSites              uint32 `json:"call_sites"`
	SemanticAdmitted       uint32 `json:"semantic_pre_dispatch_admitted"`
	SemanticRejected       uint32 `json:"semantic_pre_dispatch_rejected"`
}

type NaturalSourcePrefixAnchor struct {
	EvidenceIdentity     string `json:"evidence_identity"`
	Events               uint32 `json:"events"`
	UniqueSources        uint32 `json:"unique_sources"`
	StructurallyEligible uint32 `json:"structurally_eligible"`
	TimingRecorded       bool   `json:"timing_recorded"`
}

type AuthoredWorkloadEvidence struct {
	SchemaVersion                  string                         `json:"schema_version"`
	Classification                 string                         `json:"classification"`
	IdentitySHA256                 string                         `json:"identity_sha256"`
	PreregistrationSHA256          string                         `json:"preregistration_sha256"`
	ArtifactSourceCommit           string                         `json:"artifact_source_commit"`
	ArtifactSHA256                 string                         `json:"artifact_sha256"`
	ArtifactManifestSHA256         string                         `json:"artifact_manifest_sha256"`
	HarnessSourceCommit            string                         `json:"harness_source_commit"`
	CapabilityPlanSHA256           string                         `json:"capability_plan_sha256"`
	ExecutionProfileSHA256         string                         `json:"execution_profile_sha256"`
	PerformanceComparisonSupported bool                           `json:"performance_comparison_supported"`
	Counts                         AuthoredWorkloadEvidenceCounts `json:"counts"`
	NaturalCorpus                  NaturalSourcePrefixAnchor      `json:"natural_corpus"`
	Rows                           []AuthoredWorkloadEvidenceRow  `json:"rows"`
	ClaimBoundary                  []string                       `json:"claim_boundary"`
}

func BuildAuthoredWorkloadEvidence(build AuthoredWorkloadEvidenceBuild) (AuthoredWorkloadEvidence, error) {
	if build.Preregistration.Validate() != nil || !validWorkloadDigest(build.PreregistrationSHA256) ||
		!commitPattern.MatchString(build.ArtifactSourceCommit) || !commitPattern.MatchString(build.HarnessSourceCommit) ||
		!validWorkloadDigest(build.ArtifactSHA256) || !validWorkloadDigest(build.ArtifactManifestSHA256) ||
		!validWorkloadDigest(build.CapabilityPlanSHA256) || !validWorkloadDigest(build.ExecutionProfileSHA256) ||
		len(build.Rows) != len(build.Preregistration.Cases) {
		return AuthoredWorkloadEvidence{}, ErrInvalidAuthoredWorkloadEvidence
	}
	rows := make([]AuthoredWorkloadEvidenceRow, len(build.Rows))
	counts := AuthoredWorkloadEvidenceCounts{Cases: uint32(len(build.Rows))}
	for index, input := range build.Rows {
		registered := build.Preregistration.Cases[index]
		if input.ID != registered.ID || input.SourceSHA256 != registered.SourceSHA256 || !validWorkloadDigest(input.ASTSHA256) ||
			!validWorkloadDigest(input.AnalyzerSHA256) || input.LocallyReusableRegions > input.CandidateRegions ||
			input.SemanticAdmitted+input.SemanticRejected != input.CallSites {
			return AuthoredWorkloadEvidence{}, ErrInvalidAuthoredWorkloadEvidence
		}
		rows[index] = AuthoredWorkloadEvidenceRow{
			ID: input.ID, Category: registered.Category, SourceSHA256: input.SourceSHA256,
			ASTSHA256: input.ASTSHA256, AnalyzerSHA256: input.AnalyzerSHA256,
			CandidateRegions: input.CandidateRegions, LocallyReusableRegions: input.LocallyReusableRegions,
			CallSites: input.CallSites, SemanticAdmitted: input.SemanticAdmitted, SemanticRejected: input.SemanticRejected,
			PreparedStatus:    "candidate_census_only_not_formally_executed",
			AllAdmittedStatus: "not_applicable_without_shared_fixture",
		}
		counts.CandidateRegions += input.CandidateRegions
		counts.LocallyReusableRegions += input.LocallyReusableRegions
		counts.CallSites += input.CallSites
		counts.SemanticAdmitted += input.SemanticAdmitted
		counts.SemanticRejected += input.SemanticRejected
	}
	value := AuthoredWorkloadEvidence{
		SchemaVersion:         AuthoredWorkloadEvidenceSchemaVersion,
		Classification:        "AUTHORED_EXACT_GUEST_STRUCTURAL_CENSUS_NOT_END_TO_END_PERFORMANCE_EVIDENCE",
		PreregistrationSHA256: build.PreregistrationSHA256,
		ArtifactSourceCommit:  build.ArtifactSourceCommit, ArtifactSHA256: build.ArtifactSHA256,
		ArtifactManifestSHA256: build.ArtifactManifestSHA256, HarnessSourceCommit: build.HarnessSourceCommit,
		CapabilityPlanSHA256: build.CapabilityPlanSHA256, ExecutionProfileSHA256: build.ExecutionProfileSHA256,
		PerformanceComparisonSupported: false, Counts: counts, Rows: rows,
		NaturalCorpus: NaturalSourcePrefixAnchor{
			EvidenceIdentity: "sha256:13120c7ec8565fe7599c0c3f362a0ae90deeb67cafdd986dafa4a8cac70d714a",
			Events:           36, UniqueSources: 30, StructurallyEligible: 0, TimingRecorded: false,
		},
		ClaimBoundary: []string{
			"exact Guest structural analysis of six preregistered authored cases",
			"semantic_pre_dispatch admission census", "prepared_pure_region candidate census only",
			"no authored timing or speedup", "no deferred-pass implementation", "no natural prevalence beyond anchored census",
		},
	}
	value.IdentitySHA256 = authoredEvidenceIdentity(value)
	if err := value.Validate(); err != nil {
		return AuthoredWorkloadEvidence{}, err
	}
	return value, nil
}

func (value AuthoredWorkloadEvidence) Validate() error {
	if value.SchemaVersion != AuthoredWorkloadEvidenceSchemaVersion || !validWorkloadDigest(value.IdentitySHA256) ||
		value.IdentitySHA256 != authoredEvidenceIdentity(value) || value.PerformanceComparisonSupported ||
		value.Counts.Cases != uint32(len(value.Rows)) || value.NaturalCorpus.Events != 36 ||
		value.NaturalCorpus.StructurallyEligible != 0 || value.NaturalCorpus.TimingRecorded {
		return ErrInvalidAuthoredWorkloadEvidence
	}
	counts := AuthoredWorkloadEvidenceCounts{Cases: uint32(len(value.Rows))}
	for _, row := range value.Rows {
		if !validWorkloadDigest(row.SourceSHA256) || !validWorkloadDigest(row.ASTSHA256) || !validWorkloadDigest(row.AnalyzerSHA256) ||
			row.LocallyReusableRegions > row.CandidateRegions || row.SemanticAdmitted+row.SemanticRejected != row.CallSites {
			return ErrInvalidAuthoredWorkloadEvidence
		}
		counts.CandidateRegions += row.CandidateRegions
		counts.LocallyReusableRegions += row.LocallyReusableRegions
		counts.CallSites += row.CallSites
		counts.SemanticAdmitted += row.SemanticAdmitted
		counts.SemanticRejected += row.SemanticRejected
	}
	if counts != value.Counts {
		return ErrInvalidAuthoredWorkloadEvidence
	}
	return nil
}

func EncodeAuthoredWorkloadEvidence(value AuthoredWorkloadEvidence) ([]byte, error) {
	if err := value.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func authoredEvidenceIdentity(value AuthoredWorkloadEvidence) string {
	value.IdentitySHA256 = ""
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func validWorkloadDigest(value string) bool {
	return len(value) == 71 && value[:7] == "sha256:" && regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(value[7:])
}

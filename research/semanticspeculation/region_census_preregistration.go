package semanticspeculation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"

	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

const Phase4RegionMatrixSchemaVersion = "pysolate.semantic-speculation-phase4-region-case-matrix.v1"
const Phase4RegionPreregistrationSchemaVersion = "pysolate.semantic-speculation-phase4-region-census-preregistration.v1"
const Phase4RegionStudyID = "semantic-speculation-phase4-region-census-v1"
const Phase4RegionMatrixIdentity = "sha256:fc3c3cdbf62eac9cde8c17625b6c60de1709d8a61b464872820021068813f6ee"
const Phase4RegionPreregistrationIdentity = "sha256:81a3110d66c8f84dc1be9bfea057049cbbe7af9214e52b6c4348ffabdedaf234"
const Phase4RegionFreezeParentCommit = "5f12bfcd57f529fc5bc4af42f8e18ee9ba0f12c1"
const Phase4RegionAnalyzerSourceCommit = "384327f413138434455af77a322f63afbace7384"
const Phase4RegionAnalyzerArtifactSHA256 = "sha256:cdb440e794b5865878e602eeebf4fe8198a20b33a140f7d4e87a679b1fa89191"
const Phase4RegionRemediationSourceCommit = "44574ddaf907181e9354b6e8c47c9a33a2657bf1"
const Phase4RegionRemediationArtifactSHA256 = "sha256:8780338cf3b4330371b13f06a2846006077c3ff99ee89d7fb618ea19e252d242"

const phase4RegionPilotCaseID = "scalar_chain_2_before_effect"

type Phase4RegionCostShape struct {
	Operation     string `json:"operation"`
	OperatorCount uint32 `json:"operator_count"`
}

type Phase4RegionCase struct {
	Class                 string                `json:"class"`
	ConstructedCostShape  Phase4RegionCostShape `json:"constructed_cost_shape"`
	ExpectedLocalReusable bool                  `json:"expected_local_reusable"`
	FocusRegionIndex      uint32                `json:"focus_region_index"`
	ID                    string                `json:"id"`
	RequiredControlTags   []string              `json:"required_control_tags"`
	Source                string                `json:"source"`
	SourceSHA256          string                `json:"source_sha256"`
}

type Phase4RegionCaseMatrix struct {
	AnalysisSchema         string             `json:"analysis_schema"`
	AnalyzerArtifactSHA256 string             `json:"analyzer_artifact_sha256"`
	AnalyzerSourceCommit   string             `json:"analyzer_source_commit"`
	CandidateRegionSchema  string             `json:"candidate_region_schema"`
	CapabilitySymbols      []string           `json:"capability_symbols"`
	Cases                  []Phase4RegionCase `json:"cases"`
	FreezeParentCommit     string             `json:"freeze_parent_commit"`
	PilotCaseIDs           []string           `json:"pilot_case_ids"`
	PilotPolicy            string             `json:"pilot_policy"`
	SchemaVersion          string             `json:"schema_version"`
	ShuffleSeed            uint64             `json:"shuffle_seed"`
	StudyID                string             `json:"study_id"`
}

type Phase4RegionMechanismGate struct {
	FailureAction string   `json:"failure_action"`
	Required      []string `json:"required"`
}

type Phase4RegionOpportunityGate struct {
	ConsumerDecision              string `json:"consumer_decision"`
	RequiredCostOrdering          string `json:"required_cost_ordering"`
	RequiredPositiveNonPilotCases uint32 `json:"required_positive_non_pilot_cases"`
}

type Phase4RegionPreregistration struct {
	AnalyzerArtifactSHA256 string                      `json:"analyzer_artifact_sha256"`
	AnalyzerSourceCommit   string                      `json:"analyzer_source_commit"`
	AuthorityConstraints   []string                    `json:"authority_constraints"`
	CostMetrics            []string                    `json:"cost_metrics"`
	CostPolicy             string                      `json:"cost_policy"`
	EligibilityMetrics     []string                    `json:"eligibility_metrics"`
	FreezeParentCommit     string                      `json:"freeze_parent_commit"`
	FrozenInputs           []string                    `json:"frozen_inputs"`
	MatrixIdentity         string                      `json:"matrix_identity"`
	MeasurementOrder       string                      `json:"measurement_order"`
	MechanismGate          Phase4RegionMechanismGate   `json:"mechanism_gate"`
	OpportunityGate        Phase4RegionOpportunityGate `json:"opportunity_gate"`
	PilotExclusion         []string                    `json:"pilot_exclusion"`
	SchemaVersion          string                      `json:"schema_version"`
	StudyID                string                      `json:"study_id"`
}

func DecodePhase4RegionCaseMatrix(raw []byte) (Phase4RegionCaseMatrix, error) {
	var value Phase4RegionCaseMatrix
	if err := decodeRegionContract(raw, &value); err != nil || regionDigest(raw) != Phase4RegionMatrixIdentity || validatePhase4RegionMatrix(value) != nil {
		return Phase4RegionCaseMatrix{}, errors.New("invalid phase 4 region case matrix")
	}
	return value, nil
}

func DecodePhase4RegionPreregistration(raw []byte) (Phase4RegionPreregistration, error) {
	var value Phase4RegionPreregistration
	if err := decodeRegionContract(raw, &value); err != nil || regionDigest(raw) != Phase4RegionPreregistrationIdentity || validatePhase4RegionPreregistration(value) != nil {
		return Phase4RegionPreregistration{}, errors.New("invalid phase 4 region preregistration")
	}
	return value, nil
}

func validatePhase4RegionMatrix(value Phase4RegionCaseMatrix) error {
	if value.SchemaVersion != Phase4RegionMatrixSchemaVersion || value.StudyID != Phase4RegionStudyID || value.FreezeParentCommit != Phase4RegionFreezeParentCommit || value.AnalyzerSourceCommit != Phase4RegionAnalyzerSourceCommit || value.AnalyzerArtifactSHA256 != Phase4RegionAnalyzerArtifactSHA256 || value.AnalysisSchema != semantic.AnalysisSchemaVersion || value.CandidateRegionSchema != "pysolate.semantic-candidate-region.v0" || value.ShuffleSeed != 20260822 || len(value.PilotCaseIDs) != 1 || value.PilotCaseIDs[0] != phase4RegionPilotCaseID || len(value.Cases) != 12 {
		return errors.New("invalid phase 4 region matrix header")
	}
	seen := map[string]bool{}
	positiveNonPilot := 0
	negativeClasses := map[string]bool{}
	for _, candidate := range value.Cases {
		if seen[candidate.ID] || !identifierPattern.MatchString(candidate.ID) || !identifierPattern.MatchString(candidate.Class) || candidate.Source == "" || candidate.SourceSHA256 != regionDigest([]byte(candidate.Source)) || candidate.ConstructedCostShape.Operation == "" || candidate.ConstructedCostShape.OperatorCount == 0 || len(candidate.RequiredControlTags) == 0 {
			return errors.New("invalid phase 4 region case")
		}
		seen[candidate.ID] = true
		if candidate.ExpectedLocalReusable && candidate.ID != phase4RegionPilotCaseID {
			positiveNonPilot++
		}
		if !candidate.ExpectedLocalReusable {
			negativeClasses[candidate.Class] = true
		}
	}
	if !seen[phase4RegionPilotCaseID] || positiveNonPilot != 3 || len(negativeClasses) < 7 {
		return errors.New("invalid phase 4 region case coverage")
	}
	return nil
}

func validatePhase4RegionPreregistration(value Phase4RegionPreregistration) error {
	if value.SchemaVersion != Phase4RegionPreregistrationSchemaVersion || value.StudyID != Phase4RegionStudyID || value.FreezeParentCommit != Phase4RegionFreezeParentCommit || value.MatrixIdentity != Phase4RegionMatrixIdentity || value.AnalyzerSourceCommit != Phase4RegionAnalyzerSourceCommit || value.AnalyzerArtifactSHA256 != Phase4RegionAnalyzerArtifactSHA256 || len(value.PilotExclusion) != 1 || value.PilotExclusion[0] != phase4RegionPilotCaseID || len(value.EligibilityMetrics) == 0 || len(value.CostMetrics) == 0 || len(value.MechanismGate.Required) == 0 || value.OpportunityGate.RequiredPositiveNonPilotCases != 3 || len(value.AuthorityConstraints) < 5 || len(value.FrozenInputs) < 5 {
		return errors.New("invalid phase 4 region preregistration")
	}
	return nil
}

func decodeRegionContract(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("trailing phase 4 region contract data")
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return err
	}
	canonical, _ := json.Marshal(generic)
	canonical = append(canonical, '\n')
	if !bytes.Equal(raw, canonical) {
		return errors.New("non-canonical phase 4 region contract")
	}
	return nil
}

func regionDigest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(digest[:])
}

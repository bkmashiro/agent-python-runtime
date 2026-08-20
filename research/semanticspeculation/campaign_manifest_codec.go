package semanticspeculation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
)

const (
	CampaignEvidenceManifestSchemaVersion = "pysolate.semantic-speculation-campaign-manifest.v1"
	campaignEvidenceManifestMaxBytes      = 1 << 20
)

var ErrInvalidCampaignEvidenceManifest = errors.New("invalid semantic-speculation campaign evidence manifest")

type CampaignEvidenceManifest struct {
	SchemaVersion            string                         `json:"schema_version"`
	StudyID                  string                         `json:"study_id"`
	PreregistrationSHA256    string                         `json:"preregistration_sha256"`
	CaseMatrixSHA256         string                         `json:"case_matrix_sha256"`
	SourceCommit             string                         `json:"source_commit"`
	ShuffleSeed              uint64                         `json:"shuffle_seed"`
	TrialsPerTreatment       uint32                         `json:"trials_per_treatment"`
	ClaimScope               string                         `json:"claim_scope"`
	ProductionGeneralization bool                           `json:"production_generalization"`
	OracleAnalysisOnly       bool                           `json:"oracle_analysis_only"`
	Bindings                 TrialBindings                  `json:"bindings"`
	Files                    []MatchedCaseEvidenceReference `json:"files"`
	Identity                 string                         `json:"identity"`
}

func SealCampaignEvidenceManifest(sourceCommit string, bindings TrialBindings, refs []MatchedCaseEvidenceReference) (CampaignEvidenceManifest, error) {
	value := CampaignEvidenceManifest{
		SchemaVersion: CampaignEvidenceManifestSchemaVersion, StudyID: "semantic-speculation-v1",
		PreregistrationSHA256: PreregistrationIdentity, CaseMatrixSHA256: SyntheticCaseMatrixIdentity,
		SourceCommit: sourceCommit, ShuffleSeed: phase3ShuffleSeed, TrialsPerTreatment: 5,
		ClaimScope: "synthetic_matched_mechanism_only", ProductionGeneralization: false, OracleAnalysisOnly: true,
		Bindings: bindings, Files: append([]MatchedCaseEvidenceReference(nil), refs...),
	}
	if validateCampaignEvidenceManifest(value, false) != nil {
		return CampaignEvidenceManifest{}, ErrInvalidCampaignEvidenceManifest
	}
	identity, err := campaignEvidenceManifestIdentity(value)
	if err != nil {
		return CampaignEvidenceManifest{}, err
	}
	value.Identity = identity
	if validateCampaignEvidenceManifest(value, true) != nil {
		return CampaignEvidenceManifest{}, ErrInvalidCampaignEvidenceManifest
	}
	return value, nil
}

func EncodeCampaignEvidenceManifest(value CampaignEvidenceManifest) ([]byte, error) {
	if validateCampaignEvidenceManifest(value, true) != nil {
		return nil, ErrInvalidCampaignEvidenceManifest
	}
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) > campaignEvidenceManifestMaxBytes {
		return nil, ErrInvalidCampaignEvidenceManifest
	}
	return encoded, nil
}

func DecodeCampaignEvidenceManifest(raw []byte) (CampaignEvidenceManifest, error) {
	if len(raw) == 0 || len(raw) > campaignEvidenceManifestMaxBytes || rejectDuplicateKeys(raw) != nil {
		return CampaignEvidenceManifest{}, ErrInvalidCampaignEvidenceManifest
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var value CampaignEvidenceManifest
	if err := decoder.Decode(&value); err != nil {
		return CampaignEvidenceManifest{}, ErrInvalidCampaignEvidenceManifest
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF || validateCampaignEvidenceManifest(value, true) != nil {
		return CampaignEvidenceManifest{}, ErrInvalidCampaignEvidenceManifest
	}
	canonical, err := json.Marshal(value)
	if err != nil || !bytes.Equal(canonical, raw) {
		return CampaignEvidenceManifest{}, ErrInvalidCampaignEvidenceManifest
	}
	return value, nil
}

func validateCampaignEvidenceManifest(value CampaignEvidenceManifest, sealed bool) error {
	coordinates := Phase3CampaignCoordinates()
	if value.SchemaVersion != CampaignEvidenceManifestSchemaVersion || value.StudyID != "semantic-speculation-v1" ||
		value.PreregistrationSHA256 != PreregistrationIdentity || value.CaseMatrixSHA256 != SyntheticCaseMatrixIdentity ||
		!commitPattern.MatchString(value.SourceCommit) || value.ShuffleSeed != phase3ShuffleSeed || value.TrialsPerTreatment != 5 ||
		value.ClaimScope != "synthetic_matched_mechanism_only" || value.ProductionGeneralization || !value.OracleAnalysisOnly ||
		!validTrialBindings(value.Bindings) || len(value.Files) != len(coordinates) || sealed != digestPattern.MatchString(value.Identity) {
		return ErrInvalidCampaignEvidenceManifest
	}
	for index, coordinate := range coordinates {
		ref := value.Files[index]
		if ref.CaseID != coordinate.CaseID || ref.TrialIndex != coordinate.TrialIndex ||
			ref.FileName != matchedEvidenceFileName(coordinate.CaseID, coordinate.TrialIndex) ||
			!digestPattern.MatchString(ref.Identity) || !digestPattern.MatchString(ref.SHA256) || ref.SizeBytes == 0 {
			return ErrInvalidCampaignEvidenceManifest
		}
	}
	if sealed {
		expected, err := campaignEvidenceManifestIdentity(value)
		if err != nil || expected != value.Identity {
			return ErrInvalidCampaignEvidenceManifest
		}
	}
	return nil
}

func validTrialBindings(bindings TrialBindings) bool {
	return digestPattern.MatchString(bindings.ArtifactSHA256) && digestPattern.MatchString(bindings.ManifestSHA256) &&
		digestPattern.MatchString(bindings.ImportInventorySHA256) && digestPattern.MatchString(bindings.ExecutionProfileSHA256) &&
		digestPattern.MatchString(bindings.CapabilityPlanSHA256) && digestPattern.MatchString(bindings.PrivacySHA256)
}

func campaignEvidenceManifestIdentity(value CampaignEvidenceManifest) (string, error) {
	value.Identity = ""
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

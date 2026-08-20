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
	MatchedCaseEvidenceSchemaVersion = "pysolate.semantic-speculation-matched-case-evidence.v1"
	matchedCaseEvidenceMaxBytes      = 256 << 10
)

var ErrInvalidMatchedCaseEvidence = errors.New("invalid matched-case evidence")

type MatchedCaseEvidence struct {
	SchemaVersion            string                      `json:"schema_version"`
	StudyID                  string                      `json:"study_id"`
	PreregistrationSHA256    string                      `json:"preregistration_sha256"`
	CaseMatrixSHA256         string                      `json:"case_matrix_sha256"`
	ClaimScope               string                      `json:"claim_scope"`
	ProductionGeneralization bool                        `json:"production_generalization"`
	OracleAnalysisOnly       bool                        `json:"oracle_analysis_only"`
	Records                  []TrialRecord               `json:"records"`
	Oracle                   PerfectEffectOracleEstimate `json:"oracle"`
	Aggregate                MatchedCaseAggregate        `json:"aggregate"`
	Identity                 string                      `json:"identity"`
}

func SealMatchedCaseEvidence(campaign MatchedCampaignResult) (MatchedCaseEvidence, error) {
	if len(campaign.Records) != 3 {
		return MatchedCaseEvidence{}, ErrInvalidMatchedCaseEvidence
	}
	value := MatchedCaseEvidence{
		SchemaVersion: MatchedCaseEvidenceSchemaVersion,
		StudyID:       "semantic-speculation-v1", PreregistrationSHA256: PreregistrationIdentity,
		CaseMatrixSHA256: SyntheticCaseMatrixIdentity, ClaimScope: "synthetic_matched_mechanism_only",
		ProductionGeneralization: false, OracleAnalysisOnly: true,
		Records: append([]TrialRecord(nil), campaign.Records...), Oracle: campaign.Oracle, Aggregate: campaign.Aggregate,
	}
	if validateMatchedCaseEvidence(value, false) != nil {
		return MatchedCaseEvidence{}, ErrInvalidMatchedCaseEvidence
	}
	identity, err := matchedCaseEvidenceIdentity(value)
	if err != nil {
		return MatchedCaseEvidence{}, ErrInvalidMatchedCaseEvidence
	}
	value.Identity = identity
	if validateMatchedCaseEvidence(value, true) != nil {
		return MatchedCaseEvidence{}, ErrInvalidMatchedCaseEvidence
	}
	return value, nil
}

func EncodeMatchedCaseEvidence(value MatchedCaseEvidence) ([]byte, error) {
	if validateMatchedCaseEvidence(value, true) != nil {
		return nil, ErrInvalidMatchedCaseEvidence
	}
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) > matchedCaseEvidenceMaxBytes {
		return nil, ErrInvalidMatchedCaseEvidence
	}
	return encoded, nil
}

func DecodeMatchedCaseEvidence(raw []byte) (MatchedCaseEvidence, error) {
	if len(raw) == 0 || len(raw) > matchedCaseEvidenceMaxBytes || rejectDuplicateKeys(raw) != nil {
		return MatchedCaseEvidence{}, ErrInvalidMatchedCaseEvidence
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var value MatchedCaseEvidence
	if decoder.Decode(&value) != nil || decoder.Decode(&struct{}{}) != io.EOF || validateMatchedCaseEvidence(value, true) != nil {
		return MatchedCaseEvidence{}, ErrInvalidMatchedCaseEvidence
	}
	canonical, err := json.Marshal(value)
	if err != nil || !bytes.Equal(canonical, raw) {
		return MatchedCaseEvidence{}, ErrInvalidMatchedCaseEvidence
	}
	return value, nil
}

func validateMatchedCaseEvidence(value MatchedCaseEvidence, sealed bool) error {
	if value.SchemaVersion != MatchedCaseEvidenceSchemaVersion || value.StudyID != "semantic-speculation-v1" ||
		value.PreregistrationSHA256 != PreregistrationIdentity || value.CaseMatrixSHA256 != SyntheticCaseMatrixIdentity ||
		value.ClaimScope != "synthetic_matched_mechanism_only" || value.ProductionGeneralization || !value.OracleAnalysisOnly ||
		len(value.Records) != 3 || sealed != digestPattern.MatchString(value.Identity) {
		return ErrInvalidMatchedCaseEvidence
	}
	aggregate, err := AggregateMatchedTrials(value.Records, value.Oracle)
	if err != nil || aggregate != value.Aggregate {
		return ErrInvalidMatchedCaseEvidence
	}
	if sealed {
		expected, identityErr := matchedCaseEvidenceIdentity(value)
		if identityErr != nil || expected != value.Identity {
			return ErrInvalidMatchedCaseEvidence
		}
	}
	return nil
}

func matchedCaseEvidenceIdentity(value MatchedCaseEvidence) (string, error) {
	value.Identity = ""
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

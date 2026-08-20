package semanticspeculation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
)

const (
	SyntheticCaseMatrixSchemaVersion = "pysolate.semantic-speculation-synthetic-case-matrix.v1"
	SyntheticCaseMatrixIdentity      = "sha256:f69c31c874d56b7563942bf889a798ed16b38a657fef18be90d4251f49fbee3f"
)

type SyntheticCaseMatrix struct {
	SchemaVersion             string                    `json:"schema_version"`
	StudyID                   string                    `json:"study_id"`
	ComparatorContractSHA256  string                    `json:"comparator_contract_sha256"`
	PhysicalDelayMilliseconds uint32                    `json:"physical_delay_milliseconds"`
	Cases                     []SyntheticCaseProjection `json:"cases"`
	Identity                  string                    `json:"identity"`
}

func NewSyntheticCaseMatrix() (SyntheticCaseMatrix, error) {
	fixtures := Phase3SyntheticCases()
	cases := make([]SyntheticCaseProjection, len(fixtures))
	for index, fixture := range fixtures {
		if err := fixture.Validate(); err != nil {
			return SyntheticCaseMatrix{}, ErrInvalidPreregistration
		}
		cases[index] = fixture.Projection()
	}
	value := SyntheticCaseMatrix{
		SchemaVersion:             SyntheticCaseMatrixSchemaVersion,
		StudyID:                   "semantic-speculation-v1",
		ComparatorContractSHA256:  EagerStyleGateV1Identity,
		PhysicalDelayMilliseconds: 250,
		Cases:                     cases,
	}
	if !validSyntheticCaseMatrix(value, false) {
		return SyntheticCaseMatrix{}, ErrInvalidPreregistration
	}
	return value, nil
}

func SealSyntheticCaseMatrix(value SyntheticCaseMatrix) (SyntheticCaseMatrix, error) {
	value.Identity = ""
	if !validSyntheticCaseMatrix(value, false) {
		return SyntheticCaseMatrix{}, ErrInvalidPreregistration
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return SyntheticCaseMatrix{}, ErrInvalidPreregistration
	}
	digest := sha256.Sum256(append([]byte(SyntheticCaseMatrixSchemaVersion+"\x00"), raw...))
	value.Identity = "sha256:" + hex.EncodeToString(digest[:])
	if value.Identity != SyntheticCaseMatrixIdentity || !validSyntheticCaseMatrix(value, true) {
		return SyntheticCaseMatrix{}, ErrInvalidPreregistration
	}
	return value, nil
}

func EncodeSyntheticCaseMatrix(value SyntheticCaseMatrix) ([]byte, error) {
	if !validSyntheticCaseMatrix(value, true) {
		return nil, ErrInvalidPreregistration
	}
	return json.Marshal(value)
}

func DecodeSyntheticCaseMatrix(raw []byte) (SyntheticCaseMatrix, error) {
	if len(raw) == 0 || len(raw) > maxTrialBytes || rejectDuplicateKeys(raw) != nil {
		return SyntheticCaseMatrix{}, ErrInvalidPreregistration
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var value SyntheticCaseMatrix
	if decoder.Decode(&value) != nil {
		return SyntheticCaseMatrix{}, ErrInvalidPreregistration
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF || !validSyntheticCaseMatrix(value, true) {
		return SyntheticCaseMatrix{}, ErrInvalidPreregistration
	}
	canonical, err := json.Marshal(value)
	if err != nil || !bytes.Equal(canonical, raw) {
		return SyntheticCaseMatrix{}, ErrInvalidPreregistration
	}
	return value, nil
}

func validSyntheticCaseMatrix(value SyntheticCaseMatrix, sealed bool) bool {
	if value.SchemaVersion != SyntheticCaseMatrixSchemaVersion || value.StudyID != "semantic-speculation-v1" ||
		value.ComparatorContractSHA256 != EagerStyleGateV1Identity || value.PhysicalDelayMilliseconds != 250 ||
		len(value.Cases) != 7 || sealed != digestPattern.MatchString(value.Identity) {
		return false
	}
	for index, item := range value.Cases {
		if _, err := EncodeSyntheticCaseProjection(item); err != nil || (index > 0 && value.Cases[index-1].ID >= item.ID) {
			return false
		}
	}
	copyValue := value
	copyValue.Identity = ""
	raw, err := json.Marshal(copyValue)
	if err != nil {
		return false
	}
	digest := sha256.Sum256(append([]byte(SyntheticCaseMatrixSchemaVersion+"\x00"), raw...))
	expected := "sha256:" + hex.EncodeToString(digest[:])
	return expected == SyntheticCaseMatrixIdentity && (!sealed || value.Identity == expected)
}

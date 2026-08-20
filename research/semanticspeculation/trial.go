package semanticspeculation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
)

const TrialSchemaVersion = "pysolate.semantic-speculation-trial.v2"

const maxTrialBytes = 64 << 10

var ErrInvalidTrialRecord = errors.New("invalid semantic-speculation trial record")

type PhysicalDispositions struct {
	Consumed  uint32 `json:"consumed"`
	Orphaned  uint32 `json:"orphaned"`
	Cancelled uint32 `json:"cancelled"`
	Failed    uint32 `json:"failed"`
	Late      uint32 `json:"late"`
	TimedOut  uint32 `json:"timed_out"`
	Fallback  uint32 `json:"fallback"`
}

func (value PhysicalDispositions) total() uint32 {
	return value.Consumed + value.Orphaned + value.Cancelled + value.Failed + value.Late + value.TimedOut + value.Fallback
}

type TrialRecord struct {
	SchemaVersion            string               `json:"schema_version"`
	StudyID                  string               `json:"study_id"`
	PreregistrationSHA256    string               `json:"preregistration_sha256"`
	CaseID                   string               `json:"case_id"`
	Treatment                string               `json:"treatment"`
	ComparatorContractSHA256 string               `json:"comparator_contract_sha256,omitempty"`
	TrialIndex               uint32               `json:"trial_index"`
	SourceSHA256             string               `json:"source_sha256"`
	SourceScheduleSHA256     string               `json:"source_schedule_sha256"`
	InputsSHA256             string               `json:"inputs_sha256"`
	ArtifactSHA256           string               `json:"artifact_sha256"`
	ManifestSHA256           string               `json:"manifest_sha256"`
	ImportInventorySHA256    string               `json:"import_inventory_sha256"`
	ExecutionProfileSHA256   string               `json:"execution_profile_sha256"`
	CapabilityPlanSHA256     string               `json:"capability_plan_sha256"`
	PrivacySHA256            string               `json:"privacy_sha256"`
	FinalProgramOutcome      string               `json:"final_program_outcome"`
	FinalPythonStarted       bool                 `json:"final_python_started"`
	PrefixPythonExecutions   uint32               `json:"prefix_python_executions"`
	ResultSHA256             string               `json:"result_sha256"`
	ErrorClass               string               `json:"error_class"`
	LogicalCalls             uint32               `json:"logical_calls"`
	PhysicalAttempts         uint32               `json:"physical_attempts"`
	PhysicalResultBytes      uint64               `json:"physical_result_bytes"`
	ProviderCostUnits        uint64               `json:"provider_cost_units"`
	ReadyBeforeFinalize      uint32               `json:"ready_before_finalize"`
	PhysicalDispositions     PhysicalDispositions `json:"physical_dispositions"`
	AuthorityDisposition     string               `json:"authority_disposition"`
	WorkspaceDisposition     string               `json:"workspace_disposition"`
	StartedNanos             uint64               `json:"started_nanos"`
	EndedNanos               uint64               `json:"ended_nanos"`
	Identity                 string               `json:"identity"`
}

func SealTrialRecord(value TrialRecord) (TrialRecord, error) {
	if value.Identity != "" || validateTrialRecord(value, false) != nil {
		return TrialRecord{}, ErrInvalidTrialRecord
	}
	identity, err := trialIdentity(value)
	if err != nil {
		return TrialRecord{}, ErrInvalidTrialRecord
	}
	value.Identity = identity
	if validateTrialRecord(value, true) != nil {
		return TrialRecord{}, ErrInvalidTrialRecord
	}
	return value, nil
}

func EncodeTrialRecord(value TrialRecord) ([]byte, error) {
	if validateTrialRecord(value, true) != nil {
		return nil, ErrInvalidTrialRecord
	}
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) > maxTrialBytes {
		return nil, ErrInvalidTrialRecord
	}
	return encoded, nil
}

func DecodeTrialRecord(raw []byte) (TrialRecord, error) {
	if len(raw) == 0 || len(raw) > maxTrialBytes || rejectDuplicateKeys(raw) != nil {
		return TrialRecord{}, ErrInvalidTrialRecord
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var value TrialRecord
	if decoder.Decode(&value) != nil || decoder.Decode(&struct{}{}) != io.EOF || validateTrialRecord(value, true) != nil {
		return TrialRecord{}, ErrInvalidTrialRecord
	}
	canonical, err := json.Marshal(value)
	if err != nil || !bytes.Equal(canonical, raw) {
		return TrialRecord{}, ErrInvalidTrialRecord
	}
	return value, nil
}

func validateTrialRecord(value TrialRecord, sealed bool) error {
	if value.SchemaVersion != TrialSchemaVersion || value.StudyID != "semantic-speculation-v1" ||
		!digestPattern.MatchString(value.PreregistrationSHA256) || !identifierPattern.MatchString(value.CaseID) ||
		!validTreatment(value.Treatment) || value.Treatment == "perfect_effect_oracle" || value.TrialIndex == 0 || value.TrialIndex > 5 ||
		!digestPattern.MatchString(value.SourceSHA256) || !digestPattern.MatchString(value.SourceScheduleSHA256) ||
		!digestPattern.MatchString(value.InputsSHA256) || !digestPattern.MatchString(value.ArtifactSHA256) ||
		!digestPattern.MatchString(value.ManifestSHA256) || !digestPattern.MatchString(value.ImportInventorySHA256) ||
		!digestPattern.MatchString(value.ExecutionProfileSHA256) || !digestPattern.MatchString(value.CapabilityPlanSHA256) ||
		!digestPattern.MatchString(value.PrivacySHA256) ||
		value.StartedNanos == 0 || value.EndedNanos < value.StartedNanos || value.PhysicalDispositions.total() != value.PhysicalAttempts ||
		value.ReadyBeforeFinalize > value.PhysicalAttempts || !validAuthorityDisposition(value.AuthorityDisposition) ||
		!validWorkspaceDisposition(value.WorkspaceDisposition) || sealed != digestPattern.MatchString(value.Identity) {
		return ErrInvalidTrialRecord
	}
	if value.Treatment != "eager_style_gate" && value.PrefixPythonExecutions != 0 {
		return ErrInvalidTrialRecord
	}
	if value.Treatment == "eager_style_gate" {
		if value.ComparatorContractSHA256 != EagerStyleGateV1Identity {
			return ErrInvalidTrialRecord
		}
	} else if value.ComparatorContractSHA256 != "" {
		return ErrInvalidTrialRecord
	}
	switch value.FinalProgramOutcome {
	case "success":
		if !value.FinalPythonStarted || !digestPattern.MatchString(value.ResultSHA256) || value.ErrorClass != "" {
			return ErrInvalidTrialRecord
		}
	case "syntax_error":
		if value.FinalPythonStarted || value.ResultSHA256 != "" || value.ErrorClass != "syntax_error" || value.LogicalCalls != 0 || value.AuthorityDisposition != "unchanged" || value.WorkspaceDisposition != "untouched" {
			return ErrInvalidTrialRecord
		}
	case "runtime_error":
		if !value.FinalPythonStarted || value.ResultSHA256 != "" || !identifierPattern.MatchString(value.ErrorClass) {
			return ErrInvalidTrialRecord
		}
	case "cancelled":
		if value.ResultSHA256 != "" || value.ErrorClass != "cancelled" {
			return ErrInvalidTrialRecord
		}
	default:
		return ErrInvalidTrialRecord
	}
	if value.PhysicalAttempts == 0 && (value.PhysicalResultBytes != 0 || value.ProviderCostUnits != 0) {
		return ErrInvalidTrialRecord
	}
	if value.AuthorityDisposition == "read_consumed" && value.LogicalCalls == 0 {
		return ErrInvalidTrialRecord
	}
	if sealed {
		expected, err := trialIdentity(value)
		if err != nil || expected != value.Identity {
			return ErrInvalidTrialRecord
		}
	}
	return nil
}

func validTreatment(value string) bool {
	switch value {
	case "serial_whole_file", "eager_style_gate", "semantic_pre_dispatch", "perfect_effect_oracle":
		return true
	default:
		return false
	}
}

func validAuthorityDisposition(value string) bool {
	return value == "unchanged" || value == "read_consumed"
}

func validWorkspaceDisposition(value string) bool {
	return value == "untouched" || value == "published" || value == "discarded"
}

func trialIdentity(value TrialRecord) (string, error) {
	value.Identity = ""
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

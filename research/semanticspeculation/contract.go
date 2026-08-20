// Package semanticspeculation defines body-free, frozen experiment contracts for
// the semantic-speculation research lane. It carries no runtime authority.
package semanticspeculation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"sort"
)

const PreregistrationSchemaVersion = "pysolate.semantic-speculation-preregistration.v1"

const maxPreregistrationBytes = 256 << 10

var (
	ErrInvalidPreregistration = errors.New("invalid semantic-speculation preregistration")
	digestPattern             = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	commitPattern             = regexp.MustCompile(`^[0-9a-f]{40}$`)
	identifierPattern         = regexp.MustCompile(`^[a-z][a-z0-9_]{0,95}$`)
)

type Treatment struct {
	ID         string `json:"id"`
	Executable bool   `json:"executable"`
}

type Case struct {
	ID       string `json:"id"`
	Class    string `json:"class"`
	Baseline string `json:"baseline"`
}

type ClaimBoundary struct {
	Supported []string `json:"supported"`
	Excluded  []string `json:"excluded"`
}

type Preregistration struct {
	SchemaVersion             string        `json:"schema_version"`
	StudyID                   string        `json:"study_id"`
	ParentCommit              string        `json:"parent_commit"`
	ClockPolicy               string        `json:"clock_policy"`
	ShuffleSeed               uint64        `json:"shuffle_seed"`
	TrialsPerTreatment        uint32        `json:"trials_per_treatment"`
	PhysicalDelayMilliseconds uint32        `json:"physical_delay_milliseconds"`
	Treatments                []Treatment   `json:"treatments"`
	Cases                     []Case        `json:"cases"`
	Metrics                   []string      `json:"metrics"`
	RetainedStatuses          []string      `json:"retained_statuses"`
	ClaimBoundary             ClaimBoundary `json:"claim_boundary"`
	Identity                  string        `json:"identity"`
}

func NewV1Preregistration(parentCommit string) (Preregistration, error) {
	if !commitPattern.MatchString(parentCommit) {
		return Preregistration{}, ErrInvalidPreregistration
	}
	value := Preregistration{
		SchemaVersion:             PreregistrationSchemaVersion,
		StudyID:                   "semantic-speculation-v1",
		ParentCommit:              parentCommit,
		ClockPolicy:               "study_relative_monotonic_nanos",
		ShuffleSeed:               20260820,
		TrialsPerTreatment:        5,
		PhysicalDelayMilliseconds: 250,
		Treatments: []Treatment{
			{ID: "eager_style_gate", Executable: true},
			{ID: "perfect_effect_oracle", Executable: false},
			{ID: "semantic_pre_dispatch", Executable: true},
			{ID: "serial_whole_file", Executable: true},
		},
		Cases: []Case{
			{ID: "branch_not_taken", Class: "control", Baseline: "success_no_logical_call"},
			{ID: "earlier_exception", Class: "runtime_error", Baseline: "error_no_logical_call"},
			{ID: "external_read_valid_suffix", Class: "external_read", Baseline: "success_one_logical_call"},
			{ID: "later_runtime_error", Class: "runtime_error", Baseline: "error_one_logical_call"},
			{ID: "later_syntax_error", Class: "syntax_error", Baseline: "parse_error_no_execution"},
			{ID: "pure_local", Class: "pure_local", Baseline: "success_no_logical_call"},
			{ID: "unknown_wrapper", Class: "unknown_effect", Baseline: "success_one_logical_call"},
		},
		Metrics: []string{
			"authority_terminal_disposition", "false_conservative_critical_path_nanos", "logical_call_count",
			"orphaned_physical_bytes", "orphaned_physical_count", "physical_attempt_count", "safe_overlap_coverage",
			"workspace_terminal_disposition",
		},
		RetainedStatuses: []string{"cancelled", "completed", "failed", "rejected", "unclassifiable"},
		ClaimBoundary: ClaimBoundary{
			Supported: []string{"matched_mechanism_comparison", "whole_program_outcome_parity"},
			Excluded:  []string{"production_speedup", "published_eager_implementation_parity", "universal_python_equivalence"},
		},
	}
	if validatePreregistration(value, false) != nil {
		return Preregistration{}, ErrInvalidPreregistration
	}
	return value, nil
}

func SealPreregistration(value Preregistration) (Preregistration, error) {
	if value.Identity != "" || validatePreregistration(value, false) != nil {
		return Preregistration{}, ErrInvalidPreregistration
	}
	cloned := clonePreregistration(value)
	identity, err := preregistrationIdentity(cloned)
	if err != nil {
		return Preregistration{}, ErrInvalidPreregistration
	}
	cloned.Identity = identity
	if validatePreregistration(cloned, true) != nil {
		return Preregistration{}, ErrInvalidPreregistration
	}
	return cloned, nil
}

func EncodePreregistration(value Preregistration) ([]byte, error) {
	if validatePreregistration(value, true) != nil {
		return nil, ErrInvalidPreregistration
	}
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) > maxPreregistrationBytes {
		return nil, ErrInvalidPreregistration
	}
	return encoded, nil
}

func DecodePreregistration(raw []byte) (Preregistration, error) {
	if len(raw) == 0 || len(raw) > maxPreregistrationBytes || rejectDuplicateKeys(raw) != nil {
		return Preregistration{}, ErrInvalidPreregistration
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var value Preregistration
	if decoder.Decode(&value) != nil || decoder.Decode(&struct{}{}) != io.EOF || validatePreregistration(value, true) != nil {
		return Preregistration{}, ErrInvalidPreregistration
	}
	canonical, err := json.Marshal(value)
	if err != nil || !bytes.Equal(canonical, raw) {
		return Preregistration{}, ErrInvalidPreregistration
	}
	return clonePreregistration(value), nil
}

func validatePreregistration(value Preregistration, sealed bool) error {
	if value.SchemaVersion != PreregistrationSchemaVersion || value.StudyID != "semantic-speculation-v1" ||
		!commitPattern.MatchString(value.ParentCommit) || value.ClockPolicy != "study_relative_monotonic_nanos" ||
		value.ShuffleSeed == 0 || value.TrialsPerTreatment != 5 || value.PhysicalDelayMilliseconds < 10 ||
		value.PhysicalDelayMilliseconds > 2000 || len(value.Treatments) != 4 || len(value.Cases) < 7 ||
		len(value.Cases) > 64 || len(value.Metrics) == 0 || len(value.Metrics) > 64 ||
		len(value.RetainedStatuses) != 5 || value.ClaimBoundary.Supported == nil || value.ClaimBoundary.Excluded == nil {
		return ErrInvalidPreregistration
	}
	if sealed != digestPattern.MatchString(value.Identity) {
		return ErrInvalidPreregistration
	}
	if sealed {
		expected, err := preregistrationIdentity(value)
		if err != nil || value.Identity != expected {
			return ErrInvalidPreregistration
		}
	}
	expectedTreatments := []Treatment{
		{ID: "eager_style_gate", Executable: true},
		{ID: "perfect_effect_oracle", Executable: false},
		{ID: "semantic_pre_dispatch", Executable: true},
		{ID: "serial_whole_file", Executable: true},
	}
	for index, treatment := range value.Treatments {
		if treatment != expectedTreatments[index] {
			return ErrInvalidPreregistration
		}
	}
	for index, candidate := range value.Cases {
		if !identifierPattern.MatchString(candidate.ID) || !identifierPattern.MatchString(candidate.Class) || !identifierPattern.MatchString(candidate.Baseline) ||
			(index > 0 && value.Cases[index-1].ID >= candidate.ID) {
			return ErrInvalidPreregistration
		}
	}
	if !sortedUniqueIdentifiers(value.Metrics) || !sortedUniqueIdentifiers(value.RetainedStatuses) ||
		!sortedUniqueIdentifiers(value.ClaimBoundary.Supported) || !sortedUniqueIdentifiers(value.ClaimBoundary.Excluded) {
		return ErrInvalidPreregistration
	}
	for _, forbidden := range []string{"production_speedup", "universal_python_equivalence"} {
		if contains(value.ClaimBoundary.Supported, forbidden) || !contains(value.ClaimBoundary.Excluded, forbidden) {
			return ErrInvalidPreregistration
		}
	}
	return nil
}

func preregistrationIdentity(value Preregistration) (string, error) {
	value.Identity = ""
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func clonePreregistration(value Preregistration) Preregistration {
	value.Treatments = append([]Treatment(nil), value.Treatments...)
	value.Cases = append([]Case(nil), value.Cases...)
	value.Metrics = append([]string(nil), value.Metrics...)
	value.RetainedStatuses = append([]string(nil), value.RetainedStatuses...)
	value.ClaimBoundary.Supported = append([]string(nil), value.ClaimBoundary.Supported...)
	value.ClaimBoundary.Excluded = append([]string(nil), value.ClaimBoundary.Excluded...)
	return value
}

func sortedUniqueIdentifiers(values []string) bool {
	if values == nil || !sort.StringsAreSorted(values) {
		return false
	}
	for index, value := range values {
		if !identifierPattern.MatchString(value) || index > 0 && values[index-1] == value {
			return false
		}
	}
	return true
}

func contains(values []string, expected string) bool {
	index := sort.SearchStrings(values, expected)
	return index < len(values) && values[index] == expected
}

func rejectDuplicateKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var visit func() error
	visit = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]struct{}{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return ErrInvalidPreregistration
				}
				if _, duplicate := seen[key]; duplicate {
					return ErrInvalidPreregistration
				}
				seen[key] = struct{}{}
				if err := visit(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case '[':
			for decoder.More() {
				if err := visit(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		default:
			return ErrInvalidPreregistration
		}
	}
	if err := visit(); err != nil {
		return err
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		return ErrInvalidPreregistration
	}
	return nil
}

package semanticspeculation

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

const Phase4ExtensionMatrixSchemaVersion = "pysolate.semantic-speculation-phase4-extension-matrix.v1"
const Phase4PreregistrationSchemaVersion = "pysolate.semantic-speculation-phase4-preregistration.v1"
const Phase4StudyID = "semantic-speculation-phase4-remediation-v1"
const Phase3FrozenMatrixIdentity = "sha256:f69c31c874d56b7563942bf889a798ed16b38a657fef18be90d4251f49fbee3f"
const Phase4ExtensionMatrixIdentity = "sha256:4cec92655c0f73578f96dc352be13e17aff3376645830ff89f0292e01d15af39"
const Phase4PreregistrationIdentity = "sha256:d17a78fa49fd8699f2d7ae3ec4f183e6e05e50a18d868f8fe54b26b87899676e"
const Phase4ShuffleSeed uint64 = 20260821
const Phase4TrialsPerTreatment uint32 = 5

var phase4Profiles = []string{"cold_end_to_end", "preprovisioned_equivalent_capacity"}
var phase4Treatments = []string{"eager_style_gate", "semantic_pre_dispatch", "serial_whole_file"}

type Phase4Coordinate struct {
	ID                     string                  `json:"id"`
	Shape                  string                  `json:"shape"`
	LeadGapMilliseconds    uint32                  `json:"lead_gap_milliseconds"`
	PhysicalDelayMillis    uint32                  `json:"physical_delay_milliseconds"`
	ExpectedPreDispatch    string                  `json:"expected_pre_dispatch"`
	CandidatePrefixIndices []uint32                `json:"candidate_prefix_indices"`
	Fixture                SyntheticCaseProjection `json:"fixture"`
}

type Phase4ExtensionMatrix struct {
	SchemaVersion              string             `json:"schema_version"`
	StudyID                    string             `json:"study_id"`
	ParentPhase3MatrixIdentity string             `json:"parent_phase3_matrix_identity"`
	ShuffleSeed                uint64             `json:"shuffle_seed"`
	TrialsPerTreatment         uint32             `json:"trials_per_treatment"`
	Profiles                   []string           `json:"profiles"`
	Treatments                 []string           `json:"treatments"`
	Coordinates                []Phase4Coordinate `json:"coordinates"`
	Identity                   string             `json:"identity"`
}

type Phase4EconomicsGate struct {
	EligibleExpectedPreDispatch string `json:"eligible_expected_pre_dispatch"`
	MinimumGapMilliseconds      uint32 `json:"minimum_gap_milliseconds"`
	MinimumPhysicalDelayMillis  uint32 `json:"minimum_physical_delay_milliseconds"`
	MinimumMedianSavingNanos    uint64 `json:"minimum_median_saving_nanos"`
	MinimumReadyTrials          uint32 `json:"minimum_ready_trials"`
	RequiredPassingCoordinates  uint32 `json:"required_passing_coordinates"`
}

type Phase4ProfilePolicy struct {
	ID                     string `json:"id"`
	ClockStart             string `json:"clock_start"`
	ProvisioningAccounting string `json:"provisioning_accounting"`
	CapacityBoundary       string `json:"capacity_boundary"`
}

type Phase4Preregistration struct {
	SchemaVersion              string                `json:"schema_version"`
	StudyID                    string                `json:"study_id"`
	ParentCommit               string                `json:"parent_commit"`
	ParentPhase3MatrixIdentity string                `json:"parent_phase3_matrix_identity"`
	ExtensionMatrixIdentity    string                `json:"extension_matrix_identity"`
	ClockPolicy                string                `json:"clock_policy"`
	Profiles                   []string              `json:"profiles"`
	ProfilePolicies            []Phase4ProfilePolicy `json:"profile_policies"`
	Treatments                 []string              `json:"treatments"`
	Metrics                    []string              `json:"metrics"`
	MechanismRequirements      []string              `json:"mechanism_requirements"`
	EconomicsGate              Phase4EconomicsGate   `json:"economics_gate"`
	ClaimBoundary              map[string][]string   `json:"claim_boundary"`
	Identity                   string                `json:"identity"`
}

type Phase4SyntheticCoordinate struct {
	Shape                  string
	ExpectedPreDispatch    string
	CandidatePrefixIndices []uint32
	PhysicalDelayMillis    uint32
	Fixture                SyntheticCase
}

func Phase4SyntheticCoordinates() []Phase4SyntheticCoordinate {
	inputsEmpty := json.RawMessage(`{}`)
	return []Phase4SyntheticCoordinate{
		{Shape: "direct_read_short_control", ExpectedPreDispatch: "admit_consumed", CandidatePrefixIndices: []uint32{1}, PhysicalDelayMillis: 250, Fixture: SyntheticCase{ID: "direct_read_short_control", Class: "control", Inputs: inputsEmpty, Chunks: []SyntheticChunk{{Source: "value = time.read('weather')\n", ReleaseAfterMilliseconds: 0}, {Source: "result = value\n", ReleaseAfterMilliseconds: 300}}, ExpectedOutcome: "success", ExpectedLogicalCalls: 1}},
		{Shape: "direct_read", ExpectedPreDispatch: "admit_consumed", CandidatePrefixIndices: []uint32{1}, PhysicalDelayMillis: 250, Fixture: SyntheticCase{ID: "direct_read_gap3_low_latency", Class: "positive", Inputs: inputsEmpty, Chunks: []SyntheticChunk{{Source: "value = time.read('weather')\n", ReleaseAfterMilliseconds: 0}, {Source: "result = value\n", ReleaseAfterMilliseconds: 3000}}, ExpectedOutcome: "success", ExpectedLogicalCalls: 1}},
		{Shape: "direct_read", ExpectedPreDispatch: "admit_consumed", CandidatePrefixIndices: []uint32{1}, PhysicalDelayMillis: 6000, Fixture: SyntheticCase{ID: "direct_read_gap3_long_latency", Class: "positive", Inputs: inputsEmpty, Chunks: []SyntheticChunk{{Source: "value = time.read('weather')\n", ReleaseAfterMilliseconds: 0}, {Source: "result = value\n", ReleaseAfterMilliseconds: 3000}}, ExpectedOutcome: "success", ExpectedLogicalCalls: 1}},
		{Shape: "direct_read", ExpectedPreDispatch: "admit_consumed", CandidatePrefixIndices: []uint32{1}, PhysicalDelayMillis: 2500, Fixture: SyntheticCase{ID: "direct_read_gap6_medium_latency", Class: "positive", Inputs: inputsEmpty, Chunks: []SyntheticChunk{{Source: "value = time.read('weather')\n", ReleaseAfterMilliseconds: 0}, {Source: "result = value\n", ReleaseAfterMilliseconds: 6000}}, ExpectedOutcome: "success", ExpectedLogicalCalls: 1}},
		{Shape: "local_then_read", ExpectedPreDispatch: "admit_consumed", CandidatePrefixIndices: []uint32{2}, PhysicalDelayMillis: 6000, Fixture: SyntheticCase{ID: "local_then_read_gap3_long_latency", Class: "positive", Inputs: json.RawMessage(`{"n":41}`), Chunks: []SyntheticChunk{{Source: "base = inputs['n'] + 1\n", ReleaseAfterMilliseconds: 0}, {Source: "value = time.read('weather')\n", ReleaseAfterMilliseconds: 250}, {Source: "result = {'base': base, 'value': value}\n", ReleaseAfterMilliseconds: 3250}}, ExpectedOutcome: "success", ExpectedLogicalCalls: 1}},
		{Shape: "local_then_read", ExpectedPreDispatch: "admit_consumed", CandidatePrefixIndices: []uint32{2}, PhysicalDelayMillis: 2500, Fixture: SyntheticCase{ID: "local_then_read_gap6_medium_latency", Class: "positive", Inputs: json.RawMessage(`{"n":41}`), Chunks: []SyntheticChunk{{Source: "base = inputs['n'] + 1\n", ReleaseAfterMilliseconds: 0}, {Source: "value = time.read('weather')\n", ReleaseAfterMilliseconds: 250}, {Source: "result = {'base': base, 'value': value}\n", ReleaseAfterMilliseconds: 6250}}, ExpectedOutcome: "success", ExpectedLogicalCalls: 1}},
		{Shape: "read_then_runtime_error", ExpectedPreDispatch: "admit_consumed", CandidatePrefixIndices: []uint32{1}, PhysicalDelayMillis: 2500, Fixture: SyntheticCase{ID: "later_runtime_error_gap6_medium_latency", Class: "adversarial", Inputs: inputsEmpty, Chunks: []SyntheticChunk{{Source: "value = time.read('weather')\n", ReleaseAfterMilliseconds: 0}, {Source: "raise RuntimeError('after')\n", ReleaseAfterMilliseconds: 6000}, {Source: "result = value\n", ReleaseAfterMilliseconds: 6250}}, ExpectedOutcome: "runtime_error", ExpectedLogicalCalls: 1}},
		{Shape: "read_then_syntax_error", ExpectedPreDispatch: "admit_discarded", CandidatePrefixIndices: []uint32{1}, PhysicalDelayMillis: 2500, Fixture: SyntheticCase{ID: "later_syntax_error_gap6_medium_latency", Class: "adversarial", Inputs: inputsEmpty, Chunks: []SyntheticChunk{{Source: "value = time.read('weather')\n", ReleaseAfterMilliseconds: 0}, {Source: "result = )\n", ReleaseAfterMilliseconds: 6000}}, ExpectedOutcome: "syntax_error", ExpectedLogicalCalls: 0}},
		{Shape: "earlier_exception", ExpectedPreDispatch: "must_not_admit", CandidatePrefixIndices: []uint32{1, 2}, PhysicalDelayMillis: 2500, Fixture: SyntheticCase{ID: "earlier_exception_gap6_medium_latency", Class: "adversarial", Inputs: inputsEmpty, Chunks: []SyntheticChunk{{Source: "raise RuntimeError('before')\n", ReleaseAfterMilliseconds: 0}, {Source: "value = time.read('weather')\n", ReleaseAfterMilliseconds: 250}, {Source: "result = value\n", ReleaseAfterMilliseconds: 6250}}, ExpectedOutcome: "runtime_error", ExpectedLogicalCalls: 0}},
		{Shape: "branch_not_taken", ExpectedPreDispatch: "must_not_admit", CandidatePrefixIndices: []uint32{1}, PhysicalDelayMillis: 2500, Fixture: SyntheticCase{ID: "branch_not_taken_gap6_medium_latency", Class: "negative_control", Inputs: inputsEmpty, Chunks: []SyntheticChunk{{Source: "if False:\n    value = time.read('weather')\n", ReleaseAfterMilliseconds: 0}, {Source: "result = {'ok': True}\n", ReleaseAfterMilliseconds: 6000}}, ExpectedOutcome: "success", ExpectedLogicalCalls: 0}},
		{Shape: "unknown_wrapper", ExpectedPreDispatch: "must_not_admit", CandidatePrefixIndices: []uint32{1, 2}, PhysicalDelayMillis: 2500, Fixture: SyntheticCase{ID: "unknown_wrapper_gap6_medium_latency", Class: "negative_control", Inputs: inputsEmpty, Chunks: []SyntheticChunk{{Source: "def fetch():\n    return time.read('weather')\n", ReleaseAfterMilliseconds: 0}, {Source: "value = fetch()\n", ReleaseAfterMilliseconds: 250}, {Source: "result = value\n", ReleaseAfterMilliseconds: 6250}}, ExpectedOutcome: "success", ExpectedLogicalCalls: 1}},
		{Shape: "pure_local", ExpectedPreDispatch: "must_not_admit", CandidatePrefixIndices: []uint32{1, 2}, PhysicalDelayMillis: 2500, Fixture: SyntheticCase{ID: "pure_local_gap6_control", Class: "control", Inputs: json.RawMessage(`{"n":3}`), Chunks: []SyntheticChunk{{Source: "value = inputs['n'] + 1\n", ReleaseAfterMilliseconds: 0}, {Source: "result = value * 2\n", ReleaseAfterMilliseconds: 6000}}, ExpectedOutcome: "success", ExpectedLogicalCalls: 0}},
	}
}

func Phase4ExtensionFixtures() []Phase4Coordinate {
	rows := Phase4SyntheticCoordinates()
	coordinates := make([]Phase4Coordinate, len(rows))
	for index, row := range rows {
		candidateRelease := row.Fixture.Chunks[row.CandidatePrefixIndices[len(row.CandidatePrefixIndices)-1]-1].ReleaseAfterMilliseconds
		leadGap := row.Fixture.Chunks[len(row.Fixture.Chunks)-1].ReleaseAfterMilliseconds - candidateRelease
		coordinates[index] = Phase4Coordinate{ID: row.Fixture.ID, Shape: row.Shape, LeadGapMilliseconds: leadGap, PhysicalDelayMillis: row.PhysicalDelayMillis, ExpectedPreDispatch: row.ExpectedPreDispatch, CandidatePrefixIndices: append([]uint32(nil), row.CandidatePrefixIndices...), Fixture: row.Fixture.Projection()}
	}
	return coordinates
}

func NewPhase4ExtensionMatrix() Phase4ExtensionMatrix {
	return Phase4ExtensionMatrix{SchemaVersion: Phase4ExtensionMatrixSchemaVersion, StudyID: Phase4StudyID, ParentPhase3MatrixIdentity: Phase3FrozenMatrixIdentity, ShuffleSeed: Phase4ShuffleSeed, TrialsPerTreatment: Phase4TrialsPerTreatment, Profiles: append([]string(nil), phase4Profiles...), Treatments: append([]string(nil), phase4Treatments...), Coordinates: Phase4ExtensionFixtures()}
}

func NewPhase4Preregistration(parentCommit, extensionMatrixIdentity string) Phase4Preregistration {
	policies := []Phase4ProfilePolicy{
		{ID: "cold_end_to_end", ClockStart: "before_treatment_required_runtime_provisioning", ProvisioningAccounting: "included_in_total_elapsed_and_reported_separately", CapacityBoundary: "compiled_artifact_only_no_initialized_guest_capacity"},
		{ID: "preprovisioned_equivalent_capacity", ClockStart: "after_each_lane_required_never_served_capacity_is_ready", ProvisioningAccounting: "excluded_from_steady_state_but_reported_separately", CapacityBoundary: "all_lane_required_capacity_is_private_single_use_never_served_and_not_shared_across_runs"},
	}
	return Phase4Preregistration{SchemaVersion: Phase4PreregistrationSchemaVersion, StudyID: Phase4StudyID, ParentCommit: parentCommit, ParentPhase3MatrixIdentity: Phase3FrozenMatrixIdentity, ExtensionMatrixIdentity: extensionMatrixIdentity, ClockPolicy: "study_relative_monotonic_nanos", Profiles: append([]string(nil), phase4Profiles...), ProfilePolicies: policies, Treatments: append([]string(nil), phase4Treatments...), Metrics: []string{"admission_nanos", "analyzer_invocations", "analyzer_session_count", "authority_terminal_disposition", "discarded_capacity_bytes", "formal_execution_nanos", "logical_call_count", "orphaned_physical_count", "physical_attempt_count", "prepared_or_cow_fallback_count", "prepared_or_cow_hit_count", "provider_nanos", "ready_before_finalize", "resident_memory_bytes", "runtime_init_nanos", "total_elapsed_nanos", "workspace_terminal_disposition"}, MechanismRequirements: []string{"all_trials_match_serial_outcome_logical_calls_authority_and_workspace", "formal_execution_uses_fresh_guest_and_unchanged_original_source", "no_cross_run_analyzer_session_reuse", "no_orphaned_physical_attempts", "one_bounded_private_analyzer_session_per_source_generation_run", "target_guest_analysis_only_at_declared_candidate_prefixes"}, EconomicsGate: Phase4EconomicsGate{EligibleExpectedPreDispatch: "admit_consumed", MinimumGapMilliseconds: 3000, MinimumPhysicalDelayMillis: 2500, MinimumMedianSavingNanos: 100000000, MinimumReadyTrials: 4, RequiredPassingCoordinates: 1}, ClaimBoundary: map[string][]string{"supported": {"named_synthetic_regime_mechanism", "named_synthetic_regime_economics", "whole_program_outcome_parity"}, "excluded": {"natural_workload_prevalence", "production_general_speedup", "universal_python_equivalence"}}}
}

func SealPhase4ExtensionMatrix(value Phase4ExtensionMatrix) (Phase4ExtensionMatrix, error) {
	value.Identity = ""
	if err := validatePhase4ExtensionMatrix(value, false); err != nil {
		return Phase4ExtensionMatrix{}, err
	}
	raw, _ := json.Marshal(value)
	value.Identity = syntheticDigest(raw)
	if value.Identity != Phase4ExtensionMatrixIdentity {
		return Phase4ExtensionMatrix{}, errors.New("phase 4 extension matrix drift")
	}
	return value, nil
}

func SealPhase4Preregistration(value Phase4Preregistration) (Phase4Preregistration, error) {
	value.Identity = ""
	if err := validatePhase4Preregistration(value, false); err != nil {
		return Phase4Preregistration{}, err
	}
	raw, _ := json.Marshal(value)
	value.Identity = syntheticDigest(raw)
	if value.Identity != Phase4PreregistrationIdentity {
		return Phase4Preregistration{}, errors.New("phase 4 preregistration drift")
	}
	return value, nil
}

func EncodePhase4ExtensionMatrix(value Phase4ExtensionMatrix) ([]byte, error) {
	if value.Identity != Phase4ExtensionMatrixIdentity || validatePhase4ExtensionMatrix(value, true) != nil {
		return nil, errors.New("invalid phase 4 extension matrix")
	}
	return json.Marshal(value)
}

func EncodePhase4Preregistration(value Phase4Preregistration) ([]byte, error) {
	if value.Identity != Phase4PreregistrationIdentity || validatePhase4Preregistration(value, true) != nil {
		return nil, errors.New("invalid phase 4 preregistration")
	}
	return json.Marshal(value)
}

func DecodePhase4ExtensionMatrix(raw []byte) (Phase4ExtensionMatrix, error) {
	var value Phase4ExtensionMatrix
	if err := decodePhase4Strict(raw, &value); err != nil || value.Identity != Phase4ExtensionMatrixIdentity || validatePhase4ExtensionMatrix(value, true) != nil {
		return Phase4ExtensionMatrix{}, errors.New("invalid phase 4 extension matrix")
	}
	return value, nil
}

func DecodePhase4Preregistration(raw []byte) (Phase4Preregistration, error) {
	var value Phase4Preregistration
	if err := decodePhase4Strict(raw, &value); err != nil || value.Identity != Phase4PreregistrationIdentity || validatePhase4Preregistration(value, true) != nil {
		return Phase4Preregistration{}, errors.New("invalid phase 4 preregistration")
	}
	return value, nil
}

func validatePhase4ExtensionMatrix(value Phase4ExtensionMatrix, sealed bool) error {
	if value.SchemaVersion != Phase4ExtensionMatrixSchemaVersion || value.StudyID != Phase4StudyID || value.ParentPhase3MatrixIdentity != Phase3FrozenMatrixIdentity || value.ShuffleSeed != Phase4ShuffleSeed || value.TrialsPerTreatment != Phase4TrialsPerTreatment || !stringSlicesEqual(value.Profiles, phase4Profiles) || !stringSlicesEqual(value.Treatments, phase4Treatments) || len(value.Coordinates) != 12 {
		return errors.New("invalid phase 4 extension matrix")
	}
	seen := map[string]bool{}
	for _, coordinate := range value.Coordinates {
		if seen[coordinate.ID] || coordinate.ID != coordinate.Fixture.ID || !identifierPattern.MatchString(coordinate.ID) || !identifierPattern.MatchString(coordinate.Shape) || coordinate.PhysicalDelayMillis == 0 || len(coordinate.CandidatePrefixIndices) == 0 || !validPhase4ExpectedPreDispatch(coordinate.ExpectedPreDispatch) {
			return errors.New("invalid phase 4 coordinate")
		}
		seen[coordinate.ID] = true
		if _, err := EncodeSyntheticCaseProjection(coordinate.Fixture); err != nil {
			return err
		}
		for index, prefix := range coordinate.CandidatePrefixIndices {
			if prefix == 0 || int(prefix) > len(coordinate.Fixture.Chunks) || (index > 0 && coordinate.CandidatePrefixIndices[index-1] >= prefix) {
				return errors.New("invalid phase 4 candidate prefixes")
			}
		}
		candidateRelease := coordinate.Fixture.Chunks[coordinate.CandidatePrefixIndices[len(coordinate.CandidatePrefixIndices)-1]-1].ReleaseAfterMilliseconds
		finalRelease := coordinate.Fixture.Chunks[len(coordinate.Fixture.Chunks)-1].ReleaseAfterMilliseconds
		if finalRelease < candidateRelease || coordinate.LeadGapMilliseconds != finalRelease-candidateRelease {
			return errors.New("invalid phase 4 lead gap")
		}
	}
	return validatePhase4Identity(value.Identity, sealed, func() string {
		candidate := value
		candidate.Identity = ""
		raw, _ := json.Marshal(candidate)
		return syntheticDigest(raw)
	}())
}

func validatePhase4Preregistration(value Phase4Preregistration, sealed bool) error {
	gate := value.EconomicsGate
	if value.SchemaVersion != Phase4PreregistrationSchemaVersion || value.StudyID != Phase4StudyID || !commitPattern.MatchString(value.ParentCommit) || value.ParentPhase3MatrixIdentity != Phase3FrozenMatrixIdentity || !digestPattern.MatchString(value.ExtensionMatrixIdentity) || value.ClockPolicy != "study_relative_monotonic_nanos" || !stringSlicesEqual(value.Profiles, phase4Profiles) || !validPhase4ProfilePolicies(value.ProfilePolicies) || !stringSlicesEqual(value.Treatments, phase4Treatments) || len(value.Metrics) == 0 || len(value.MechanismRequirements) == 0 || len(value.ClaimBoundary["supported"]) == 0 || len(value.ClaimBoundary["excluded"]) == 0 || gate.EligibleExpectedPreDispatch != "admit_consumed" || gate.MinimumGapMilliseconds != 3000 || gate.MinimumPhysicalDelayMillis != 2500 || gate.MinimumMedianSavingNanos != 100000000 || gate.MinimumReadyTrials != 4 || gate.RequiredPassingCoordinates != 1 {
		return errors.New("invalid phase 4 preregistration")
	}
	return validatePhase4Identity(value.Identity, sealed, func() string {
		candidate := value
		candidate.Identity = ""
		raw, _ := json.Marshal(candidate)
		return syntheticDigest(raw)
	}())
}

func validatePhase4Identity(actual string, sealed bool, expected string) error {
	if !sealed && actual == "" {
		return nil
	}
	if !digestPattern.MatchString(actual) || actual != expected {
		return errors.New("invalid phase 4 identity")
	}
	return nil
}

func validPhase4ExpectedPreDispatch(value string) bool {
	return value == "admit_consumed" || value == "admit_discarded" || value == "must_not_admit"
}

func validPhase4ProfilePolicies(value []Phase4ProfilePolicy) bool {
	if len(value) != 2 {
		return false
	}
	return value[0] == (Phase4ProfilePolicy{ID: "cold_end_to_end", ClockStart: "before_treatment_required_runtime_provisioning", ProvisioningAccounting: "included_in_total_elapsed_and_reported_separately", CapacityBoundary: "compiled_artifact_only_no_initialized_guest_capacity"}) &&
		value[1] == (Phase4ProfilePolicy{ID: "preprovisioned_equivalent_capacity", ClockStart: "after_each_lane_required_never_served_capacity_is_ready", ProvisioningAccounting: "excluded_from_steady_state_but_reported_separately", CapacityBoundary: "all_lane_required_capacity_is_private_single_use_never_served_and_not_shared_across_runs"})
}

func stringSlicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
func decodePhase4Strict(raw []byte, destination any) error {
	if len(raw) == 0 || len(raw) > maxPreregistrationBytes {
		return errors.New("invalid phase 4 JSON size")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("trailing phase 4 JSON")
	}
	canonical, err := json.Marshal(destination)
	if err != nil || !bytes.Equal(raw, canonical) {
		return errors.New("phase 4 JSON must be canonical")
	}
	return nil
}

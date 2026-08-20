package semanticspeculation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"strconv"
	"strings"
)

const Phase5CaseMatrixSchemaVersion = "pysolate.semantic-speculation-phase5-case-matrix.v1"
const Phase5PreregistrationSchemaVersion = "pysolate.semantic-speculation-phase5-preregistration.v1"
const Phase5StudyID = "semantic-speculation-phase5-scalar-materialisation-v1"
const Phase5ParentCommit = "fa8d293df74c3338388cb0ecb8c002ed66819cde"
const Phase5GuestArtifactSHA256 = "sha256:621f5fcec3f4bc7fc3550aa8fd1a275e7a6c09017518f535395c5bae84a297cb"
const Phase5GuestManifestSHA256 = "sha256:ad1affb32a14f0feababcf5c3c6fd614aa699ac71a97b9ec44278bdf6098ad9c"
const Phase5CaseMatrixIdentity = "sha256:e4025295cc47cdc62925f4a4e0b0d3f072726de9aff983c75a0b9187fd355cee"
const Phase5PreregistrationIdentity = "sha256:9db34a4fa8091bd9875132457dfcf9515fbf78802a5f0453029a4f52e1f776c6"
const Phase5ShuffleSeed uint64 = 20260822
const Phase5TrialsPerTreatment uint32 = 5

var phase5Profiles = []string{"cold_end_to_end", "preprovisioned_equivalent_capacity"}
var phase5Treatments = []string{"original_unchanged", "prepared_region_derived"}

type Phase5Case struct {
	ID                    string          `json:"id"`
	Class                 string          `json:"class"`
	Source                string          `json:"source"`
	SourceSHA256          string          `json:"source_sha256"`
	FocusRegionIndex      uint32          `json:"focus_region_index"`
	RegionSourceSHA256    string          `json:"region_source_sha256"`
	OutputName            string          `json:"output_name"`
	Operation             string          `json:"operation"`
	OperatorCount         uint32          `json:"operator_count"`
	FinalizationGapMillis uint32          `json:"finalization_gap_millis"`
	ControlAction         string          `json:"control_action"`
	ExpectedDisposition   string          `json:"expected_disposition"`
	ExpectedOutcome       string          `json:"expected_outcome"`
	ExpectedResult        json.RawMessage `json:"expected_result"`
	EconomicsEligible     bool            `json:"economics_eligible"`
	RequiredControlTags   []string        `json:"required_control_tags"`
}

type Phase5CaseMatrix struct {
	SchemaVersion              string       `json:"schema_version"`
	StudyID                    string       `json:"study_id"`
	ParentCommit               string       `json:"parent_commit"`
	ParentPhase3MatrixIdentity string       `json:"parent_phase3_matrix_identity"`
	ParentPhase4MatrixIdentity string       `json:"parent_phase4_matrix_identity"`
	ParentPhase4PreregIdentity string       `json:"parent_phase4_preregistration_identity"`
	GuestArtifactSHA256        string       `json:"guest_artifact_sha256"`
	GuestManifestSHA256        string       `json:"guest_manifest_sha256"`
	ShuffleSeed                uint64       `json:"shuffle_seed"`
	TrialsPerTreatment         uint32       `json:"trials_per_treatment"`
	Profiles                   []string     `json:"profiles"`
	Treatments                 []string     `json:"treatments"`
	Cases                      []Phase5Case `json:"cases"`
	Identity                   string       `json:"identity"`
}

type Phase5ProfilePolicy struct {
	ID                     string `json:"id"`
	ClockStart             string `json:"clock_start"`
	ProvisioningAccounting string `json:"provisioning_accounting"`
	CapacityBoundary       string `json:"capacity_boundary"`
}

type Phase5MechanismGate struct {
	RequiredCaseIDs                []string `json:"required_case_ids"`
	RequireAllExactControlsPass    bool     `json:"require_all_exact_controls_pass"`
	RequireCanonicalTrialRecords   bool     `json:"require_canonical_trial_records"`
	RequireBaselineDerivedParity   bool     `json:"require_baseline_derived_parity"`
	RequireZeroAuthorityExpansion  bool     `json:"require_zero_authority_expansion"`
	RequireNoReplayOrRecomputation bool     `json:"require_no_replay_or_recomputation"`
}

type Phase5EconomicsGate struct {
	EligibleClass                        string `json:"eligible_class"`
	MinimumPositiveTrialsPerCell         uint32 `json:"minimum_positive_trials_per_cell"`
	MinimumMedianNetSavingNanos          uint64 `json:"minimum_median_net_saving_nanos"`
	RequiredPassingCoordinatesPerProfile uint32 `json:"required_passing_coordinates_per_profile"`
	RequireBothProfilesPass              bool   `json:"require_both_profiles_pass"`
	Comparison                           string `json:"comparison"`
}

type Phase5Preregistration struct {
	SchemaVersion           string                `json:"schema_version"`
	StudyID                 string                `json:"study_id"`
	ParentCommit            string                `json:"parent_commit"`
	CaseMatrixIdentity      string                `json:"case_matrix_identity"`
	ClockPolicy             string                `json:"clock_policy"`
	Profiles                []string              `json:"profiles"`
	ProfilePolicies         []Phase5ProfilePolicy `json:"profile_policies"`
	Treatments              []string              `json:"treatments"`
	StageMetrics            []string              `json:"stage_metrics"`
	TrialRecordRequirements []string              `json:"trial_record_requirements"`
	MechanismGate           Phase5MechanismGate   `json:"mechanism_gate"`
	EconomicsGate           Phase5EconomicsGate   `json:"economics_gate"`
	FailureAction           string                `json:"failure_action"`
	ClaimBoundary           map[string][]string   `json:"claim_boundary"`
	Identity                string                `json:"identity"`
}

type Phase5CampaignCoordinate struct {
	Profile    string `json:"profile"`
	CaseID     string `json:"case_id"`
	Treatment  string `json:"treatment"`
	TrialIndex uint32 `json:"trial_index"`
}

func phase5ScalarSource(operation string, operatorCount int) (string, string, json.RawMessage) {
	operator := "+ 1"
	result := operatorCount + 1
	if operation == "integer_multiply" {
		operator = "* 1"
		result = 1
	}
	region := "value = seed " + strings.Repeat(operator+" ", operatorCount)
	region = strings.TrimSpace(region) + "\n"
	return "seed = 1\n" + region + "result = value\n", region, json.RawMessage(strconv.Itoa(result))
}

func phase5Case(id, class, source, region string, operation string, operators, gap uint32, action, disposition, outcome string, expected json.RawMessage, eligible bool, tags ...string) Phase5Case {
	return Phase5Case{ID: id, Class: class, Source: source, SourceSHA256: syntheticDigest([]byte(source)), FocusRegionIndex: 1, RegionSourceSHA256: syntheticDigest([]byte(region)), OutputName: "value", Operation: operation, OperatorCount: operators, FinalizationGapMillis: gap, ControlAction: action, ExpectedDisposition: disposition, ExpectedOutcome: outcome, ExpectedResult: expected, EconomicsEligible: eligible, RequiredControlTags: append([]string(nil), tags...)}
}

func Phase5Cases() []Phase5Case {
	pilotSource, pilotRegion, pilotResult := phase5ScalarSource("integer_add", 2)
	add16Source, add16Region, add16Result := phase5ScalarSource("integer_add", 16)
	add64Source, add64Region, add64Result := phase5ScalarSource("integer_add", 64)
	mul256Source, mul256Region, mul256Result := phase5ScalarSource("integer_multiply", 256)
	add512Source, add512Region, add512Result := phase5ScalarSource("integer_add", 512)
	overflowSource := "seed = 9223372036854775807\nvalue = seed + 1\nresult = value\n"
	unsafeSource := "seed = -1\nvalue = abs(seed)\nresult = value\n"
	preSource := "seed = 1\nraise ValueError('before region')\nvalue = seed + 1\nresult = value\n"
	postSource := "seed = 1\nvalue = seed + 1\nraise LookupError('after region')\nresult = value\n"
	preException := phase5Case("exception_before_region", "adversarial", preSource, "value = seed + 1\n", "integer_add", 1, 250, "manual_sealed_control", "ready_unclaimed", "python_exception", json.RawMessage("null"), false, "exception_parity", "zero_claims", "capsule_discarded")
	preException.FocusRegionIndex = 2
	return []Phase5Case{
		phase5Case("scalar_add_2_pilot", "pilot_only", pilotSource, pilotRegion, "integer_add", 2, 0, "none", "ready_consumed", "success", pilotResult, false, "pilot_excluded", "result_parity"),
		phase5Case("scalar_add_16_gap0", "positive", add16Source, add16Region, "integer_add", 16, 0, "none", "ready_consumed", "success", add16Result, true, "zero_gap", "result_parity"),
		phase5Case("scalar_add_64_gap250", "positive", add64Source, add64Region, "integer_add", 64, 250, "none", "ready_consumed", "success", add64Result, true, "short_gap", "result_parity"),
		phase5Case("scalar_multiply_256_gap1000", "positive", mul256Source, mul256Region, "integer_multiply", 256, 1000, "none", "ready_consumed", "success", mul256Result, true, "medium_gap", "result_parity"),
		phase5Case("scalar_add_512_gap6000", "positive", add512Source, add512Region, "integer_add", 512, 6000, "none", "ready_consumed", "success", add512Result, true, "long_gap", "result_parity"),
		phase5Case("scalar_int64_overflow", "negative_control", overflowSource, "value = seed + 1\n", "integer_add", 1, 250, "none", "scratch_rejected_original_only", "success", json.RawMessage("9223372036854775808"), false, "typed_range_rejection", "no_capsule_publication"),
		phase5Case("scalar_unsafe_call", "negative_control", unsafeSource, "value = abs(seed)\n", "unknown_call", 0, 250, "none", "analyzer_rejected_original_only", "success", json.RawMessage("1"), false, "unsafe_rhs_rejection", "no_scratch_execution"),
		phase5Case("derived_suffix_drift", "adversarial", pilotSource, pilotRegion, "integer_add", 2, 250, "append_invalid_suffix", "preexecution_rejected", "host_error", json.RawMessage("null"), false, "selection_identity_drift", "zero_claims"),
		preException,
		phase5Case("exception_after_region", "adversarial", postSource, "value = seed + 1\n", "integer_add", 1, 250, "manual_sealed_control", "ready_consumed", "python_exception", json.RawMessage("null"), false, "exception_parity", "traceback_location_parity", "single_claim"),
		phase5Case("pre_cancelled_final_execution", "adversarial", pilotSource, pilotRegion, "integer_add", 2, 250, "cancel_before_final_run", "ready_unclaimed", "host_cancelled", json.RawMessage("null"), false, "zero_claims", "capsule_discarded", "no_guest_response"),
	}
}

func NewPhase5CaseMatrix() Phase5CaseMatrix {
	return Phase5CaseMatrix{SchemaVersion: Phase5CaseMatrixSchemaVersion, StudyID: Phase5StudyID, ParentCommit: Phase5ParentCommit, ParentPhase3MatrixIdentity: Phase3FrozenMatrixIdentity, ParentPhase4MatrixIdentity: Phase4ExtensionMatrixIdentity, ParentPhase4PreregIdentity: Phase4PreregistrationIdentity, GuestArtifactSHA256: Phase5GuestArtifactSHA256, GuestManifestSHA256: Phase5GuestManifestSHA256, ShuffleSeed: Phase5ShuffleSeed, TrialsPerTreatment: Phase5TrialsPerTreatment, Profiles: append([]string(nil), phase5Profiles...), Treatments: append([]string(nil), phase5Treatments...), Cases: Phase5Cases()}
}

func NewPhase5Preregistration(matrixIdentity string) Phase5Preregistration {
	caseIDs := make([]string, 0, len(Phase5Cases()))
	for _, candidate := range Phase5Cases() {
		caseIDs = append(caseIDs, candidate.ID)
	}
	return Phase5Preregistration{
		SchemaVersion: Phase5PreregistrationSchemaVersion, StudyID: Phase5StudyID, ParentCommit: Phase5ParentCommit, CaseMatrixIdentity: matrixIdentity, ClockPolicy: "study_relative_monotonic_nanos",
		Profiles: append([]string(nil), phase5Profiles...),
		ProfilePolicies: []Phase5ProfilePolicy{
			{ID: "cold_end_to_end", ClockStart: "when_focus_region_and_live_ins_are_available_before_any_treatment_runtime_provisioning", ProvisioningAccounting: "included_in_critical_path_and_reported_per_stage", CapacityBoundary: "compiled_artifact_only_no_initialized_guest_capacity"},
			{ID: "preprovisioned_equivalent_capacity", ClockStart: "when_focus_region_and_live_ins_are_available_after_each_treatment_required_never_served_capacity_is_ready", ProvisioningAccounting: "excluded_from_critical_path_but_reported_per_stage_memory_and_discard", CapacityBoundary: "original_has_one_final_capacity_derived_has_analyzer_scratch_and_final_capacities_each_private_single_use_never_served"},
		},
		Treatments:              append([]string(nil), phase5Treatments...),
		StageMetrics:            []string{"analysis_nanos", "analyzer_provision_nanos", "analyzer_runtime_init_count", "capsule_bytes", "capsule_seal_transport_nanos", "discarded_capacity_bytes", "final_execution_nanos", "final_guest_provision_nanos", "final_patch_compile_load_nanos", "final_selection_validation_nanos", "finalization_gap_nanos", "helper_claim_count", "logical_call_count", "orphaned_physical_count", "patch_emission_nanos", "peak_resident_memory_bytes", "scratch_execution_nanos", "scratch_guest_provision_nanos", "scratch_runtime_init_count", "teardown_nanos", "total_critical_path_nanos", "workspace_terminal_disposition"},
		TrialRecordRequirements: []string{"body_free_canonical_record", "exact_case_profile_treatment_trial_identity", "monotonic_stage_intervals", "original_source_and_input_identity", "separate_process_exit_and_protocol_status", "stage_sum_reconciles_with_total_critical_path", "table_claim_consumption_discard_evidence"},
		MechanismGate:           Phase5MechanismGate{RequiredCaseIDs: caseIDs, RequireAllExactControlsPass: true, RequireCanonicalTrialRecords: true, RequireBaselineDerivedParity: true, RequireZeroAuthorityExpansion: true, RequireNoReplayOrRecomputation: true},
		EconomicsGate:           Phase5EconomicsGate{EligibleClass: "positive", MinimumPositiveTrialsPerCell: 4, MinimumMedianNetSavingNanos: 1, RequiredPassingCoordinatesPerProfile: 2, RequireBothProfilesPass: true, Comparison: "original_unchanged_total_critical_path_nanos_minus_prepared_region_derived_total_critical_path_nanos"},
		FailureAction:           "record_no_go_retain_original_execution_and_do_not_expand_transport_or_authority",
		ClaimBoundary:           map[string][]string{"supported": {"authored_scalar_mechanism_parity", "frozen_synthetic_profile_economics"}, "excluded": {"natural_workload_prevalence", "persistent_result_cache", "production_result_replacement", "generic_typed_transport", "arbitrary_python_heap_transfer", "authority_expansion"}},
	}
}

func Phase5CampaignCoordinates() []Phase5CampaignCoordinate {
	matrix := NewPhase5CaseMatrix()
	coordinates := make([]Phase5CampaignCoordinate, 0, 80)
	for _, profile := range matrix.Profiles {
		for _, candidate := range matrix.Cases {
			if !candidate.EconomicsEligible {
				continue
			}
			for _, treatment := range matrix.Treatments {
				for trial := uint32(1); trial <= matrix.TrialsPerTreatment; trial++ {
					coordinates = append(coordinates, Phase5CampaignCoordinate{Profile: profile, CaseID: candidate.ID, Treatment: treatment, TrialIndex: trial})
				}
			}
		}
	}
	random := rand.New(rand.NewSource(int64(matrix.ShuffleSeed)))
	random.Shuffle(len(coordinates), func(i, j int) { coordinates[i], coordinates[j] = coordinates[j], coordinates[i] })
	return coordinates
}

func SealPhase5CaseMatrix(value Phase5CaseMatrix) (Phase5CaseMatrix, error) {
	value.Identity = ""
	if err := validatePhase5CaseMatrix(value, false); err != nil {
		return Phase5CaseMatrix{}, err
	}
	raw, _ := json.Marshal(value)
	value.Identity = syntheticDigest(raw)
	if value.Identity != Phase5CaseMatrixIdentity {
		return Phase5CaseMatrix{}, fmt.Errorf("phase 5 case matrix drift: got %s", value.Identity)
	}
	return value, nil
}

func SealPhase5Preregistration(value Phase5Preregistration) (Phase5Preregistration, error) {
	value.Identity = ""
	if err := validatePhase5Preregistration(value, false); err != nil {
		return Phase5Preregistration{}, err
	}
	raw, _ := json.Marshal(value)
	value.Identity = syntheticDigest(raw)
	if value.Identity != Phase5PreregistrationIdentity {
		return Phase5Preregistration{}, fmt.Errorf("phase 5 preregistration drift: got %s", value.Identity)
	}
	return value, nil
}

func EncodePhase5CaseMatrix(value Phase5CaseMatrix) ([]byte, error) {
	if err := validatePhase5CaseMatrix(value, true); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}
func EncodePhase5Preregistration(value Phase5Preregistration) ([]byte, error) {
	if err := validatePhase5Preregistration(value, true); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}
func DecodePhase5CaseMatrix(raw []byte) (Phase5CaseMatrix, error) {
	var value Phase5CaseMatrix
	if err := decodePhase5Strict(raw, &value); err != nil || validatePhase5CaseMatrix(value, true) != nil {
		return Phase5CaseMatrix{}, errors.New("invalid phase 5 case matrix")
	}
	return value, nil
}
func DecodePhase5Preregistration(raw []byte) (Phase5Preregistration, error) {
	var value Phase5Preregistration
	if err := decodePhase5Strict(raw, &value); err != nil || validatePhase5Preregistration(value, true) != nil {
		return Phase5Preregistration{}, errors.New("invalid phase 5 preregistration")
	}
	return value, nil
}

func validatePhase5CaseMatrix(value Phase5CaseMatrix, sealed bool) error {
	if value.SchemaVersion != Phase5CaseMatrixSchemaVersion || value.StudyID != Phase5StudyID || value.ParentCommit != Phase5ParentCommit || value.ParentPhase3MatrixIdentity != Phase3FrozenMatrixIdentity || value.ParentPhase4MatrixIdentity != Phase4ExtensionMatrixIdentity || value.ParentPhase4PreregIdentity != Phase4PreregistrationIdentity || value.GuestArtifactSHA256 != Phase5GuestArtifactSHA256 || value.GuestManifestSHA256 != Phase5GuestManifestSHA256 || value.ShuffleSeed != Phase5ShuffleSeed || value.TrialsPerTreatment != Phase5TrialsPerTreatment || !stringSlicesEqual(value.Profiles, phase5Profiles) || !stringSlicesEqual(value.Treatments, phase5Treatments) || len(value.Cases) != 11 {
		return errors.New("invalid phase 5 case matrix")
	}
	expected, _ := json.Marshal(Phase5Cases())
	actual, _ := json.Marshal(value.Cases)
	if !bytes.Equal(actual, expected) {
		return errors.New("phase 5 cases drift")
	}
	if sealed && value.Identity != Phase5CaseMatrixIdentity {
		return errors.New("phase 5 case matrix identity drift")
	}
	if !sealed && value.Identity != "" {
		return errors.New("unsealed phase 5 case matrix has identity")
	}
	return nil
}

func validatePhase5Preregistration(value Phase5Preregistration, sealed bool) error {
	expected := NewPhase5Preregistration(Phase5CaseMatrixIdentity)
	expected.Identity = value.Identity
	rawExpected, _ := json.Marshal(expected)
	rawActual, _ := json.Marshal(value)
	if !bytes.Equal(rawActual, rawExpected) || value.CaseMatrixIdentity != Phase5CaseMatrixIdentity {
		return errors.New("phase 5 preregistration drift")
	}
	if sealed && value.Identity != Phase5PreregistrationIdentity {
		return errors.New("phase 5 preregistration identity drift")
	}
	if !sealed && value.Identity != "" {
		return errors.New("unsealed phase 5 preregistration has identity")
	}
	return nil
}

func decodePhase5Strict(raw []byte, destination any) error {
	if len(raw) == 0 || len(raw) > maxPreregistrationBytes {
		return errors.New("invalid phase 5 JSON size")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("trailing phase 5 JSON")
	}
	canonical, err := json.Marshal(destination)
	if err != nil || !bytes.Equal(raw, canonical) {
		return errors.New("phase 5 JSON must be canonical")
	}
	return nil
}

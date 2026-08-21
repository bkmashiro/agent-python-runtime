package numpyreuse

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
)

const (
	CaseMatrixSchemaVersion                 = "pysolate.numpy-result-reuse-case-matrix.v1"
	PreregistrationSchemaVersion            = "pysolate.numpy-result-reuse-preregistration.v1"
	StudyID                                 = "numpy-result-reuse-v1"
	ParentCommit                            = "48a17fd0ef7b9207408ea46bbac269d5f479f8cf"
	ParentP5RMechanismEvidenceSHA256        = "sha256:e7065002d5519e99cbac4c182bcc79c8abadadf4aa2e3e753ece8bd160b9a12a"
	ReferenceWheelsCommit                   = "184cce0b537088be76e1e8a06d6fe742e2f29ff4"
	NumPySourceCommit                       = "7bc18034031f32e5d03bb646c472dabd1623e9d5"
	NumPySourceArchiveSHA256                = "sha256:9a34aaef957033ff8a3a865e8f0172eb7de4cf4c2891195a56c13e915fb86014"
	CaseMatrixIdentity                      = "sha256:02172d79851a991b002bbfe3b5cda2c777eb92a0ecce877d6e360769be20cb5f"
	PreregistrationIdentity                 = "sha256:df6cb5376e25ace6084a40b3f323dc9843f18342bd56f5072f51b63c9b8f2c01"
	ShuffleSeed                      uint64 = 20260823
	TrialsPerTreatment               uint32 = 3
	MaxContractBytes                        = 1 << 20
)

var (
	platforms  = []string{"darwin_arm64", "linux_amd64"}
	profiles   = []string{"cold_end_to_end", "preprovisioned_numpy_ready_equivalent_capacity"}
	treatments = []string{"original_recompute", "prepared_ndarray_reuse"}
)

type Case struct {
	ID                  string `json:"id"`
	Class               string `json:"class"`
	ComputeClass        string `json:"compute_class"`
	PayloadClass        string `json:"payload_class"`
	Source              string `json:"source"`
	SourceSHA256        string `json:"source_sha256"`
	DType               string `json:"dtype"`
	ExpectedNBytes      uint64 `json:"expected_nbytes"`
	LeadGapMillis       uint32 `json:"lead_gap_millis"`
	Consumers           uint32 `json:"consumers"`
	ControlAction       string `json:"control_action"`
	ExpectedDisposition string `json:"expected_disposition"`
	EconomicsEligible   bool   `json:"economics_eligible"`
}

type CaseMatrix struct {
	SchemaVersion          string   `json:"schema_version"`
	StudyID                string   `json:"study_id"`
	ParentCommit           string   `json:"parent_commit"`
	ParentP5REvidence      string   `json:"parent_p5r_mechanism_evidence_sha256"`
	ReferenceWheelsCommit  string   `json:"reference_wheels_commit"`
	NumPySourceCommit      string   `json:"numpy_source_commit"`
	NumPySourceArchiveHash string   `json:"numpy_source_archive_sha256"`
	ShuffleSeed            uint64   `json:"shuffle_seed"`
	TrialsPerTreatment     uint32   `json:"trials_per_treatment"`
	Platforms              []string `json:"platforms"`
	Profiles               []string `json:"profiles"`
	Treatments             []string `json:"treatments"`
	Cases                  []Case   `json:"cases"`
	Identity               string   `json:"identity"`
}

type MechanismGate struct {
	RequireAllAdversarialControls    bool `json:"require_all_adversarial_controls"`
	RequireFreshProducerAndConsumers bool `json:"require_fresh_producer_and_consumers"`
	RequireNoAuthorityExpansion      bool `json:"require_no_authority_expansion"`
	RequireNoReplay                  bool `json:"require_no_replay"`
	RequireExactResultParity         bool `json:"require_exact_result_parity"`
	RequireSingleUseLeases           bool `json:"require_single_use_leases"`
	RequireTerminalBlobDisposition   bool `json:"require_terminal_blob_disposition"`
}

type Preregistration struct {
	SchemaVersion                     string              `json:"schema_version"`
	StudyID                           string              `json:"study_id"`
	ParentCommit                      string              `json:"parent_commit"`
	ParentP5RMechanismEvidenceSHA256  string              `json:"parent_p5r_mechanism_evidence_sha256"`
	CaseMatrixIdentity                string              `json:"case_matrix_identity"`
	ClockPolicy                       string              `json:"clock_policy"`
	Profiles                          []string            `json:"profiles"`
	Treatments                        []string            `json:"treatments"`
	StageMetrics                      []string            `json:"stage_metrics"`
	TrialRecordRequirements           []string            `json:"trial_record_requirements"`
	MechanismGate                     MechanismGate       `json:"mechanism_gate"`
	RequireUniversalPositiveEconomics bool                `json:"require_universal_positive_economics"`
	EconomicsInterpretation           string              `json:"economics_interpretation"`
	EconomicSummaries                 []string            `json:"economic_summaries"`
	ClaimBoundary                     map[string][]string `json:"claim_boundary"`
	Identity                          string              `json:"identity"`
}

type CampaignCoordinate struct {
	Platform   string `json:"platform"`
	Profile    string `json:"profile"`
	CaseID     string `json:"case_id"`
	Treatment  string `json:"treatment"`
	TrialIndex uint32 `json:"trial_index"`
}

func digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("sha256:%x", sum[:])
}

func economicCase(id, computeClass, payloadClass, source, dtype string, nbytes uint64, gap, consumers uint32) Case {
	return Case{ID: id, Class: "economics", ComputeClass: computeClass, PayloadClass: payloadClass, Source: source, SourceSHA256: digest([]byte(source)), DType: dtype, ExpectedNBytes: nbytes, LeadGapMillis: gap, Consumers: consumers, ControlAction: "none", ExpectedDisposition: "blob_consumed_by_all_leases", EconomicsEligible: true}
}

func controlCase(id, computeClass, payloadClass, source, dtype, action, disposition string) Case {
	return Case{ID: id, Class: "adversarial", ComputeClass: computeClass, PayloadClass: payloadClass, Source: source, SourceSHA256: digest([]byte(source)), DType: dtype, Consumers: 1, ControlAction: action, ExpectedDisposition: disposition}
}

func Cases() []Case {
	importSmall := "import numpy as np\nvalue = np.zeros((8192,), dtype=np.float64)\nresult = int(value.size)\n"
	elementSmall := "import numpy as np\nbase = np.arange(8192, dtype=np.float64)\nvalue = base * 1.5 + 2.0\nresult = float(value[-1])\n"
	elementMedium := "import numpy as np\nbase = np.arange(131072, dtype=np.float64)\nvalue = base * 1.5 + 2.0\nresult = float(value[-1])\n"
	elementLarge := "import numpy as np\nbase = np.arange(1048576, dtype=np.float64)\nvalue = base * 1.5 + 2.0\nresult = float(value[-1])\n"
	reduction := "import numpy as np\nbase = np.arange(1048576, dtype=np.int64)\nvalue = np.asarray([np.sum(base)], dtype=np.int64)\nresult = int(value[0])\n"
	matrix := "import numpy as np\nbase = np.arange(65536, dtype=np.float64).reshape((256, 256))\nvalue = base @ base.T\nresult = float(value[0, 0])\n"
	return []Case{
		economicCase("numpy_import_small_gap0_c1", "import_only", "small", importSmall, "float64", 65536, 0, 1),
		economicCase("numpy_elementwise_small_gap0_c1", "elementwise", "small", elementSmall, "float64", 65536, 0, 1),
		economicCase("numpy_elementwise_medium_gap10000_c1", "elementwise", "medium", elementMedium, "float64", 1048576, 10000, 1),
		economicCase("numpy_elementwise_large_gap45000_c1", "elementwise", "large", elementLarge, "float64", 8388608, 45000, 1),
		economicCase("numpy_reduction_small_gap0_c1", "reduction", "small", reduction, "int64", 8, 0, 1),
		economicCase("numpy_reduction_small_gap10000_c2", "reduction", "small", reduction, "int64", 8, 10000, 2),
		economicCase("numpy_matrix_medium_gap0_c1", "matrix", "medium", matrix, "float64", 524288, 0, 1),
		economicCase("numpy_matrix_medium_gap10000_c2", "matrix", "medium", matrix, "float64", 524288, 10000, 2),
		economicCase("numpy_matrix_medium_gap45000_c4", "matrix", "medium", matrix, "float64", 524288, 45000, 4),
		economicCase("numpy_elementwise_large_gap0_c4", "elementwise", "large", elementLarge, "float64", 8388608, 0, 4),
		controlCase("reject_object_dtype", "elementwise", "small", "import numpy as np\nvalue = np.asarray([object()], dtype=object)\nresult = 1\n", "object", "admit_producer", "producer_rejected_no_blob"),
		controlCase("reject_noncontiguous_view", "elementwise", "small", "import numpy as np\nbase = np.arange(64, dtype=np.float64).reshape((8, 8))\nvalue = base[:, ::2]\nresult = int(value.size)\n", "float64", "admit_producer", "producer_rejected_no_blob"),
		controlCase("reject_fortran_order", "elementwise", "small", "import numpy as np\nvalue = np.asfortranarray(np.arange(64, dtype=np.float64).reshape((8, 8)))\nresult = int(value.size)\n", "float64", "admit_producer", "producer_rejected_no_blob"),
		controlCase("reject_random_api", "elementwise", "small", "import numpy as np\nvalue = np.random.default_rng().random(8)\nresult = float(value[0])\n", "float64", "analyze_source", "analysis_rejected_no_producer"),
		controlCase("reject_source_drift", "elementwise", "small", elementSmall, "float64", "mutate_final_source", "selection_rejected_blob_discarded"),
		controlCase("reject_corrupt_descriptor", "elementwise", "small", elementSmall, "float64", "mutate_descriptor", "materialization_rejected_blob_discarded"),
		controlCase("reject_cross_profile", "elementwise", "small", elementSmall, "float64", "change_consumer_profile", "materialization_rejected_blob_discarded"),
		controlCase("consumer_mutation_is_private", "elementwise", "small", elementSmall, "float64", "mutate_first_consumer", "second_consumer_observes_canonical_bytes"),
	}
}

func NewCaseMatrix() CaseMatrix {
	return CaseMatrix{SchemaVersion: CaseMatrixSchemaVersion, StudyID: StudyID, ParentCommit: ParentCommit, ParentP5REvidence: ParentP5RMechanismEvidenceSHA256, ReferenceWheelsCommit: ReferenceWheelsCommit, NumPySourceCommit: NumPySourceCommit, NumPySourceArchiveHash: NumPySourceArchiveSHA256, ShuffleSeed: ShuffleSeed, TrialsPerTreatment: TrialsPerTreatment, Platforms: append([]string(nil), platforms...), Profiles: append([]string(nil), profiles...), Treatments: append([]string(nil), treatments...), Cases: Cases()}
}

func NewPreregistration(matrixIdentity string) Preregistration {
	return Preregistration{
		SchemaVersion: PreregistrationSchemaVersion, StudyID: StudyID, ParentCommit: ParentCommit, ParentP5RMechanismEvidenceSHA256: ParentP5RMechanismEvidenceSHA256, CaseMatrixIdentity: matrixIdentity, ClockPolicy: "study_relative_monotonic_nanos",
		Profiles: append([]string(nil), profiles...), Treatments: append([]string(nil), treatments...),
		StageMetrics:                      []string{"analysis_nanos", "producer_guest_provision_nanos", "producer_import_nanos", "producer_compute_nanos", "producer_encode_copy_nanos", "host_blob_store_nanos", "host_blob_bytes", "consumer_guest_provision_nanos", "consumer_import_nanos", "consumer_copy_materialize_nanos", "consumer_execution_nanos", "teardown_nanos", "critical_wall_nanos", "peak_resident_memory_bytes"},
		TrialRecordRequirements:           []string{"body_free_canonical_record", "exact_artifact_profile_pass_source_input_case_treatment_trial_identity", "separate_process_exit_and_protocol_status", "monotonic_stage_intervals", "physical_guest_and_runtime_init_counts", "blob_and_lease_terminal_disposition", "stage_intervals_may_overlap_and_must_not_be_naively_summed"},
		MechanismGate:                     MechanismGate{RequireAllAdversarialControls: true, RequireFreshProducerAndConsumers: true, RequireNoAuthorityExpansion: true, RequireNoReplay: true, RequireExactResultParity: true, RequireSingleUseLeases: true, RequireTerminalBlobDisposition: true},
		RequireUniversalPositiveEconomics: false,
		EconomicsInterpretation:           "mixed_or_negative_cells_are_valid_results_and_do_not_fail_mechanism_closure",
		EconomicSummaries:                 []string{"median_and_mad_per_cell", "net_saved_nanos", "speedup_ratio", "break_even_compute_nanos", "break_even_consumer_count", "break_even_lead_gap_nanos", "bytes_copied_per_saved_compute_nano"},
		ClaimBoundary:                     map[string][]string{"supported": {"numpy_core_profile_artifact", "bounded_numeric_c_contiguous_ndarray_transport", "run_scoped_fresh_guest_reuse", "measured_break_even_surface"}, "excluded": {"pandas", "scipy", "object_dtype", "pickle", "arbitrary_python_heap_transfer", "pointer_or_linear_memory_sharing", "runtime_package_installation", "generic_plugin_framework", "universal_positive_economics"}},
	}
}

func CampaignCoordinates() []CampaignCoordinate {
	coordinates := make([]CampaignCoordinate, 0, 240)
	for _, platform := range platforms {
		for _, profile := range profiles {
			for _, candidate := range Cases() {
				if !candidate.EconomicsEligible {
					continue
				}
				for _, treatment := range treatments {
					for trial := uint32(1); trial <= TrialsPerTreatment; trial++ {
						coordinates = append(coordinates, CampaignCoordinate{Platform: platform, Profile: profile, CaseID: candidate.ID, Treatment: treatment, TrialIndex: trial})
					}
				}
			}
		}
	}
	random := rand.New(rand.NewSource(int64(ShuffleSeed)))
	random.Shuffle(len(coordinates), func(i, j int) { coordinates[i], coordinates[j] = coordinates[j], coordinates[i] })
	return coordinates
}

func SealCaseMatrix(value CaseMatrix) (CaseMatrix, error) {
	value.Identity = ""
	if err := validateCaseMatrix(value, false); err != nil {
		return CaseMatrix{}, err
	}
	raw, _ := json.Marshal(value)
	value.Identity = digest(raw)
	if value.Identity != CaseMatrixIdentity {
		return CaseMatrix{}, fmt.Errorf("case matrix drift: got %s", value.Identity)
	}
	return value, nil
}

func SealPreregistration(value Preregistration) (Preregistration, error) {
	value.Identity = ""
	if err := validatePreregistration(value, false); err != nil {
		return Preregistration{}, err
	}
	raw, _ := json.Marshal(value)
	value.Identity = digest(raw)
	if value.Identity != PreregistrationIdentity {
		return Preregistration{}, fmt.Errorf("preregistration drift: got %s", value.Identity)
	}
	return value, nil
}

func EncodeCaseMatrix(value CaseMatrix) ([]byte, error) {
	if err := validateCaseMatrix(value, true); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}
func EncodePreregistration(value Preregistration) ([]byte, error) {
	if err := validatePreregistration(value, true); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}
func DecodeCaseMatrix(raw []byte) (CaseMatrix, error) {
	var value CaseMatrix
	if err := strictDecode(raw, &value); err != nil || validateCaseMatrix(value, true) != nil {
		return CaseMatrix{}, errors.New("invalid case matrix")
	}
	return value, nil
}
func DecodePreregistration(raw []byte) (Preregistration, error) {
	var value Preregistration
	if err := strictDecode(raw, &value); err != nil || validatePreregistration(value, true) != nil {
		return Preregistration{}, errors.New("invalid preregistration")
	}
	return value, nil
}
func validateCaseMatrix(value CaseMatrix, sealed bool) error {
	expected := NewCaseMatrix()
	expected.Identity = value.Identity
	left, _ := json.Marshal(value)
	right, _ := json.Marshal(expected)
	if !bytes.Equal(left, right) {
		return errors.New("case matrix drift")
	}
	if sealed && value.Identity != CaseMatrixIdentity {
		return errors.New("case matrix identity drift")
	}
	if !sealed && value.Identity != "" {
		return errors.New("unsealed matrix has identity")
	}
	return nil
}
func validatePreregistration(value Preregistration, sealed bool) error {
	expected := NewPreregistration(CaseMatrixIdentity)
	expected.Identity = value.Identity
	left, _ := json.Marshal(value)
	right, _ := json.Marshal(expected)
	if !bytes.Equal(left, right) || value.CaseMatrixIdentity != CaseMatrixIdentity {
		return errors.New("preregistration drift")
	}
	if sealed && value.Identity != PreregistrationIdentity {
		return errors.New("preregistration identity drift")
	}
	if !sealed && value.Identity != "" {
		return errors.New("unsealed preregistration has identity")
	}
	return nil
}
func strictDecode(raw []byte, destination any) error {
	if len(raw) == 0 || len(raw) > MaxContractBytes {
		return errors.New("invalid JSON size")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON")
	}
	canonical, err := json.Marshal(destination)
	if err != nil || !bytes.Equal(raw, canonical) {
		return errors.New("JSON must be canonical")
	}
	return nil
}

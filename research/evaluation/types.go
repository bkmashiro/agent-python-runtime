package evaluation

import "errors"

const (
	CorpusSchemaVersion = "pysolate.workload-corpus.v1"
	PlanSchemaVersion   = "pysolate.evaluation-plan.v1"
	ReportSchemaVersion = "pysolate.evaluation-report.v1"
)

var ErrInvalid = errors.New("invalid evaluation contract")

type EvidenceClass string

const (
	EvidenceCurrent             EvidenceClass = "current"
	EvidenceMechanismOnly       EvidenceClass = "mechanism_only"
	EvidenceQualifiedWorkload   EvidenceClass = "qualified_workload"
	EvidenceExperimentalPartial EvidenceClass = "experimental_partial"
	EvidenceNotMeasured         EvidenceClass = "not_measured"
)

type Family string

const (
	FamilyStructuredSource Family = "structured_source_synthesis"
	FamilyStatefulLocal    Family = "stateful_local_analysis"
	FamilyBoundedPlanning  Family = "bounded_planning_search"
)

type Treatment string

const (
	TreatmentLiveCapture          Treatment = "live_capture"
	TreatmentOfflineReplay        Treatment = "offline_replay"
	TreatmentCounterfactualBranch Treatment = "counterfactual_branch"
	TreatmentDeterministicVerify  Treatment = "deterministic_verification"
)

type OracleKind string

const (
	OracleResultOnly         OracleKind = "result_only"
	OracleResultAndWorkspace OracleKind = "result_and_workspace"
)

type Oracle struct {
	Kind                    OracleKind `json:"kind"`
	ExpectedResultSHA256    string     `json:"expected_result_sha256"`
	ExpectedWorkspaceSHA256 string     `json:"expected_workspace_sha256,omitempty"`
	ExpectedCapabilityCalls uint32     `json:"expected_capability_calls"`
}

type EffectClass string

const (
	EffectExternalRead EffectClass = "external_read"
)

type PlaybackPolicy string

const (
	PlaybackCaptured PlaybackPolicy = "captured"
)

type CapabilityRequirement struct {
	Name        string         `json:"name"`
	EffectClass EffectClass    `json:"effect_class"`
	Playback    PlaybackPolicy `json:"playback"`
}

type Workload struct {
	ID                   string                  `json:"id"`
	Version              uint32                  `json:"version"`
	Family               Family                  `json:"family"`
	CodeSHA256           string                  `json:"code_sha256"`
	InputSHA256          string                  `json:"input_sha256"`
	WorkspaceSeedSHA256  string                  `json:"workspace_seed_sha256,omitempty"`
	RequiredCapabilities []CapabilityRequirement `json:"required_capabilities"`
	Treatments           []Treatment             `json:"treatments"`
	Oracle               Oracle                  `json:"oracle"`
}

type Corpus struct {
	SchemaVersion string        `json:"schema_version"`
	EvidenceClass EvidenceClass `json:"evidence_class"`
	Workloads     []Workload    `json:"workloads"`
}

type Ceilings struct {
	MaxRows                uint32 `json:"max_rows"`
	MaxWallMillisPerRow    uint64 `json:"max_wall_millis_per_row"`
	MaxEvidenceBytesPerRow uint64 `json:"max_evidence_bytes_per_row"`
}

type Plan struct {
	SchemaVersion        string        `json:"schema_version"`
	EvidenceClass        EvidenceClass `json:"evidence_class"`
	HostCommit           string        `json:"host_commit"`
	GuestArtifactSHA256  string        `json:"guest_artifact_sha256"`
	GuestManifestSHA256  string        `json:"guest_manifest_sha256"`
	CorpusSHA256         string        `json:"corpus_sha256"`
	RuntimeProfileSHA256 string        `json:"runtime_profile_sha256"`
	TreatmentOrder       []Treatment   `json:"treatment_order"`
	Repetitions          uint32        `json:"repetitions"`
	Ceilings             Ceilings      `json:"ceilings"`
	ProhibitedClaims     []string      `json:"prohibited_claims"`
}

type RowStatus string

const (
	RowCompleted   RowStatus = "completed"
	RowFailed      RowStatus = "failed"
	RowTimedOut    RowStatus = "timed_out"
	RowUnsupported RowStatus = "unsupported"
)

type OracleStatus string

const (
	OraclePassed OracleStatus = "passed"
	OracleFailed OracleStatus = "failed"
	OracleNotRun OracleStatus = "not_run"
)

type Row struct {
	RowID            string       `json:"row_id"`
	WorkloadID       string       `json:"workload_id"`
	Treatment        Treatment    `json:"treatment"`
	Repetition       uint32       `json:"repetition"`
	Status           RowStatus    `json:"status"`
	OracleStatus     OracleStatus `json:"oracle_status"`
	EvidenceComplete bool         `json:"evidence_complete"`
	CorpusSHA256     string       `json:"corpus_sha256"`
	PlanSHA256       string       `json:"plan_sha256"`
	EvidenceRefs     []string     `json:"evidence_refs"`
	ProblemCode      string       `json:"problem_code,omitempty"`
}

type Summary struct {
	Offered     uint32 `json:"offered"`
	Completed   uint32 `json:"completed"`
	Failed      uint32 `json:"failed"`
	TimedOut    uint32 `json:"timed_out"`
	Unsupported uint32 `json:"unsupported"`
}

type Report struct {
	SchemaVersion    string        `json:"schema_version"`
	EvidenceClass    EvidenceClass `json:"evidence_class"`
	CorpusSHA256     string        `json:"corpus_sha256"`
	PlanSHA256       string        `json:"plan_sha256"`
	ProhibitedClaims []string      `json:"prohibited_claims"`
	Summary          Summary       `json:"summary"`
	Rows             []Row         `json:"rows"`
}

type Fixture struct {
	Bytes    []byte
	SHA256   string
	Contract Contract
}

type Contract string

const (
	ContractCorpus Contract = "corpus"
	ContractPlan   Contract = "plan"
	ContractReport Contract = "report"
)

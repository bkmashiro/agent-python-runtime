package evaluationv2

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"

	"github.com/bkmashiro/agent-python-runtime/research/evaluation"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
)

const (
	CorpusSchemaVersion         = "pysolate.workload-corpus.v2"
	PlanSchemaVersion           = "pysolate.evaluation-plan.v2"
	ExpandedCorpusSchemaVersion = "pysolate.workload-corpus.v2.1"
	ExpandedPlanSchemaVersion   = "pysolate.evaluation-plan.v2.1"
	EvidenceClass               = "mechanism_only"
	maxContractBytes            = 4 << 20
	maxRows                     = 32
)

var (
	ErrInvalid        = errors.New("invalid evaluation v2 contract")
	identifierPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
	digestPattern     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	commitPattern     = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

type Condition string

const (
	ConditionDirect Condition = "direct_broker"
	ConditionGuest  Condition = "pysolate_guest"
)

type Workload struct {
	ID                      string   `json:"id"`
	Version                 uint32   `json:"version"`
	CodeSHA256              string   `json:"code_sha256"`
	InputSHA256             string   `json:"input_sha256"`
	RequiredCapabilities    []string `json:"required_capabilities"`
	SourceFixtureSHA256     []string `json:"source_fixture_sha256"`
	ExpectedResultSHA256    string   `json:"expected_result_sha256"`
	ExpectedCapabilityCalls uint32   `json:"expected_capability_calls"`
}

type Corpus struct {
	SchemaVersion string     `json:"schema_version"`
	EvidenceClass string     `json:"evidence_class"`
	Workloads     []Workload `json:"workloads"`
}

type Ceilings struct {
	MaxRows               uint32 `json:"max_rows"`
	MaxWallMillisPerRow   uint64 `json:"max_wall_millis_per_row"`
	MaxSerializedBytesRow uint64 `json:"max_serialized_bytes_per_row"`
}

type Plan struct {
	SchemaVersion        string      `json:"schema_version"`
	EvidenceClass        string      `json:"evidence_class"`
	HostCommit           string      `json:"host_commit"`
	GuestArtifactSHA256  string      `json:"guest_artifact_sha256"`
	GuestManifestSHA256  string      `json:"guest_manifest_sha256"`
	RuntimeProfileSHA256 string      `json:"runtime_profile_sha256"`
	CorpusSHA256         string      `json:"corpus_sha256"`
	Conditions           []Condition `json:"conditions"`
	Repetitions          uint32      `json:"repetitions"`
	Ceilings             Ceilings    `json:"ceilings"`
	ProhibitedClaims     []string    `json:"prohibited_claims"`
}

type PlannedRow struct {
	RowID      string
	WorkloadID string
	Condition  Condition
	Repetition uint32
}

func PilotPlan(hostCommit, artifactSHA256, manifestSHA256, profileSHA256, corpusSHA256 string) Plan {
	return Plan{
		SchemaVersion: PlanSchemaVersion, EvidenceClass: EvidenceClass,
		HostCommit: hostCommit, GuestArtifactSHA256: artifactSHA256, GuestManifestSHA256: manifestSHA256,
		RuntimeProfileSHA256: profileSHA256, CorpusSHA256: corpusSHA256,
		Conditions: []Condition{ConditionDirect, ConditionGuest}, Repetitions: 1,
		Ceilings:         Ceilings{MaxRows: 4, MaxWallMillisPerRow: 60000, MaxSerializedBytesRow: 1 << 20},
		ProhibitedClaims: evaluation.RequiredProhibitedClaims(),
	}
}

func ExpandedPlan(hostCommit, artifactSHA256, manifestSHA256, profileSHA256, corpusSHA256 string) Plan {
	plan := PilotPlan(hostCommit, artifactSHA256, manifestSHA256, profileSHA256, corpusSHA256)
	plan.SchemaVersion = ExpandedPlanSchemaVersion
	plan.Ceilings.MaxRows = 10
	return plan
}

func RuntimeProfileSHA256(config runtimeconfig.RunConfig) (string, error) {
	if err := config.Validate(); err != nil || config.ExecutionProfile != nil || config.DeterministicVerification != nil || len(config.CapabilityGrants) != 0 {
		return "", ErrInvalid
	}
	encoded, err := json.Marshal(struct {
		Domain           string `json:"domain"`
		TimeoutMillis    int64  `json:"timeout_millis"`
		MaxRequestBytes  uint32 `json:"max_request_bytes"`
		MaxResponseBytes uint32 `json:"max_response_bytes"`
		MemoryLimitPages uint32 `json:"memory_limit_pages"`
	}{"pysolate.evaluation-runtime-profile.v2", config.Timeout.Milliseconds(), config.MaxRequestBytes, config.MaxResponseBytes, config.MemoryLimitPages})
	if err != nil {
		return "", ErrInvalid
	}
	d := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", d), nil
}

func RowIdentity(workloadID string, condition Condition, repetition uint32) string {
	return fmt.Sprintf("%s:%s:%d", workloadID, condition, repetition)
}

func ExpandRows(corpus Corpus, plan Plan) ([]PlannedRow, error) {
	if validateCorpus(corpus) != nil || validatePlan(plan) != nil || corpus.SchemaVersion == CorpusSchemaVersion && plan.SchemaVersion != PlanSchemaVersion || corpus.SchemaVersion == ExpandedCorpusSchemaVersion && plan.SchemaVersion != ExpandedPlanSchemaVersion {
		return nil, ErrInvalid
	}
	_, corpusID, err := EncodeCorpus(corpus)
	if err != nil || corpusID != plan.CorpusSHA256 {
		return nil, ErrInvalid
	}
	count := uint64(len(corpus.Workloads)) * uint64(len(plan.Conditions)) * uint64(plan.Repetitions)
	if count == 0 || count > uint64(plan.Ceilings.MaxRows) || count > maxRows {
		return nil, ErrInvalid
	}
	rows := make([]PlannedRow, 0, count)
	for _, workload := range corpus.Workloads {
		for _, condition := range plan.Conditions {
			for repetition := uint32(0); repetition < plan.Repetitions; repetition++ {
				rows = append(rows, PlannedRow{RowID: RowIdentity(workload.ID, condition, repetition), WorkloadID: workload.ID, Condition: condition, Repetition: repetition})
			}
		}
	}
	return rows, nil
}

func EncodeCorpus(value Corpus) ([]byte, string, error) {
	return encodeCanonical(value, validateCorpus)
}
func DecodeCorpus(data []byte) (Corpus, string, error) { return decodeStrict(data, validateCorpus) }
func EncodePlan(value Plan) ([]byte, string, error)    { return encodeCanonical(value, validatePlan) }
func DecodePlan(data []byte) (Plan, string, error)     { return decodeStrict(data, validatePlan) }

func validateCorpus(corpus Corpus) error {
	expectedDefinitions, err := definitionsForCorpusSchema(corpus.SchemaVersion)
	if err != nil || corpus.EvidenceClass != EvidenceClass || len(corpus.Workloads) != len(expectedDefinitions) {
		return ErrInvalid
	}
	seen := map[string]struct{}{}
	for _, workload := range corpus.Workloads {
		if !identifierPattern.MatchString(workload.ID) || workload.Version != 1 || !digestPattern.MatchString(workload.CodeSHA256) || !digestPattern.MatchString(workload.InputSHA256) || !digestPattern.MatchString(workload.ExpectedResultSHA256) || workload.ExpectedCapabilityCalls == 0 || workload.ExpectedCapabilityCalls > 2 || len(workload.RequiredCapabilities) != int(workload.ExpectedCapabilityCalls) || len(workload.SourceFixtureSHA256) != int(workload.ExpectedCapabilityCalls) {
			return ErrInvalid
		}
		for _, sourceID := range workload.SourceFixtureSHA256 {
			if !digestPattern.MatchString(sourceID) {
				return ErrInvalid
			}
		}
		if _, exists := seen[workload.ID]; exists {
			return ErrInvalid
		}
		seen[workload.ID] = struct{}{}
		for _, name := range workload.RequiredCapabilities {
			if name != "sources.demo_catalog" && name != "sources.benchmark_manifest" {
				return ErrInvalid
			}
		}
	}
	for i := range expectedDefinitions {
		if !reflect.DeepEqual(corpus.Workloads[i], expectedDefinitions[i].Workload) {
			return ErrInvalid
		}
	}
	return nil
}

func validatePlan(plan Plan) error {
	claims := evaluation.RequiredProhibitedClaims()
	wantRows := uint32(4)
	if plan.SchemaVersion == ExpandedPlanSchemaVersion {
		wantRows = 10
	} else if plan.SchemaVersion != PlanSchemaVersion {
		return ErrInvalid
	}
	if plan.EvidenceClass != EvidenceClass || !commitPattern.MatchString(plan.HostCommit) || !digestPattern.MatchString(plan.GuestArtifactSHA256) || !digestPattern.MatchString(plan.GuestManifestSHA256) || !digestPattern.MatchString(plan.RuntimeProfileSHA256) || !digestPattern.MatchString(plan.CorpusSHA256) || len(plan.Conditions) != 2 || plan.Conditions[0] != ConditionDirect || plan.Conditions[1] != ConditionGuest || plan.Repetitions != 1 || plan.Ceilings.MaxRows != wantRows || plan.Ceilings.MaxWallMillisPerRow != 60000 || plan.Ceilings.MaxSerializedBytesRow != 1<<20 || len(plan.ProhibitedClaims) != len(claims) {
		return ErrInvalid
	}
	for i := range claims {
		if plan.ProhibitedClaims[i] != claims[i] {
			return ErrInvalid
		}
	}
	return nil
}

func encodeCanonical[T any](value T, validate func(T) error) ([]byte, string, error) {
	if validate(value) != nil {
		return nil, "", ErrInvalid
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, "", ErrInvalid
	}
	d := sha256.Sum256(encoded)
	return encoded, fmt.Sprintf("sha256:%x", d), nil
}

func decodeStrict[T any](data []byte, validate func(T) error) (T, string, error) {
	var zero T
	if len(data) == 0 || len(data) > maxContractBytes {
		return zero, "", ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var value T
	if decoder.Decode(&value) != nil {
		return zero, "", ErrInvalid
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) || validate(value) != nil {
		return zero, "", ErrInvalid
	}
	canonical, identity, err := encodeCanonical(value, validate)
	if err != nil || !bytes.Equal(data, canonical) {
		return zero, "", ErrInvalid
	}
	return value, identity, nil
}

func digest(value byte) string { return "sha256:" + string(bytes.Repeat([]byte{value}, 64)) }

package prepareddataset

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"unicode/utf8"

	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

const (
	ContractSchemaVersion = "pysolate.prepared-data-contract.v1"

	SourcePolicyImmutableWorkspaceRoot = "immutable_workspace_root"
	LoaderNumpyNPYV1                   = "numpy_npy_v1"
	CodecNumpyNDArrayCV1               = "numpy_ndarray_c_v1"

	PreparedCapability        = "sources.read"
	PreparedCall              = "numpy.load"
	PreparedResourceNamespace = "workspace"
	PreparedResourcePath      = "/workspace/input.npy"
	PreparedDType             = "<i8"
	PreparedOrder             = "C"
	PreparedEndianness        = "little"
	PreparedHeaderBytes       = uint64(128)
	PreparedBodyBytes         = uint64(8 << 20)
	PreparedFileBytes         = uint64((8 << 20) + 128)
	PreparedMaxResultBytes    = uint64(4 << 10)
	PreparedElementBytes      = uint32(8)
	PreparedCostUnits         = uint32(1)
)

var (
	ErrInvalidContract      = errors.New("invalid prepared-data contract")
	ErrHostContractRequired = errors.New("prepared-data authority must be Host-authored")
	digestPattern           = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	identityPattern         = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
)

// HostPreparedDataDeclaration is the positive, Host-authored v1 declaration.
// It is intentionally separate from NumpyLoadFacts: syntax facts cannot be
// converted into this declaration or any authority-bearing decision.
type HostPreparedDataDeclaration struct {
	SchemaVersion           string              `json:"schema_version"`
	Capability              string              `json:"capability"`
	Call                    string              `json:"call"`
	CapabilityPlanSHA256    string              `json:"capability_plan_sha256"`
	StreamEpoch             string              `json:"stream_epoch"`
	AdmittedPrefixSHA256    string              `json:"admitted_prefix_sha256"`
	Span                    semantic.SourceSpan `json:"span"`
	CanonicalArguments      json.RawMessage     `json:"canonical_arguments"`
	DynamicOccurrence       uint32              `json:"dynamic_occurrence"`
	ResourceNamespace       string              `json:"resource_namespace"`
	ResourcePath            string              `json:"resource_path"`
	SourcePolicy            string              `json:"source_policy"`
	WorkspaceRootSHA256     string              `json:"workspace_root_sha256"`
	FileSHA256              string              `json:"file_sha256"`
	BodySHA256              string              `json:"body_sha256"`
	Freshness               string              `json:"freshness"`
	Unclaimed               string              `json:"unclaimed"`
	LoaderKind              string              `json:"loader_kind"`
	AllowPickle             bool                `json:"allow_pickle"`
	MMapMode                string              `json:"mmap_mode"`
	DType                   string              `json:"dtype"`
	Shape                   []uint64            `json:"shape"`
	Order                   string              `json:"order"`
	Endianness              string              `json:"endianness"`
	HeaderBytes             uint64              `json:"header_bytes"`
	ElementBytes            uint32              `json:"element_bytes"`
	CodecKind               string              `json:"codec_kind"`
	ArtifactSHA256          string              `json:"artifact_sha256"`
	ExecutionProfileSHA256  string              `json:"execution_profile_sha256"`
	ImportClosureSHA256     string              `json:"import_closure_sha256"`
	RunIdentity             string              `json:"run_identity"`
	PrivacyPartition        string              `json:"privacy_partition"`
	BudgetReservationSHA256 string              `json:"budget_reservation_sha256"`
	MaxFileBytes            uint64              `json:"max_file_bytes"`
	MaxBodyBytes            uint64              `json:"max_body_bytes"`
	MaxResultBytes          uint64              `json:"max_result_bytes"`
	CostUnits               uint32              `json:"cost_units"`
}

// PreparedDataContract is a sealed copy of a Host declaration. Its validated
// identity is the only contract material accepted by Decide.
type PreparedDataContract struct {
	declaration HostPreparedDataDeclaration
	identity    string
}

func NewPreparedDataContract(declaration HostPreparedDataDeclaration) (PreparedDataContract, error) {
	declaration = cloneDeclaration(declaration)
	if err := declaration.validate(); err != nil {
		return PreparedDataContract{}, err
	}
	encoded, err := json.Marshal(declaration)
	if err != nil {
		return PreparedDataContract{}, ErrInvalidContract
	}
	return PreparedDataContract{declaration: declaration, identity: digestBytes(encoded)}, nil
}

// NewPreparedDataContractFromPythonMetadata is deliberately not an adapter.
// Metadata produced by Python, tools, or the Guest has no authority to mint a
// Host contract; only NewPreparedDataContract accepts a Host declaration.
func NewPreparedDataContractFromPythonMetadata(any) (PreparedDataContract, error) {
	return PreparedDataContract{}, ErrHostContractRequired
}

// DecodePreparedDataContract decodes a Host-supplied document with a closed
// schema. Unknown, omitted, malformed, or trailing fields fail closed.
func DecodePreparedDataContract(raw []byte) (PreparedDataContract, error) {
	if len(raw) == 0 || !utf8.Valid(raw) {
		return PreparedDataContract{}, ErrInvalidContract
	}
	var document map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&document); err != nil {
		return PreparedDataContract{}, ErrInvalidContract
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return PreparedDataContract{}, ErrInvalidContract
	}
	for _, field := range requiredContractFields {
		if _, ok := document[field]; !ok {
			return PreparedDataContract{}, ErrInvalidContract
		}
	}
	decoder = json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var declaration HostPreparedDataDeclaration
	if err := decoder.Decode(&declaration); err != nil {
		return PreparedDataContract{}, ErrInvalidContract
	}
	return NewPreparedDataContract(declaration)
}

func (contract PreparedDataContract) Identity() string { return contract.identity }
func (contract PreparedDataContract) Valid() bool {
	return contract.identity != "" && contract.declaration.validate() == nil
}

func (contract PreparedDataContract) Declaration() HostPreparedDataDeclaration {
	return cloneDeclaration(contract.declaration)
}

func (contract PreparedDataContract) String() string {
	if contract.identity == "" {
		return ""
	}
	return fmt.Sprintf("PreparedDataContract{%s}", contract.identity)
}

func (declaration HostPreparedDataDeclaration) validate() error {
	if declaration.SchemaVersion != ContractSchemaVersion || declaration.Capability != PreparedCapability || declaration.Call != PreparedCall ||
		!validDigest(declaration.CapabilityPlanSHA256) || !validIdentity(declaration.StreamEpoch) ||
		!validDigest(declaration.AdmittedPrefixSHA256) || !validSpan(declaration.Span) || declaration.DynamicOccurrence != 1 ||
		declaration.ResourceNamespace != PreparedResourceNamespace || declaration.ResourcePath != PreparedResourcePath ||
		declaration.SourcePolicy != SourcePolicyImmutableWorkspaceRoot || !validDigest(declaration.WorkspaceRootSHA256) ||
		!validDigest(declaration.FileSHA256) || !validDigest(declaration.BodySHA256) ||
		declaration.Freshness != "plan_epoch" || declaration.Unclaimed != "discard_with_disposition" ||
		declaration.LoaderKind != LoaderNumpyNPYV1 || declaration.AllowPickle || declaration.MMapMode != "" ||
		declaration.DType != PreparedDType || !equalShape(declaration.Shape, []uint64{1024, 1024}) ||
		declaration.Order != PreparedOrder || declaration.Endianness != PreparedEndianness ||
		declaration.HeaderBytes != PreparedHeaderBytes || declaration.ElementBytes != PreparedElementBytes ||
		declaration.CodecKind != CodecNumpyNDArrayCV1 || !validDigest(declaration.ArtifactSHA256) ||
		!validDigest(declaration.ExecutionProfileSHA256) || !validDigest(declaration.ImportClosureSHA256) ||
		!validIdentity(declaration.RunIdentity) || !validIdentity(declaration.PrivacyPartition) ||
		!validDigest(declaration.BudgetReservationSHA256) || declaration.MaxFileBytes != PreparedFileBytes ||
		declaration.MaxBodyBytes != PreparedBodyBytes || declaration.MaxResultBytes != PreparedMaxResultBytes || declaration.CostUnits != PreparedCostUnits {
		return ErrInvalidContract
	}
	canonical, err := canonicalArguments(declaration.ResourcePath)
	if err != nil || !bytes.Equal(canonical, declaration.CanonicalArguments) {
		return ErrInvalidContract
	}
	return nil
}

func cloneDeclaration(value HostPreparedDataDeclaration) HostPreparedDataDeclaration {
	value.CanonicalArguments = append(json.RawMessage(nil), value.CanonicalArguments...)
	value.Shape = append([]uint64(nil), value.Shape...)
	return value
}

func validDigest(value string) bool   { return digestPattern.MatchString(value) }
func validIdentity(value string) bool { return identityPattern.MatchString(value) }

func validSpan(span semantic.SourceSpan) bool {
	return span.StartLine > 0 && span.EndLine >= span.StartLine &&
		(span.EndLine != span.StartLine || span.EndColumn >= span.StartColumn)
}

func equalShape(left, right []uint64) bool {
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

func canonicalArguments(path string) ([]byte, error) {
	return json.Marshal(struct {
		AllowPickle bool   `json:"allow_pickle"`
		Path        string `json:"path"`
	}{AllowPickle: false, Path: path})
}

func digestBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func digestText(value string) string { return digestBytes([]byte(value)) }

var requiredContractFields = []string{
	"schema_version", "capability", "call", "capability_plan_sha256", "stream_epoch", "admitted_prefix_sha256", "span",
	"canonical_arguments", "dynamic_occurrence", "resource_namespace", "resource_path", "source_policy",
	"workspace_root_sha256", "file_sha256", "body_sha256", "freshness", "unclaimed", "loader_kind", "allow_pickle",
	"mmap_mode", "dtype", "shape", "order", "endianness", "header_bytes", "element_bytes", "codec_kind",
	"artifact_sha256", "execution_profile_sha256", "import_closure_sha256", "run_identity", "privacy_partition",
	"budget_reservation_sha256", "max_file_bytes", "max_body_bytes", "max_result_bytes", "cost_units",
}

func canonicalArgumentsMatch(value json.RawMessage, path string) bool {
	canonical, err := canonicalArguments(path)
	return err == nil && bytes.Equal(canonical, value)
}

func hasPrefixDigest(source, digest string) bool { return digestText(source) == digest }

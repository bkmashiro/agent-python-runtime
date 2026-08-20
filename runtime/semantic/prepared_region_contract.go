package semantic

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strconv"
)

const (
	PreparedRegionDecisionSchemaVersion = "pysolate.prepared-region-decision.v1"
	PreparedRegionCapsuleSchemaVersion  = "pysolate.prepared-region-capsule.v1"
	PreparedRegionPatchSchemaVersion    = "pysolate.prepared-region-execution-patch.v1"
	PreparedRegionConsumer              = "prepared_pure_region"
	PreparedRegionPassSchemaVersion     = "pysolate.prepared-pure-region-pass.v1"
	PreparedRegionCodecJSONScalarV1     = "canonical_json_bool_or_int64.v1"
	PreparedRegionHelperBinding         = "__pysolate_materialize_value__"
	PreparedRegionMaxPayloadBytes       = 256
)

var (
	ErrInvalidPreparedRegion = errors.New("invalid prepared region contract")
	pythonIdentifierPattern  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	canonicalIntegerPattern  = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)
)

type PreparedRegionBinding struct {
	SourceSHA256           string     `json:"source_sha256"`
	ASTSHA256              string     `json:"ast_sha256"`
	AnalysisSHA256         string     `json:"analysis_sha256"`
	RegionID               string     `json:"region_id"`
	RegionSpan             SourceSpan `json:"region_span"`
	RegionSourceSHA256     string     `json:"region_source_sha256"`
	LiveInsSHA256          string     `json:"live_ins_sha256"`
	EnvironmentSHA256      string     `json:"environment_sha256"`
	ExecutionProfileSHA256 string     `json:"execution_profile_sha256"`
	ImportClosureSHA256    string     `json:"import_closure_sha256"`
	CapabilityPlanSHA256   string     `json:"capability_plan_sha256"`
	PassConfigSHA256       string     `json:"pass_config_sha256"`
	Codec                  string     `json:"codec"`
	OutputName             string     `json:"output_name"`
}

type PreparedRegionDecision struct {
	SchemaVersion   string `json:"schema_version"`
	Consumer        string `json:"consumer"`
	PassSchema      string `json:"pass_schema"`
	MaxPayloadBytes uint32 `json:"max_payload_bytes"`
	PreparedRegionBinding
	IdentitySHA256 string `json:"identity_sha256"`
}

type preparedRegionDecisionIdentity struct {
	SchemaVersion   string `json:"schema_version"`
	Consumer        string `json:"consumer"`
	PassSchema      string `json:"pass_schema"`
	MaxPayloadBytes uint32 `json:"max_payload_bytes"`
	PreparedRegionBinding
}

func SealPreparedRegionDecision(binding PreparedRegionBinding) ([]byte, PreparedRegionDecision, error) {
	if !isValidPreparedRegionBinding(binding) {
		return nil, PreparedRegionDecision{}, ErrInvalidPreparedRegion
	}
	identity := preparedRegionDecisionIdentity{SchemaVersion: PreparedRegionDecisionSchemaVersion, Consumer: PreparedRegionConsumer, PassSchema: PreparedRegionPassSchemaVersion, MaxPayloadBytes: PreparedRegionMaxPayloadBytes, PreparedRegionBinding: binding}
	decision := PreparedRegionDecision{SchemaVersion: identity.SchemaVersion, Consumer: identity.Consumer, PassSchema: identity.PassSchema, MaxPayloadBytes: identity.MaxPayloadBytes, PreparedRegionBinding: binding, IdentitySHA256: preparedRegionDigest(identity)}
	raw, err := preparedRegionCanonicalJSON(decision)
	return raw, decision, err
}

func DecodePreparedRegionDecision(raw []byte) (PreparedRegionDecision, error) {
	var value PreparedRegionDecision
	if preparedRegionDecode(raw, &value) != nil || !value.valid() {
		return PreparedRegionDecision{}, ErrInvalidPreparedRegion
	}
	return value, nil
}

func (decision PreparedRegionDecision) ValidateBinding(binding PreparedRegionBinding) error {
	if !decision.valid() || decision.PreparedRegionBinding != binding {
		return ErrInvalidPreparedRegion
	}
	return nil
}

func (decision PreparedRegionDecision) valid() bool {
	if decision.SchemaVersion != PreparedRegionDecisionSchemaVersion || decision.Consumer != PreparedRegionConsumer || decision.PassSchema != PreparedRegionPassSchemaVersion || decision.MaxPayloadBytes != PreparedRegionMaxPayloadBytes || !isValidPreparedRegionBinding(decision.PreparedRegionBinding) {
		return false
	}
	identity := preparedRegionDecisionIdentity{SchemaVersion: decision.SchemaVersion, Consumer: decision.Consumer, PassSchema: decision.PassSchema, MaxPayloadBytes: decision.MaxPayloadBytes, PreparedRegionBinding: decision.PreparedRegionBinding}
	return decision.IdentitySHA256 == preparedRegionDigest(identity)
}

func isValidPreparedRegionBinding(binding PreparedRegionBinding) bool {
	for _, digest := range []string{binding.SourceSHA256, binding.ASTSHA256, binding.AnalysisSHA256, binding.RegionID, binding.RegionSourceSHA256, binding.LiveInsSHA256, binding.EnvironmentSHA256, binding.ExecutionProfileSHA256, binding.ImportClosureSHA256, binding.CapabilityPlanSHA256, binding.PassConfigSHA256} {
		if !digestPattern.MatchString(digest) {
			return false
		}
	}
	return binding.RegionSpan.valid() && binding.Codec == PreparedRegionCodecJSONScalarV1 && pythonIdentifierPattern.MatchString(binding.OutputName) && binding.OutputName != PreparedRegionHelperBinding
}

type PreparedRegionCapsule struct {
	SchemaVersion  string          `json:"schema_version"`
	DecisionSHA256 string          `json:"decision_sha256"`
	Codec          string          `json:"codec"`
	ValueType      string          `json:"value_type"`
	Payload        json.RawMessage `json:"payload"`
	PayloadBytes   uint32          `json:"payload_bytes"`
	PayloadSHA256  string          `json:"payload_sha256"`
	IdentitySHA256 string          `json:"identity_sha256"`
}

type preparedRegionCapsuleIdentity struct {
	SchemaVersion  string          `json:"schema_version"`
	DecisionSHA256 string          `json:"decision_sha256"`
	Codec          string          `json:"codec"`
	ValueType      string          `json:"value_type"`
	Payload        json.RawMessage `json:"payload"`
	PayloadBytes   uint32          `json:"payload_bytes"`
	PayloadSHA256  string          `json:"payload_sha256"`
}

func SealPreparedRegionCapsule(decisionSHA256 string, payload json.RawMessage) ([]byte, PreparedRegionCapsule, error) {
	valueType, ok := validatePreparedRegionPayload(payload)
	if !digestPattern.MatchString(decisionSHA256) || !ok {
		return nil, PreparedRegionCapsule{}, ErrInvalidPreparedRegion
	}
	identity := preparedRegionCapsuleIdentity{SchemaVersion: PreparedRegionCapsuleSchemaVersion, DecisionSHA256: decisionSHA256, Codec: PreparedRegionCodecJSONScalarV1, ValueType: valueType, Payload: append(json.RawMessage(nil), payload...), PayloadBytes: uint32(len(payload)), PayloadSHA256: preparedRegionBytesDigest(payload)}
	capsule := PreparedRegionCapsule{SchemaVersion: identity.SchemaVersion, DecisionSHA256: identity.DecisionSHA256, Codec: identity.Codec, ValueType: identity.ValueType, Payload: identity.Payload, PayloadBytes: identity.PayloadBytes, PayloadSHA256: identity.PayloadSHA256, IdentitySHA256: preparedRegionDigest(identity)}
	raw, err := preparedRegionCanonicalJSON(capsule)
	return raw, capsule, err
}

func DecodePreparedRegionCapsule(raw []byte) (PreparedRegionCapsule, error) {
	var value PreparedRegionCapsule
	if preparedRegionDecode(raw, &value) != nil || !value.valid() {
		return PreparedRegionCapsule{}, ErrInvalidPreparedRegion
	}
	return value, nil
}

func (capsule PreparedRegionCapsule) valid() bool {
	valueType, ok := validatePreparedRegionPayload(capsule.Payload)
	if !ok || capsule.SchemaVersion != PreparedRegionCapsuleSchemaVersion || !digestPattern.MatchString(capsule.DecisionSHA256) || capsule.Codec != PreparedRegionCodecJSONScalarV1 || capsule.ValueType != valueType || capsule.PayloadBytes != uint32(len(capsule.Payload)) || capsule.PayloadSHA256 != preparedRegionBytesDigest(capsule.Payload) {
		return false
	}
	identity := preparedRegionCapsuleIdentity{SchemaVersion: capsule.SchemaVersion, DecisionSHA256: capsule.DecisionSHA256, Codec: capsule.Codec, ValueType: capsule.ValueType, Payload: capsule.Payload, PayloadBytes: capsule.PayloadBytes, PayloadSHA256: capsule.PayloadSHA256}
	return capsule.IdentitySHA256 == preparedRegionDigest(identity)
}

func (capsule PreparedRegionCapsule) ValidateDecision(decision PreparedRegionDecision) error {
	if !capsule.valid() || !decision.valid() || capsule.DecisionSHA256 != decision.IdentitySHA256 || capsule.Codec != decision.Codec || capsule.PayloadBytes > decision.MaxPayloadBytes {
		return ErrInvalidPreparedRegion
	}
	return nil
}

func validatePreparedRegionPayload(payload json.RawMessage) (string, bool) {
	if len(payload) == 0 || len(payload) > PreparedRegionMaxPayloadBytes {
		return "", false
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return "", false
	}
	canonical, err := json.Marshal(value)
	if err != nil || !bytes.Equal(payload, canonical) {
		return "", false
	}
	switch typed := value.(type) {
	case bool:
		return "bool", true
	case json.Number:
		if !canonicalIntegerPattern.MatchString(string(typed)) {
			return "", false
		}
		if _, err := strconv.ParseInt(string(typed), 10, 64); err != nil {
			return "", false
		}
		return "int64", true
	default:
		return "", false
	}
}

type PreparedRegionPatchBinding struct {
	DecisionSHA256    string     `json:"decision_sha256"`
	FinalSourceSHA256 string     `json:"final_source_sha256"`
	FinalASTSHA256    string     `json:"final_ast_sha256"`
	DerivedASTSHA256  string     `json:"derived_ast_sha256"`
	RegionID          string     `json:"region_id"`
	RegionSpan        SourceSpan `json:"region_span"`
	OutputName        string     `json:"output_name"`
}

type PreparedRegionPatch struct {
	SchemaVersion string `json:"schema_version"`
	PassSchema    string `json:"pass_schema"`
	HelperBinding string `json:"helper_binding"`
	PreparedRegionPatchBinding
	IdentitySHA256 string `json:"identity_sha256"`
}

type preparedRegionPatchIdentity struct {
	SchemaVersion string `json:"schema_version"`
	PassSchema    string `json:"pass_schema"`
	HelperBinding string `json:"helper_binding"`
	PreparedRegionPatchBinding
}

func SealPreparedRegionPatch(binding PreparedRegionPatchBinding) ([]byte, PreparedRegionPatch, error) {
	if !validPreparedRegionPatchBinding(binding) {
		return nil, PreparedRegionPatch{}, ErrInvalidPreparedRegion
	}
	identity := preparedRegionPatchIdentity{SchemaVersion: PreparedRegionPatchSchemaVersion, PassSchema: PreparedRegionPassSchemaVersion, HelperBinding: PreparedRegionHelperBinding, PreparedRegionPatchBinding: binding}
	patch := PreparedRegionPatch{SchemaVersion: identity.SchemaVersion, PassSchema: identity.PassSchema, HelperBinding: identity.HelperBinding, PreparedRegionPatchBinding: binding, IdentitySHA256: preparedRegionDigest(identity)}
	raw, err := preparedRegionCanonicalJSON(patch)
	return raw, patch, err
}

func DecodePreparedRegionPatch(raw []byte) (PreparedRegionPatch, error) {
	var value PreparedRegionPatch
	if preparedRegionDecode(raw, &value) != nil || !value.valid() {
		return PreparedRegionPatch{}, ErrInvalidPreparedRegion
	}
	return value, nil
}

func (patch PreparedRegionPatch) ValidateBinding(binding PreparedRegionPatchBinding) error {
	if !patch.valid() || patch.PreparedRegionPatchBinding != binding {
		return ErrInvalidPreparedRegion
	}
	return nil
}

func (patch PreparedRegionPatch) ValidateDecision(decision PreparedRegionDecision) error {
	if !patch.valid() || !decision.valid() || patch.DecisionSHA256 != decision.IdentitySHA256 || patch.RegionID != decision.RegionID || patch.RegionSpan != decision.RegionSpan || patch.OutputName != decision.OutputName {
		return ErrInvalidPreparedRegion
	}
	return nil
}

func (patch PreparedRegionPatch) valid() bool {
	if patch.SchemaVersion != PreparedRegionPatchSchemaVersion || patch.PassSchema != PreparedRegionPassSchemaVersion || patch.HelperBinding != PreparedRegionHelperBinding || !validPreparedRegionPatchBinding(patch.PreparedRegionPatchBinding) {
		return false
	}
	identity := preparedRegionPatchIdentity{SchemaVersion: patch.SchemaVersion, PassSchema: patch.PassSchema, HelperBinding: patch.HelperBinding, PreparedRegionPatchBinding: patch.PreparedRegionPatchBinding}
	return patch.IdentitySHA256 == preparedRegionDigest(identity)
}

func validPreparedRegionPatchBinding(binding PreparedRegionPatchBinding) bool {
	for _, digest := range []string{binding.DecisionSHA256, binding.FinalSourceSHA256, binding.FinalASTSHA256, binding.DerivedASTSHA256, binding.RegionID} {
		if !digestPattern.MatchString(digest) {
			return false
		}
	}
	return binding.RegionSpan.valid() && pythonIdentifierPattern.MatchString(binding.OutputName) && binding.OutputName != PreparedRegionHelperBinding
}

func preparedRegionDecode(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return ErrInvalidPreparedRegion
	}
	canonical, err := preparedRegionCanonicalJSON(destination)
	if err != nil || !bytes.Equal(raw, canonical) {
		return ErrInvalidPreparedRegion
	}
	return nil
}

func preparedRegionCanonicalJSON(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var generic any
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(&generic); err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(generic)
	if err != nil {
		return nil, err
	}
	return append(canonical, '\n'), nil
}

func preparedRegionDigest(value any) string {
	canonical, err := preparedRegionCanonicalJSON(value)
	if err != nil {
		return ""
	}
	return preparedRegionBytesDigest(canonical)
}

func preparedRegionBytesDigest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(digest[:])
}

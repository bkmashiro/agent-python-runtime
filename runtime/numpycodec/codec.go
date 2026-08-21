package numpycodec

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/bkmashiro/agent-python-runtime/internal/publicationauth"
	"github.com/bkmashiro/agent-python-runtime/runtime/resultblob"
)

const (
	CodecV1                     = "numpy_ndarray_c_v1"
	DescriptorSchemaVersion     = "pysolate.numpy-ndarray-c.v1"
	ProducerValueSchemaVersion  = "pysolate.numpy-ndarray-producer-value.v1"
	MaxRank                     = 8
	MaxDimension                = 1 << 31
	MaxBodyBytes                = 8 * 1024 * 1024
	maxProducerEnvelopeOverhead = 4096
)

var (
	ErrInvalidProducer   = errors.New("invalid numpy producer value")
	ErrInvalidDescriptor = errors.New("invalid numpy ndarray descriptor")
	ErrBinding           = errors.New("numpy ndarray binding mismatch")
	ErrMaterialization   = errors.New("invalid numpy ndarray materialization")

	digestPattern     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	runIDPattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
)

var dtypeBytes = map[string]uint64{
	"|b1": 1, "|i1": 1, "|u1": 1,
	"<i2": 2, "<u2": 2, "<f2": 2,
	"<i4": 4, "<u4": 4, "<f4": 4, "<c8": 8,
	"<i8": 8, "<u8": 8, "<f8": 8, "<c16": 16,
}

type Bindings struct {
	ArtifactSHA256         string `json:"artifact_sha256"`
	ExecutionProfileID     string `json:"execution_profile_id"`
	ExecutionProfileSHA256 string `json:"execution_profile_sha256"`
	ImportClosureSHA256    string `json:"import_closure_sha256"`
	SourceSHA256           string `json:"source_sha256"`
	InputsSHA256           string `json:"inputs_sha256"`
	PassRegistrationSHA256 string `json:"pass_registration_sha256"`
}

type ProducerValue struct {
	SchemaVersion string   `json:"schema_version"`
	DType         string   `json:"dtype"`
	Shape         []uint64 `json:"shape"`
	Order         string   `json:"order"`
	CContiguous   bool     `json:"c_contiguous"`
	NBytes        uint64   `json:"nbytes"`
	BodySHA256    string   `json:"body_sha256"`
	BodyBase64    string   `json:"body_base64"`
}

type Descriptor struct {
	SchemaVersion  string   `json:"schema_version"`
	Codec          string   `json:"codec"`
	DType          string   `json:"dtype"`
	Shape          []uint64 `json:"shape"`
	Order          string   `json:"order"`
	Endianness     string   `json:"endianness"`
	NBytes         uint64   `json:"nbytes"`
	BodySHA256     string   `json:"body_sha256"`
	Bindings       Bindings `json:"bindings"`
	IdentitySHA256 string   `json:"identity_sha256"`
}

type PublicationEvidence struct {
	ProducerEnvelopeBytes uint64 `json:"producer_envelope_bytes"`
	GuestToHostCopyBytes  uint64 `json:"guest_to_host_copy_bytes"`
	DecodeSealDurationNS  int64  `json:"decode_seal_duration_ns"`
}

type MaterializationPlan struct {
	Request                []byte `json:"-"`
	LeaseID                string `json:"lease_id"`
	ConsumerBindingSHA256  string `json:"consumer_binding_sha256"`
	ConsumerSourceSHA256   string `json:"consumer_source_sha256"`
	FinalSourceSHA256      string `json:"final_source_sha256"`
	InputsSHA256           string `json:"inputs_sha256"`
	RequestSHA256          string `json:"request_sha256"`
	HostToGuestCopyBytes   uint64 `json:"host_to_guest_copy_bytes"`
	RequestBytes           uint64 `json:"request_bytes"`
	RequestBuildDurationNS int64  `json:"request_build_duration_ns"`
}

type consumerIdentity struct {
	DescriptorSHA256       string `json:"descriptor_sha256"`
	OutputName             string `json:"output_name"`
	ConsumerSourceSHA256   string `json:"consumer_source_sha256"`
	FinalSourceSHA256      string `json:"final_source_sha256"`
	ArtifactSHA256         string `json:"artifact_sha256"`
	ExecutionProfileSHA256 string `json:"execution_profile_sha256"`
	PassRegistrationSHA256 string `json:"pass_registration_sha256"`
}

type descriptorIdentity struct {
	SchemaVersion string   `json:"schema_version"`
	Codec         string   `json:"codec"`
	DType         string   `json:"dtype"`
	Shape         []uint64 `json:"shape"`
	Order         string   `json:"order"`
	Endianness    string   `json:"endianness"`
	NBytes        uint64   `json:"nbytes"`
	BodySHA256    string   `json:"body_sha256"`
	Bindings      Bindings `json:"bindings"`
}

type producerAuthorityIdentity struct {
	SchemaVersion string   `json:"schema_version"`
	RunID         string   `json:"run_id"`
	RawSHA256     string   `json:"raw_sha256"`
	Bindings      Bindings `json:"bindings"`
	MaxBodyBytes  uint64   `json:"max_body_bytes"`
}

func ProducerAuthorityBinding(runID string, raw []byte, bindings Bindings, maxBodyBytes uint64) (string, error) {
	if !runIDPattern.MatchString(runID) || !validBindings(bindings) || maxBodyBytes == 0 || maxBodyBytes > MaxBodyBytes {
		return "", ErrBinding
	}
	maxEncodedBodyBytes := ((maxBodyBytes + 2) / 3) * 4
	if len(raw) == 0 || uint64(len(raw)) > maxEncodedBodyBytes+maxProducerEnvelopeOverhead {
		return "", ErrInvalidProducer
	}
	return digestJSON(producerAuthorityIdentity{
		SchemaVersion: "pysolate.numpy-producer-publication-authority.v1", RunID: runID,
		RawSHA256: resultblob.BytesDigest(raw), Bindings: bindings, MaxBodyBytes: maxBodyBytes,
	}), nil
}

func DecodeProducerValue(raw []byte, bindings Bindings, maxBodyBytes uint64) (Descriptor, []byte, error) {
	if !validBindings(bindings) {
		return Descriptor{}, nil, ErrBinding
	}
	if maxBodyBytes == 0 || maxBodyBytes > MaxBodyBytes {
		return Descriptor{}, nil, ErrInvalidProducer
	}
	maxEncodedBodyBytes := ((maxBodyBytes + 2) / 3) * 4
	if uint64(len(raw)) > maxEncodedBodyBytes+maxProducerEnvelopeOverhead || rejectDuplicateJSON(raw) != nil {
		return Descriptor{}, nil, ErrInvalidProducer
	}
	var value ProducerValue
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil || decoder.Decode(&struct{}{}) != io.EOF ||
		value.SchemaVersion != ProducerValueSchemaVersion || value.Order != "C" || !value.CContiguous ||
		!digestPattern.MatchString(value.BodySHA256) || value.NBytes == 0 || value.NBytes > maxBodyBytes {
		return Descriptor{}, nil, ErrInvalidProducer
	}
	itemBytes, ok := dtypeBytes[value.DType]
	expectedEncodedBodyBytes := ((value.NBytes + 2) / 3) * 4
	if !ok || !validShape(value.Shape, itemBytes, value.NBytes) || uint64(len(value.BodyBase64)) != expectedEncodedBodyBytes {
		return Descriptor{}, nil, ErrInvalidProducer
	}
	body, err := base64.StdEncoding.Strict().DecodeString(value.BodyBase64)
	if err != nil || base64.StdEncoding.EncodeToString(body) != value.BodyBase64 || uint64(len(body)) != value.NBytes || resultblob.BytesDigest(body) != value.BodySHA256 {
		return Descriptor{}, nil, ErrInvalidProducer
	}
	endianness := "little"
	if itemBytes == 1 {
		endianness = "not_applicable"
	}
	identity := descriptorIdentity{
		SchemaVersion: DescriptorSchemaVersion, Codec: CodecV1, DType: value.DType,
		Shape: append([]uint64(nil), value.Shape...), Order: "C", Endianness: endianness,
		NBytes: value.NBytes, BodySHA256: value.BodySHA256, Bindings: bindings,
	}
	descriptor := Descriptor{
		SchemaVersion: identity.SchemaVersion, Codec: identity.Codec, DType: identity.DType,
		Shape: append([]uint64(nil), identity.Shape...), Order: identity.Order, Endianness: identity.Endianness,
		NBytes: identity.NBytes, BodySHA256: identity.BodySHA256, Bindings: identity.Bindings,
		IdentitySHA256: digestJSON(identity),
	}
	return descriptor, append([]byte(nil), body...), nil
}

func (descriptor Descriptor) CanonicalJSON() ([]byte, error) {
	if !descriptor.valid() {
		return nil, ErrInvalidDescriptor
	}
	return canonicalJSON(descriptor)
}

func DecodeDescriptor(raw []byte) (Descriptor, error) {
	if rejectDuplicateJSON(raw) != nil {
		return Descriptor{}, ErrInvalidDescriptor
	}
	var descriptor Descriptor
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&descriptor) != nil || decoder.Decode(&struct{}{}) != io.EOF || !descriptor.valid() {
		return Descriptor{}, ErrInvalidDescriptor
	}
	canonical, err := canonicalJSON(descriptor)
	if err != nil || !bytes.Equal(canonical, raw) {
		return Descriptor{}, ErrInvalidDescriptor
	}
	return descriptor, nil
}

func (descriptor Descriptor) valid() bool {
	itemBytes, ok := dtypeBytes[descriptor.DType]
	if !ok || descriptor.SchemaVersion != DescriptorSchemaVersion || descriptor.Codec != CodecV1 || descriptor.Order != "C" ||
		descriptor.NBytes == 0 || descriptor.NBytes > MaxBodyBytes || !digestPattern.MatchString(descriptor.BodySHA256) ||
		!validBindings(descriptor.Bindings) || !validShape(descriptor.Shape, itemBytes, descriptor.NBytes) {
		return false
	}
	expectedEndianness := "little"
	if itemBytes == 1 {
		expectedEndianness = "not_applicable"
	}
	if descriptor.Endianness != expectedEndianness || !digestPattern.MatchString(descriptor.IdentitySHA256) {
		return false
	}
	identity := descriptorIdentity{
		SchemaVersion: descriptor.SchemaVersion, Codec: descriptor.Codec, DType: descriptor.DType,
		Shape: append([]uint64(nil), descriptor.Shape...), Order: descriptor.Order, Endianness: descriptor.Endianness,
		NBytes: descriptor.NBytes, BodySHA256: descriptor.BodySHA256, Bindings: descriptor.Bindings,
	}
	return descriptor.IdentitySHA256 == digestJSON(identity)
}

func Publish(ctx context.Context, store *resultblob.Store, runID string, producerRaw []byte, bindings Bindings, authority resultblob.ProducerAuthority, maxBodyBytes uint64) (Descriptor, resultblob.Descriptor, PublicationEvidence, error) {
	started := time.Now()
	if store == nil {
		return Descriptor{}, resultblob.Descriptor{}, PublicationEvidence{}, ErrMaterialization
	}
	authorityBinding, err := ProducerAuthorityBinding(runID, producerRaw, bindings, maxBodyBytes)
	if err != nil || !authority.Valid(authorityBinding) {
		return Descriptor{}, resultblob.Descriptor{}, PublicationEvidence{}, ErrMaterialization
	}
	descriptor, body, err := DecodeProducerValue(producerRaw, bindings, maxBodyBytes)
	if err != nil {
		return Descriptor{}, resultblob.Descriptor{}, PublicationEvidence{}, err
	}
	metadata, err := descriptor.CanonicalJSON()
	if err != nil {
		return Descriptor{}, resultblob.Descriptor{}, PublicationEvidence{}, err
	}
	publicationIdentity, err := resultblob.PublicationIdentitySHA256(runID, CodecV1, metadata, descriptor.IdentitySHA256, body)
	if err != nil {
		return Descriptor{}, resultblob.Descriptor{}, PublicationEvidence{}, err
	}
	blob, err := store.Publish(ctx, resultblob.Publication{
		RunID: runID, Codec: CodecV1, Metadata: metadata, BindingSHA256: descriptor.IdentitySHA256,
		ExpectedBodySHA256: descriptor.BodySHA256,
		Guard:              resultblob.NewPublicationGuard(publicationauth.Mint(publicationIdentity)),
	}, body)
	if err != nil {
		return Descriptor{}, resultblob.Descriptor{}, PublicationEvidence{}, err
	}
	evidence := PublicationEvidence{
		ProducerEnvelopeBytes: uint64(len(producerRaw)), GuestToHostCopyBytes: descriptor.NBytes,
		DecodeSealDurationNS: time.Since(started).Nanoseconds(),
	}
	return descriptor, blob, evidence, nil
}

const materializationPrelude = `import base64 as __pysolate_b64
import datetime as __pysolate_datetime
import hashlib as __pysolate_hashlib
import numpy as __pysolate_np
__pysolate_materialization_started = __pysolate_datetime.datetime.now(__pysolate_datetime.UTC)
__pysolate_desc = inputs['pysolate_ndarray_descriptor']
__pysolate_b64_text = inputs['pysolate_ndarray_body_b64']
__pysolate_body = __pysolate_b64.b64decode(__pysolate_b64_text, validate=True)
assert __pysolate_b64.b64encode(__pysolate_body).decode('ascii') == __pysolate_b64_text
assert __pysolate_hashlib.sha256(__pysolate_body).hexdigest() == __pysolate_desc['body_sha256'][7:]
assert len(__pysolate_body) == __pysolate_desc['nbytes']
__pysolate_dtype = __pysolate_np.dtype(__pysolate_desc['dtype'])
assert __pysolate_dtype.str == __pysolate_desc['dtype']
__PYSOLATE_OUTPUT__ = __pysolate_np.frombuffer(__pysolate_body, dtype=__pysolate_dtype).reshape(tuple(__pysolate_desc['shape']), order='C').copy(order='C')
assert __PYSOLATE_OUTPUT__.flags.c_contiguous
__pysolate_materialization_duration_ns = int((__pysolate_datetime.datetime.now(__pysolate_datetime.UTC) - __pysolate_materialization_started).total_seconds() * 1_000_000_000)
`

const materializationResultWrapper = `
__pysolate_consumer_value = result
result = {
    'schema_version': 'pysolate.numpy-materialization-result.v1',
    'value': __pysolate_consumer_value,
    'materialization_duration_ns': __pysolate_materialization_duration_ns,
}
`

type materializationRequest struct {
	RunID         string                       `json:"run_id"`
	Code          string                       `json:"code"`
	Inputs        materializationInputs        `json:"inputs"`
	Compatibility materializationCompatibility `json:"compatibility"`
}

type materializationInputs struct {
	Descriptor Descriptor `json:"pysolate_ndarray_descriptor"`
	BodyBase64 string     `json:"pysolate_ndarray_body_b64"`
}

type materializationCompatibility struct {
	Profile string   `json:"profile"`
	Imports []string `json:"imports"`
}

func ConsumerIdentity(descriptor Descriptor, outputName, consumerSource string) (string, error) {
	finalSource, err := buildFinalSource(outputName, consumerSource)
	if err != nil || !descriptor.valid() {
		return "", ErrMaterialization
	}
	return digestJSON(consumerIdentity{
		DescriptorSHA256: descriptor.IdentitySHA256, OutputName: outputName,
		ConsumerSourceSHA256: digestRaw([]byte(consumerSource)), FinalSourceSHA256: digestRaw(finalSource),
		ArtifactSHA256: descriptor.Bindings.ArtifactSHA256, ExecutionProfileSHA256: descriptor.Bindings.ExecutionProfileSHA256,
		PassRegistrationSHA256: descriptor.Bindings.PassRegistrationSHA256,
	}), nil
}

func BuildMaterializationRequest(runID, outputName string, claim resultblob.Claim, consumerSource string) (MaterializationPlan, error) {
	started := time.Now()
	if !runIDPattern.MatchString(runID) {
		return MaterializationPlan{}, ErrMaterialization
	}
	descriptor, err := DecodeDescriptor(claim.Metadata)
	_, blobDescriptorErr := claim.Descriptor.CanonicalJSON()
	if err != nil || blobDescriptorErr != nil || claim.Descriptor.SchemaVersion != resultblob.DescriptorSchemaVersion ||
		claim.Descriptor.Codec != CodecV1 || claim.Descriptor.MetadataBytes != uint32(len(claim.Metadata)) ||
		claim.Descriptor.MetadataSHA256 != resultblob.BytesDigest(claim.Metadata) || claim.Descriptor.BodyBytes != uint64(len(claim.Body)) ||
		claim.Descriptor.BodySHA256 != resultblob.BytesDigest(claim.Body) || claim.Descriptor.BodySHA256 != descriptor.BodySHA256 ||
		descriptor.NBytes != uint64(len(claim.Body)) ||
		claim.Descriptor.BindingSHA256 != descriptor.IdentitySHA256 || claim.BlobID != claim.Descriptor.IdentitySHA256 ||
		!digestPattern.MatchString(claim.LeaseID) || !digestPattern.MatchString(claim.ConsumerSHA256) {
		return MaterializationPlan{}, ErrMaterialization
	}
	finalSource, err := buildFinalSource(outputName, consumerSource)
	if err != nil {
		return MaterializationPlan{}, err
	}
	consumerSHA, err := ConsumerIdentity(descriptor, outputName, consumerSource)
	if err != nil || consumerSHA != claim.ConsumerSHA256 {
		return MaterializationPlan{}, ErrBinding
	}
	inputs := materializationInputs{Descriptor: descriptor, BodyBase64: base64.StdEncoding.EncodeToString(claim.Body)}
	inputsRaw, err := json.Marshal(inputs)
	if err != nil {
		return MaterializationPlan{}, ErrMaterialization
	}
	request := materializationRequest{
		RunID: runID, Code: string(finalSource), Inputs: inputs,
		Compatibility: materializationCompatibility{Profile: "numpy-core", Imports: []string{"base64", "datetime", "hashlib", "numpy"}},
	}
	requestRaw, err := json.Marshal(request)
	if err != nil {
		return MaterializationPlan{}, ErrMaterialization
	}
	return MaterializationPlan{
		Request: append([]byte(nil), requestRaw...), LeaseID: claim.LeaseID, ConsumerBindingSHA256: consumerSHA,
		ConsumerSourceSHA256: digestRaw([]byte(consumerSource)), FinalSourceSHA256: digestRaw(finalSource),
		InputsSHA256: digestRaw(inputsRaw), RequestSHA256: digestRaw(requestRaw), HostToGuestCopyBytes: descriptor.NBytes,
		RequestBytes: uint64(len(requestRaw)), RequestBuildDurationNS: time.Since(started).Nanoseconds(),
	}, nil
}

func buildFinalSource(outputName, consumerSource string) ([]byte, error) {
	if !identifierPattern.MatchString(outputName) || outputName == "result" || strings.HasPrefix(outputName, "__pysolate") ||
		outputName == "pysolate_ndarray_descriptor" || outputName == "pysolate_ndarray_body_b64" ||
		len(consumerSource) == 0 || len(consumerSource) > 64*1024 || strings.ContainsRune(consumerSource, '\x00') {
		return nil, ErrMaterialization
	}
	code := bytes.ReplaceAll([]byte(materializationPrelude), []byte("__PYSOLATE_OUTPUT__"), []byte(outputName))
	code = append(code, consumerSource...)
	code = append(code, materializationResultWrapper...)
	return code, nil
}

func validBindings(bindings Bindings) bool {
	if bindings.ExecutionProfileID != "numpy-core" {
		return false
	}
	for _, digest := range []string{
		bindings.ArtifactSHA256, bindings.ExecutionProfileSHA256, bindings.ImportClosureSHA256,
		bindings.SourceSHA256, bindings.InputsSHA256, bindings.PassRegistrationSHA256,
	} {
		if !digestPattern.MatchString(digest) {
			return false
		}
	}
	return true
}

func validShape(shape []uint64, itemBytes, nbytes uint64) bool {
	if len(shape) == 0 || len(shape) > MaxRank {
		return false
	}
	count := uint64(1)
	for _, dimension := range shape {
		if dimension == 0 || dimension > MaxDimension || count > math.MaxUint64/dimension {
			return false
		}
		count *= dimension
	}
	return count <= math.MaxUint64/itemBytes && count*itemBytes == nbytes
}

func digestJSON(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return digestRaw(raw)
}

func digestRaw(raw []byte) string {
	digest := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func canonicalJSON(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var generic any
	if decoder.Decode(&generic) != nil {
		return nil, ErrInvalidDescriptor
	}
	return json.Marshal(generic)
}

func rejectDuplicateJSON(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := parseJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return ErrInvalidProducer
	}
	return nil
}

func parseJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return ErrInvalidProducer
			}
			if _, duplicate := seen[key]; duplicate {
				return ErrInvalidProducer
			}
			seen[key] = struct{}{}
			if err := parseJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return ErrInvalidProducer
		}
	case '[':
		for decoder.More() {
			if err := parseJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return ErrInvalidProducer
		}
	default:
		return ErrInvalidProducer
	}
	return nil
}

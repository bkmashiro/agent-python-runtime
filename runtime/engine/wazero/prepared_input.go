package wazero

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/numpycodec"
)

const PreparedNumpyABIV1 = "pysolate.prepared-numpy-abi.v1"

var (
	ErrPreparedNumpyInput         = errors.New("invalid prepared numpy input")
	ErrPreparedImageCompatibility = errors.New("prepared image compatibility mismatch")
)

type preparedNumpyDescriptor struct {
	SchemaVersion string   `json:"schema_version"`
	Name          string   `json:"name"`
	Codec         string   `json:"codec"`
	DType         string   `json:"dtype"`
	Shape         []uint64 `json:"shape"`
	Order         string   `json:"order"`
	Endianness    string   `json:"endianness"`
	NBytes        uint64   `json:"nbytes"`
	BodySHA256    string   `json:"body_sha256"`
	InputSHA256   string   `json:"input_sha256"`
}

type PreparedNumpyInput struct {
	name           string
	descriptor     numpycodec.Descriptor
	descriptorJSON []byte
	body           []byte
	identity       string
}

func (input PreparedNumpyInput) Name() string { return input.name }

func (input PreparedNumpyInput) IdentitySHA256() string { return input.identity }

func (input PreparedNumpyInput) Descriptor() numpycodec.Descriptor {
	descriptor := input.descriptor
	descriptor.Shape = append([]uint64(nil), descriptor.Shape...)
	return descriptor
}

func NewPreparedNumpyInput(name string, descriptor numpycodec.Descriptor, body []byte) (PreparedNumpyInput, error) {
	if !validPreparedName(name) || len(body) == 0 || len(body) > numpycodec.MaxBodyBytes {
		return PreparedNumpyInput{}, ErrPreparedNumpyInput
	}
	canonical, err := descriptor.CanonicalJSON()
	if err != nil || descriptor.NBytes != uint64(len(body)) || descriptor.BodySHA256 != digestPreparedBytes(body) {
		return PreparedNumpyInput{}, ErrPreparedNumpyInput
	}
	shape := append([]uint64(nil), descriptor.Shape...)
	descriptor.Shape = shape
	internal := preparedNumpyDescriptor{
		SchemaVersion: "pysolate.prepared-numpy-input.v1",
		Name:          name,
		Codec:         descriptor.Codec,
		DType:         descriptor.DType,
		Shape:         append([]uint64(nil), shape...),
		Order:         descriptor.Order,
		Endianness:    descriptor.Endianness,
		NBytes:        descriptor.NBytes,
		BodySHA256:    descriptor.BodySHA256,
		InputSHA256:   digestPreparedBytes(canonical),
	}
	descriptorJSON, err := json.Marshal(internal)
	if err != nil || len(descriptorJSON) > 4096 {
		return PreparedNumpyInput{}, ErrPreparedNumpyInput
	}
	return PreparedNumpyInput{
		name:           name,
		descriptor:     descriptor,
		descriptorJSON: append([]byte(nil), descriptorJSON...),
		body:           append([]byte(nil), body...),
		identity:       digestPreparedBytes(descriptorJSON),
	}, nil
}

func validPreparedName(name string) bool {
	if len(name) == 0 || len(name) > 128 || name == "__builtins__" {
		return false
	}
	for index, character := range []byte(name) {
		letter := character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z'
		if !(letter || character == '_' || index > 0 && character >= '0' && character <= '9') {
			return false
		}
	}
	return true
}

func digestPreparedBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func cloneFamilyRunConfig(config runtimeconfig.RunConfig) runtimeconfig.RunConfig {
	clone := config
	clone.CapabilityGrants = make(map[string]runtimeconfig.CapabilityGrant, len(config.CapabilityGrants))
	for name, grant := range config.CapabilityGrants {
		clone.CapabilityGrants[name] = grant
	}
	if config.ColdIO != nil {
		value := *config.ColdIO
		clone.ColdIO = &value
	}
	if config.DeterministicVerification != nil {
		value := *config.DeterministicVerification
		clone.DeterministicVerification = &value
	}
	if profile := config.ExecutionProfile; profile != nil {
		value, err := runtimeconfig.NewExecutionProfile(profile.ID(), profile.AllowedImports())
		if err == nil && profile.ArtifactSHA256() != "" {
			value, err = value.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{
				ProfileID:            profile.ID(),
				ArtifactSHA256:       profile.ArtifactSHA256(),
				ManifestSHA256:       profile.ManifestSHA256(),
				ImportRoots:          profile.AvailableImports(),
				QualifiedImportRoots: profile.QualifiedImports(),
			})
		}
		if err == nil {
			clone.ExecutionProfile = &value
		}
	}
	return clone
}

type preparedImageIdentityDocument struct {
	SchemaVersion              string   `json:"schema_version"`
	ABI                        string   `json:"abi"`
	ArtifactSHA256             string   `json:"artifact_sha256"`
	ManifestSHA256             string   `json:"manifest_sha256"`
	ProfileID                  string   `json:"profile_id"`
	AllowedImports             []string `json:"allowed_imports"`
	AvailableImports           []string `json:"available_imports"`
	QualifiedImports           []string `json:"qualified_imports"`
	DeterministicProfileSHA256 string   `json:"deterministic_profile_sha256"`
	MemoryLimitPages           uint32   `json:"memory_limit_pages"`
	InputSHA256                string   `json:"input_sha256"`
	Name                       string   `json:"name"`
	Codec                      string   `json:"codec"`
	DType                      string   `json:"dtype"`
	Shape                      []uint64 `json:"shape"`
	Order                      string   `json:"order"`
	Endianness                 string   `json:"endianness"`
	NBytes                     uint64   `json:"nbytes"`
	BodySHA256                 string   `json:"body_sha256"`
}

func preparedImageIdentity(config runtimeconfig.RunConfig, input PreparedNumpyInput, abi string) (string, error) {
	if abi != PreparedNumpyABIV1 || config.Validate() != nil || input.identity == "" || config.ExecutionProfile == nil {
		return "", ErrPreparedImageCompatibility
	}
	profile := config.ExecutionProfile
	if profile.ID() != "numpy-core" || profile.ArtifactSHA256() == "" || profile.ManifestSHA256() == "" {
		return "", ErrPreparedImageCompatibility
	}
	deterministic := "none"
	if config.DeterministicVerification != nil {
		deterministic = config.DeterministicVerification.Identity()
	}
	document := preparedImageIdentityDocument{
		SchemaVersion: "pysolate.prepared-family-image.v1", ABI: abi,
		ArtifactSHA256: profile.ArtifactSHA256(), ManifestSHA256: profile.ManifestSHA256(), ProfileID: profile.ID(),
		AllowedImports: profile.AllowedImports(), AvailableImports: profile.AvailableImports(), QualifiedImports: profile.QualifiedImports(),
		DeterministicProfileSHA256: deterministic, MemoryLimitPages: config.MemoryLimitPages,
		InputSHA256: input.identity, Name: input.name, Codec: input.descriptor.Codec, DType: input.descriptor.DType,
		Shape: append([]uint64(nil), input.descriptor.Shape...), Order: input.descriptor.Order,
		Endianness: input.descriptor.Endianness, NBytes: input.descriptor.NBytes, BodySHA256: input.descriptor.BodySHA256,
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return "", fmt.Errorf("encode prepared image identity: %w", err)
	}
	return digestPreparedBytes(encoded), nil
}

package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
)

const ExecutionArtifactSchemaVersion = "pysolate.execution-artifact.v1"

var (
	ErrInvalidShardProfile      = errors.New("invalid execution shard profile")
	ErrInvalidExecutionArtifact = errors.New("invalid execution artifact")
)

type ExecutionBackend string

const (
	BackendPysolateWASM  ExecutionBackend = "pysolate_wasm"
	BackendNativeSandbox ExecutionBackend = "native_sandbox"
)

func (backend ExecutionBackend) Valid() bool {
	return backend == BackendPysolateWASM || backend == BackendNativeSandbox
}

type ArtifactKind string

const (
	ArtifactWASMDistribution ArtifactKind = "wasm_distribution"
	ArtifactOCIImage         ArtifactKind = "oci_image"
)

type ExecutionArtifact struct {
	SchemaVersion  string           `json:"schema_version"`
	Backend        ExecutionBackend `json:"backend"`
	Kind           ArtifactKind     `json:"kind"`
	ProfileID      string           `json:"profile_id"`
	ShardID        string           `json:"shard_id,omitempty"`
	Target         string           `json:"target"`
	ArtifactSHA256 string           `json:"artifact_sha256,omitempty"`
	ManifestSHA256 string           `json:"manifest_sha256,omitempty"`
	ImageDigest    string           `json:"image_digest,omitempty"`
	RootFSSHA256   string           `json:"rootfs_sha256,omitempty"`
}

func (artifact ExecutionArtifact) Validate() error {
	if artifact.SchemaVersion != ExecutionArtifactSchemaVersion || !artifact.Backend.Valid() {
		return ErrInvalidExecutionArtifact
	}
	switch artifact.Backend {
	case BackendPysolateWASM:
		if artifact.Kind != ArtifactWASMDistribution || artifact.ProfileID != "base" || artifact.ShardID != "plain" ||
			artifact.Target != "wasm32-wasip1" || !validProfileDigest(artifact.ArtifactSHA256) ||
			!validProfileDigest(artifact.ManifestSHA256) || artifact.ImageDigest != "" || artifact.RootFSSHA256 != "" {
			return ErrInvalidExecutionArtifact
		}
	case BackendNativeSandbox:
		if artifact.Kind != ArtifactOCIImage || artifact.ProfileID != "native-python" || artifact.ShardID != "" ||
			(artifact.Target != "linux/amd64" && artifact.Target != "linux/arm64") || !validProfileDigest(artifact.ImageDigest) || !validProfileDigest(artifact.RootFSSHA256) ||
			artifact.ArtifactSHA256 != "" || artifact.ManifestSHA256 != "" {
			return ErrInvalidExecutionArtifact
		}
	}
	return nil
}

type ShardIdlePolicy string

const ShardIdleRetireWhenIdle ShardIdlePolicy = "retire_when_idle"

type ShardProfileConfig struct {
	ID                     string
	ExecutionProfileID     string
	QualifiedImports       []string
	ArtifactSHA256         string
	ManifestSHA256         string
	PreparedBaselineSHA256 string
	IdlePolicy             ShardIdlePolicy
}

type ShardProfile struct {
	id                     string
	executionProfileID     string
	qualifiedImports       []string
	artifactSHA256         string
	manifestSHA256         string
	preparedBaselineSHA256 string
	idlePolicy             ShardIdlePolicy
	identity               string
}

func NewShardProfile(config ShardProfileConfig) (ShardProfile, error) {
	expectedProfile := map[string]string{"plain": "base", "numpy": "numpy-core"}[config.ID]
	if expectedProfile == "" || config.ExecutionProfileID != expectedProfile || len(config.QualifiedImports) == 0 ||
		len(config.QualifiedImports) > maxDeclaredImports || !validProfileDigest(config.ArtifactSHA256) ||
		!validProfileDigest(config.ManifestSHA256) || config.IdlePolicy != ShardIdleRetireWhenIdle ||
		(config.PreparedBaselineSHA256 != "" && !validProfileDigest(config.PreparedBaselineSHA256)) {
		return ShardProfile{}, ErrInvalidShardProfile
	}
	imports := append([]string(nil), config.QualifiedImports...)
	sort.Strings(imports)
	for index, module := range imports {
		if !validImportName(module) || module == "" || (index > 0 && imports[index-1] == module) {
			return ShardProfile{}, ErrInvalidShardProfile
		}
	}
	document := struct {
		SchemaVersion          string          `json:"schema_version"`
		ID                     string          `json:"id"`
		ExecutionProfileID     string          `json:"execution_profile_id"`
		QualifiedImports       []string        `json:"qualified_imports"`
		ArtifactSHA256         string          `json:"artifact_sha256"`
		ManifestSHA256         string          `json:"manifest_sha256"`
		PreparedBaselineSHA256 string          `json:"prepared_baseline_sha256,omitempty"`
		IdlePolicy             ShardIdlePolicy `json:"idle_policy"`
	}{"pysolate.shard-profile.v1", config.ID, config.ExecutionProfileID, imports, config.ArtifactSHA256, config.ManifestSHA256, config.PreparedBaselineSHA256, config.IdlePolicy}
	encoded, err := json.Marshal(document)
	if err != nil {
		return ShardProfile{}, ErrInvalidShardProfile
	}
	digest := sha256.Sum256(encoded)
	return ShardProfile{
		id: config.ID, executionProfileID: config.ExecutionProfileID, qualifiedImports: imports,
		artifactSHA256: config.ArtifactSHA256, manifestSHA256: config.ManifestSHA256,
		preparedBaselineSHA256: config.PreparedBaselineSHA256, idlePolicy: config.IdlePolicy,
		identity: "sha256:" + hex.EncodeToString(digest[:]),
	}, nil
}

func (profile ShardProfile) ID() string                 { return profile.id }
func (profile ShardProfile) ExecutionProfileID() string { return profile.executionProfileID }
func (profile ShardProfile) QualifiedImports() []string {
	return append([]string(nil), profile.qualifiedImports...)
}
func (profile ShardProfile) ArtifactSHA256() string         { return profile.artifactSHA256 }
func (profile ShardProfile) ManifestSHA256() string         { return profile.manifestSHA256 }
func (profile ShardProfile) PreparedBaselineSHA256() string { return profile.preparedBaselineSHA256 }
func (profile ShardProfile) IdlePolicy() ShardIdlePolicy    { return profile.idlePolicy }
func (profile ShardProfile) Identity() string               { return profile.identity }

func (artifact ExecutionArtifact) Identity() string {
	if artifact.Validate() != nil {
		return ""
	}
	encoded, err := json.Marshal(artifact)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:])
}

type NativeStateClass string

const (
	StatePortableValue NativeStateClass = "portable_value"
	StateWorkspaceRef  NativeStateClass = "native_workspace_ref"
	StateProcessRef    NativeStateClass = "native_process_ref"
	StateOpaque        NativeStateClass = "opaque"
)

func (state NativeStateClass) Valid() bool {
	return state == StatePortableValue || state == StateWorkspaceRef || state == StateProcessRef || state == StateOpaque
}

type NativeLeaseClass string

const (
	LeaseDestroyImmediately NativeLeaseClass = "destroy_immediately"
	LeaseWorkspaceGrace     NativeLeaseClass = "workspace_grace"
	LeaseLiveProcess        NativeLeaseClass = "live_process"
)

func (lease NativeLeaseClass) Valid() bool {
	return lease == LeaseDestroyImmediately || lease == LeaseWorkspaceGrace || lease == LeaseLiveProcess
}

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type artifactIdentity struct {
	Filename        string `json:"filename"`
	SHA256          string `json:"sha256"`
	Size            int64  `json:"size_bytes"`
	SourceCommit    string `json:"source_commit"`
	ArtifactProfile string `json:"artifact_profile,omitempty"`
	Target          string `json:"target"`
	Execution       string `json:"execution_model"`
}

type backendIdentity struct {
	Name      string `json:"name"`
	ResetMode string `json:"reset_mode"`
}

type hostSourceIdentity struct {
	Revision string `json:"revision"`
	Modified bool   `json:"modified"`
}

type compileEvidence struct {
	InstantiateHostNS int64 `json:"instantiate_host_ns"`
	CompileNS         int64 `json:"compile_ns"`
}

type sampleEvidence struct {
	InstantiateGuestNS int64 `json:"instantiate_guest_ns"`
	InitializeNS       int64 `json:"_initialize_ns"`
	RuntimeInitNS      int64 `json:"runtime_init_ns"`
	AttachHostCallsNS  int64 `json:"attach_host_calls_ns,omitempty"`
	PrepareNS          int64 `json:"prepare_ns"`
	SourceValidateNS   int64 `json:"source_validate_ns,omitempty"`
	ExecuteNS          int64 `json:"execute_ns"`
	CapabilityNS       int64 `json:"capability_ns"`
	RunTotalNS         int64 `json:"run_total_ns"`
	RequestBytes       int   `json:"request_bytes"`
	ResultBytes        int   `json:"result_bytes"`
}

type workloadEvidence struct {
	Execute    []sampleEvidence `json:"execute"`
	Capability []sampleEvidence `json:"capability"`
}

type environmentIdentity struct {
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
	GoVersion string `json:"go_version"`
}

type fixtureIdentity struct {
	Workload                 string `json:"workload,omitempty"`
	Samples                  int    `json:"samples"`
	CapabilityOperations     int    `json:"capability_operations"`
	ProviderDelayNanoseconds int64  `json:"provider_delay_ns_per_operation"`
}

type benchmarkEvidence struct {
	SchemaVersion int                 `json:"schema_version"`
	EvidenceClass string              `json:"evidence_class"`
	Artifact      artifactIdentity    `json:"artifact"`
	HostSource    hostSourceIdentity  `json:"host_source"`
	Backend       backendIdentity     `json:"backend"`
	Environment   environmentIdentity `json:"environment"`
	Fixture       fixtureIdentity     `json:"fixture"`
	CompileOnce   compileEvidence     `json:"compile_once"`
	Workloads     workloadEvidence    `json:"workloads"`
	Limitations   []string            `json:"limitations"`
}

func validateEvidenceClassProfile(class, profile string) error {
	switch class {
	case "production-safe", "full":
		if profile != "" && profile != "base" {
			return errors.New("non-base artifact cannot use production-safe or full evidence class")
		}
	case "profile-candidate":
		if profile != "numpy-core" {
			return errors.New("profile-candidate evidence requires numpy-core artifact profile")
		}
	case "preinitialization-spike":
		if profile != "base" {
			return errors.New("preinitialization-spike evidence requires base artifact profile")
		}
	default:
		return errors.New("unsupported evidence class")
	}
	return nil
}

func (evidence benchmarkEvidence) Validate() error {
	if evidence.SchemaVersion != 1 {
		return errors.New("unsupported benchmark schema version")
	}
	if err := validateEvidenceClassProfile(evidence.EvidenceClass, evidence.Artifact.ArtifactProfile); err != nil {
		return err
	}
	if evidence.Artifact.SHA256 == "" || evidence.Artifact.Size <= 0 || evidence.Artifact.SourceCommit == "" || evidence.Artifact.Target != "wasm32-wasip1" {
		return errors.New("artifact identity is incomplete")
	}
	if evidence.HostSource.Revision == "" || evidence.HostSource.Modified {
		return errors.New("Host benchmark source must be an exact clean revision")
	}
	if evidence.Backend.Name == "" || evidence.Backend.ResetMode == "" || evidence.CompileOnce.InstantiateHostNS <= 0 || evidence.CompileOnce.CompileNS <= 0 {
		return errors.New("backend or compile evidence is incomplete")
	}
	if len(evidence.Workloads.Execute) == 0 || len(evidence.Workloads.Execute) != len(evidence.Workloads.Capability) {
		return errors.New("workload sample sets are incomplete")
	}
	if evidence.Fixture.Samples != len(evidence.Workloads.Execute) || evidence.Fixture.Samples < 3 || evidence.Fixture.Samples > 20 ||
		evidence.Fixture.ProviderDelayNanoseconds != 2_000_000 {
		return errors.New("fixture sample count or provider delay drifted")
	}
	wantOperations := 1
	if evidence.EvidenceClass == "full" {
		wantOperations = 20
	}
	if evidence.Fixture.CapabilityOperations != wantOperations {
		return errors.New("fixture operation count does not match evidence class")
	}
	for _, sample := range evidence.Workloads.Execute {
		if err := validateSample(sample, false); err != nil {
			return fmt.Errorf("execute sample: %w", err)
		}
	}
	for _, sample := range evidence.Workloads.Capability {
		if err := validateSample(sample, true); err != nil {
			return fmt.Errorf("capability sample: %w", err)
		}
	}
	return nil
}

func validateSample(sample sampleEvidence, requireCapability bool) error {
	if sample.InstantiateGuestNS <= 0 || sample.InitializeNS <= 0 || sample.RuntimeInitNS <= 0 ||
		sample.PrepareNS <= 0 || sample.ExecuteNS <= 0 || sample.RunTotalNS <= 0 ||
		sample.RequestBytes <= 0 || sample.ResultBytes <= 0 {
		return errors.New("lifecycle phase or byte count is missing")
	}
	if requireCapability && sample.CapabilityNS <= 0 {
		return errors.New("capability phase is missing")
	}
	if !requireCapability && sample.CapabilityNS != 0 {
		return errors.New("execute-only sample unexpectedly used a capability")
	}
	return nil
}

type artifactManifest struct {
	ArtifactProfile string `json:"artifact_profile"`
	Artifact        struct {
		Filename string `json:"filename"`
		SHA256   string `json:"sha256"`
		Size     int64  `json:"size"`
	} `json:"artifact"`
	Build struct {
		RepositoryCommit string `json:"repository_commit"`
		CompilerTarget   string `json:"compiler_target"`
		ExecutionModel   string `json:"execution_model"`
	} `json:"build"`
	Target string `json:"target"`
}

func loadArtifactIdentity(artifactPath, manifestPath string) (artifactIdentity, []byte, error) {
	artifact, err := os.ReadFile(artifactPath)
	if err != nil {
		return artifactIdentity{}, nil, fmt.Errorf("read artifact: %w", err)
	}
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		return artifactIdentity{}, nil, fmt.Errorf("read manifest: %w", err)
	}
	var manifest artifactManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return artifactIdentity{}, nil, fmt.Errorf("decode manifest: %w", err)
	}
	digest := sha256.Sum256(artifact)
	digestHex := hex.EncodeToString(digest[:])
	if manifest.Artifact.Filename != filepath.Base(artifactPath) || manifest.Artifact.SHA256 != digestHex || manifest.Artifact.Size != int64(len(artifact)) {
		return artifactIdentity{}, nil, errors.New("artifact bytes do not match manifest identity")
	}
	if manifest.ArtifactProfile != "base" && manifest.ArtifactProfile != "numpy-core" {
		return artifactIdentity{}, nil, errors.New("manifest artifact profile is missing or unsupported")
	}
	if manifest.Build.RepositoryCommit == "" || manifest.Target != "wasm32-wasip1" || manifest.Build.CompilerTarget != "wasm32-wasip1" || manifest.Build.ExecutionModel != "reactor" {
		return artifactIdentity{}, nil, errors.New("manifest build identity is incomplete")
	}
	return artifactIdentity{
		Filename:        manifest.Artifact.Filename,
		SHA256:          digestHex,
		Size:            int64(len(artifact)),
		SourceCommit:    manifest.Build.RepositoryCommit,
		ArtifactProfile: manifest.ArtifactProfile,
		Target:          manifest.Target,
		Execution:       manifest.Build.ExecutionModel,
	}, artifact, nil
}

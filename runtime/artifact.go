package runtime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
)

const (
	maxVerifiedArtifactBytes = 256 << 20
	maxArtifactManifestBytes = 1 << 20
)

var (
	ErrInvalidArtifactManifest = errors.New("invalid artifact manifest")
	artifactHexDigestPattern   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	artifactCommitPattern      = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

type ArtifactPackage struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Status  string `json:"status"`
}

type VerifiedArtifactIdentity struct {
	ProfileID        string
	ArtifactSHA256   string
	ManifestSHA256   string
	RepositoryCommit string
	ABIVersion       string
	Target           string
	Packages         []ArtifactPackage
}

type distributionArtifactManifest struct {
	SchemaVersion   int    `json:"schema_version"`
	ABIVersion      string `json:"abi_version"`
	ArtifactProfile string `json:"artifact_profile"`
	Target          string `json:"target"`
	Artifact        struct {
		Filename string `json:"filename"`
		Size     int64  `json:"size"`
		SHA256   string `json:"sha256"`
	} `json:"artifact"`
	Build struct {
		RepositoryCommit string `json:"repository_commit"`
		SourceDateEpoch  string `json:"source_date_epoch"`
		CompilerTarget   string `json:"compiler_target"`
		ExecutionModel   string `json:"execution_model"`
	} `json:"build"`
	Sources          json.RawMessage   `json:"sources"`
	Wasm             json.RawMessage   `json:"wasm"`
	Packages         []ArtifactPackage `json:"packages"`
	ExtensionProfile json.RawMessage   `json:"extension_profile"`
	Limitations      []string          `json:"limitations"`
}

type numpyExtensionProfile struct {
	Filename       string   `json:"filename"`
	ManifestSHA256 string   `json:"manifest_sha256"`
	Profile        string   `json:"profile"`
	Modules        []string `json:"modules"`
	LinkInputCount int      `json:"link_input_count"`
}

func VerifyDistributionArtifact(artifactFilename string, artifact, manifestBytes []byte) (VerifiedArtifactIdentity, error) {
	if len(artifact) == 0 || len(artifact) > maxVerifiedArtifactBytes || len(manifestBytes) == 0 || len(manifestBytes) > maxArtifactManifestBytes ||
		filepath.Base(artifactFilename) != artifactFilename || rejectDuplicateBoundedJSON(manifestBytes) != nil {
		return VerifiedArtifactIdentity{}, ErrInvalidArtifactManifest
	}
	decoder := json.NewDecoder(bytes.NewReader(manifestBytes))
	decoder.DisallowUnknownFields()
	var manifest distributionArtifactManifest
	if err := decoder.Decode(&manifest); err != nil {
		return VerifiedArtifactIdentity{}, ErrInvalidArtifactManifest
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return VerifiedArtifactIdentity{}, ErrInvalidArtifactManifest
	}
	if err := validateDistributionManifest(manifest, artifactFilename, artifact); err != nil {
		return VerifiedArtifactIdentity{}, fmt.Errorf("%w: %v", ErrInvalidArtifactManifest, err)
	}
	artifactSum := sha256.Sum256(artifact)
	manifestSum := sha256.Sum256(manifestBytes)
	packages := append([]ArtifactPackage(nil), manifest.Packages...)
	return VerifiedArtifactIdentity{
		ProfileID:        manifest.ArtifactProfile,
		ArtifactSHA256:   "sha256:" + hex.EncodeToString(artifactSum[:]),
		ManifestSHA256:   "sha256:" + hex.EncodeToString(manifestSum[:]),
		RepositoryCommit: manifest.Build.RepositoryCommit,
		ABIVersion:       manifest.ABIVersion,
		Target:           manifest.Target,
		Packages:         packages,
	}, nil
}

func validateDistributionManifest(manifest distributionArtifactManifest, artifactFilename string, artifact []byte) error {
	expectedFilename := map[string]string{
		"base":       "agent-python-runtime.wasm",
		"numpy-core": "agent-python-runtime-numpy-core.wasm",
	}[manifest.ArtifactProfile]
	if expectedFilename == "" || manifest.SchemaVersion != 2 || manifest.ABIVersion != "v1" || manifest.Target != "wasm32-wasip1" ||
		manifest.Artifact.Filename != expectedFilename || artifactFilename != expectedFilename || manifest.Artifact.Size != int64(len(artifact)) ||
		!artifactHexDigestPattern.MatchString(manifest.Artifact.SHA256) || !artifactCommitPattern.MatchString(manifest.Build.RepositoryCommit) ||
		manifest.Build.SourceDateEpoch == "" || manifest.Build.CompilerTarget != "wasm32-wasip1" || manifest.Build.ExecutionModel != "reactor" ||
		len(manifest.Sources) == 0 || len(manifest.Wasm) == 0 || manifest.Packages == nil || manifest.ExtensionProfile == nil || manifest.Limitations == nil {
		return errors.New("manifest identity is incomplete")
	}
	artifactSum := sha256.Sum256(artifact)
	if manifest.Artifact.SHA256 != hex.EncodeToString(artifactSum[:]) {
		return errors.New("artifact digest mismatch")
	}
	if err := validateArtifactPackages(manifest.ArtifactProfile, manifest.Packages); err != nil {
		return err
	}
	if manifest.ArtifactProfile == "base" {
		if !bytes.Equal(bytes.TrimSpace(manifest.ExtensionProfile), []byte("null")) {
			return errors.New("base profile contains extension metadata")
		}
		return nil
	}
	var extension numpyExtensionProfile
	extensionDecoder := json.NewDecoder(bytes.NewReader(manifest.ExtensionProfile))
	extensionDecoder.DisallowUnknownFields()
	if err := extensionDecoder.Decode(&extension); err != nil || !errors.Is(extensionDecoder.Decode(&struct{}{}), io.EOF) ||
		extension.Filename != "extension-selection.json" || !artifactHexDigestPattern.MatchString(extension.ManifestSHA256) || extension.Profile != "core" ||
		len(extension.Modules) != 2 || extension.Modules[0] != "numpy._core._multiarray_umath" || extension.Modules[1] != "numpy.linalg._umath_linalg" || extension.LinkInputCount <= 0 {
		return errors.New("numpy extension profile is invalid")
	}
	return nil
}

func validateArtifactPackages(profile string, packages []ArtifactPackage) error {
	expected := []struct{ name, status string }{{"cpython", "core"}}
	if profile == "numpy-core" {
		expected = append(expected, struct{ name, status string }{"numpy", "selected-core"})
	}
	if len(packages) != len(expected) {
		return errors.New("artifact package set does not match profile")
	}
	for index, item := range packages {
		if item.Name != expected[index].name || item.Status != expected[index].status || item.Version == "" || len(item.Version) > 64 {
			return errors.New("artifact package identity is invalid")
		}
	}
	return nil
}

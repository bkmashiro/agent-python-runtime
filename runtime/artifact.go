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
	"reflect"
	"regexp"
	"sort"
	"strings"
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
	ImportRoots      []string
}

type pythonImportInventory struct {
	SchemaVersion     int                  `json:"schema_version"`
	Filename          string               `json:"filename"`
	SHA256            string               `json:"sha256"`
	Probe             string               `json:"probe"`
	Implementation    string               `json:"implementation"`
	PythonVersion     string               `json:"python_version"`
	DiscoverableRoots []string             `json:"discoverable_roots"`
	Failures          []importProbeFailure `json:"failures"`
}

type importProbeFailure struct {
	Name  string `json:"name"`
	Error string `json:"error"`
}

type artifactImportInventorySidecar struct {
	SchemaVersion     int                  `json:"schema_version"`
	ArtifactProfile   string               `json:"artifact_profile"`
	Probe             string               `json:"probe"`
	Implementation    string               `json:"implementation"`
	PythonVersion     string               `json:"python_version"`
	DiscoverableRoots []string             `json:"discoverable_roots"`
	Failures          []importProbeFailure `json:"failures"`
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
	Sources               json.RawMessage        `json:"sources"`
	Wasm                  json.RawMessage        `json:"wasm"`
	Packages              []ArtifactPackage      `json:"packages"`
	ExtensionProfile      json.RawMessage        `json:"extension_profile"`
	PythonImportInventory *pythonImportInventory `json:"python_import_inventory,omitempty"`
	Limitations           []string               `json:"limitations"`
}

type numpyExtensionProfile struct {
	Filename       string   `json:"filename"`
	ManifestSHA256 string   `json:"manifest_sha256"`
	Profile        string   `json:"profile"`
	Modules        []string `json:"modules"`
	LinkInputCount int      `json:"link_input_count"`
}

func VerifyDistributionArtifact(artifactFilename string, artifact, manifestBytes, importInventoryBytes []byte) (VerifiedArtifactIdentity, error) {
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
	if err := validateDistributionManifest(manifest, artifactFilename, artifact, importInventoryBytes); err != nil {
		return VerifiedArtifactIdentity{}, fmt.Errorf("%w: %v", ErrInvalidArtifactManifest, err)
	}
	artifactSum := sha256.Sum256(artifact)
	manifestSum := sha256.Sum256(manifestBytes)
	packages := append([]ArtifactPackage(nil), manifest.Packages...)
	var importRoots []string
	if manifest.PythonImportInventory != nil {
		importRoots = append([]string(nil), manifest.PythonImportInventory.DiscoverableRoots...)
	}
	return VerifiedArtifactIdentity{
		ProfileID:        manifest.ArtifactProfile,
		ArtifactSHA256:   "sha256:" + hex.EncodeToString(artifactSum[:]),
		ManifestSHA256:   "sha256:" + hex.EncodeToString(manifestSum[:]),
		RepositoryCommit: manifest.Build.RepositoryCommit,
		ABIVersion:       manifest.ABIVersion,
		Target:           manifest.Target,
		Packages:         packages,
		ImportRoots:      importRoots,
	}, nil
}

func DistributionImportInventoryFilename(manifestBytes []byte) (string, bool, error) {
	if len(manifestBytes) == 0 || len(manifestBytes) > maxArtifactManifestBytes || rejectDuplicateBoundedJSON(manifestBytes) != nil {
		return "", false, ErrInvalidArtifactManifest
	}
	var envelope struct {
		SchemaVersion int `json:"schema_version"`
		Inventory     *struct {
			Filename string `json:"filename"`
		} `json:"python_import_inventory"`
	}
	decoder := json.NewDecoder(bytes.NewReader(manifestBytes))
	if err := decoder.Decode(&envelope); err != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		return "", false, ErrInvalidArtifactManifest
	}
	if envelope.SchemaVersion == 2 && envelope.Inventory == nil {
		return "", false, nil
	}
	if envelope.SchemaVersion != 3 || envelope.Inventory == nil || envelope.Inventory.Filename != "import-inventory.json" {
		return "", false, ErrInvalidArtifactManifest
	}
	return envelope.Inventory.Filename, true, nil
}

func validateDistributionManifest(manifest distributionArtifactManifest, artifactFilename string, artifact, importInventoryBytes []byte) error {
	expectedFilename := map[string]string{
		"base":       "agent-python-runtime.wasm",
		"numpy-core": "agent-python-runtime-numpy-core.wasm",
	}[manifest.ArtifactProfile]
	if expectedFilename == "" || (manifest.SchemaVersion != 2 && manifest.SchemaVersion != 3) || manifest.ABIVersion != "v1" || manifest.Target != "wasm32-wasip1" ||
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
	if manifest.SchemaVersion == 2 {
		if manifest.PythonImportInventory != nil || len(importInventoryBytes) != 0 {
			return errors.New("legacy manifest contains import inventory")
		}
	} else {
		if err := validatePythonImportInventory(manifest.ArtifactProfile, manifest.PythonImportInventory); err != nil {
			return err
		}
		if err := validatePythonImportInventorySidecar(manifest.ArtifactProfile, manifest.PythonImportInventory, importInventoryBytes); err != nil {
			return err
		}
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

func validatePythonImportInventory(profile string, inventory *pythonImportInventory) error {
	if inventory == nil || inventory.SchemaVersion != 1 || inventory.Filename != "import-inventory.json" ||
		!artifactHexDigestPattern.MatchString(inventory.SHA256) || inventory.Probe != "guest-importlib-find-spec-v1" ||
		inventory.Implementation != "cpython" || len(inventory.PythonVersion) == 0 || len(inventory.PythonVersion) > 256 ||
		len(inventory.DiscoverableRoots) == 0 || len(inventory.DiscoverableRoots) > 1024 || inventory.Failures == nil || len(inventory.Failures) > 1024 {
		return errors.New("Python import inventory identity is invalid")
	}
	if !sort.StringsAreSorted(inventory.DiscoverableRoots) {
		return errors.New("Python import inventory roots are not sorted")
	}
	roots := make(map[string]struct{}, len(inventory.DiscoverableRoots))
	for _, root := range inventory.DiscoverableRoots {
		if !validImportName(root) || strings.Contains(root, ".") {
			return errors.New("Python import inventory root is invalid")
		}
		if _, duplicate := roots[root]; duplicate {
			return errors.New("Python import inventory root is duplicated")
		}
		roots[root] = struct{}{}
	}
	required := []string{"agent_runtime", "json", "sys"}
	if profile == "numpy-core" {
		required = append(required, "numpy")
	}
	for _, root := range required {
		if _, ok := roots[root]; !ok {
			return errors.New("Python import inventory omits a required profile root")
		}
	}
	previous := ""
	for _, failure := range inventory.Failures {
		if !validImportName(failure.Name) || strings.Contains(failure.Name, ".") || failure.Name <= previous ||
			len(failure.Error) == 0 || len(failure.Error) > 128 {
			return errors.New("Python import inventory failure is invalid")
		}
		if _, overlaps := roots[failure.Name]; overlaps {
			return errors.New("Python import inventory root and failure overlap")
		}
		previous = failure.Name
	}
	return nil
}

func validatePythonImportInventorySidecar(profile string, inventory *pythonImportInventory, encoded []byte) error {
	if inventory == nil || len(encoded) == 0 || len(encoded) > maxArtifactManifestBytes || rejectDuplicateBoundedJSON(encoded) != nil {
		return errors.New("Python import inventory sidecar is invalid")
	}
	digest := sha256.Sum256(encoded)
	if inventory.SHA256 != hex.EncodeToString(digest[:]) {
		return errors.New("Python import inventory sidecar digest mismatch")
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var sidecar artifactImportInventorySidecar
	if err := decoder.Decode(&sidecar); err != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		return errors.New("Python import inventory sidecar is invalid")
	}
	expected := artifactImportInventorySidecar{
		SchemaVersion: inventory.SchemaVersion, ArtifactProfile: profile, Probe: inventory.Probe,
		Implementation: inventory.Implementation, PythonVersion: inventory.PythonVersion,
		DiscoverableRoots: inventory.DiscoverableRoots, Failures: inventory.Failures,
	}
	if !reflect.DeepEqual(sidecar, expected) {
		return errors.New("Python import inventory sidecar does not match manifest")
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

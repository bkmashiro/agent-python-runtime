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
	ProfileID            string
	ArtifactSHA256       string
	ManifestSHA256       string
	RepositoryCommit     string
	ABIVersion           string
	Target               string
	Packages             []ArtifactPackage
	ImportRoots          []string
	QualifiedImportRoots []string
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

type importQualificationResult struct {
	Name      string `json:"name"`
	Operation string `json:"operation"`
	Status    string `json:"status"`
	Error     string `json:"error"`
}

type pythonImportQualification struct {
	SchemaVersion  int                         `json:"schema_version"`
	Filename       string                      `json:"filename"`
	SHA256         string                      `json:"sha256"`
	Probe          string                      `json:"probe"`
	Implementation string                      `json:"implementation"`
	PythonVersion  string                      `json:"python_version"`
	QualifiedRoots []string                    `json:"qualified_roots"`
	Results        []importQualificationResult `json:"results"`
}

type artifactImportQualificationSidecar struct {
	SchemaVersion   int                         `json:"schema_version"`
	ArtifactProfile string                      `json:"artifact_profile"`
	Probe           string                      `json:"probe"`
	Implementation  string                      `json:"implementation"`
	PythonVersion   string                      `json:"python_version"`
	QualifiedRoots  []string                    `json:"qualified_roots"`
	Results         []importQualificationResult `json:"results"`
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
	Sources                   json.RawMessage            `json:"sources"`
	Wasm                      json.RawMessage            `json:"wasm"`
	Packages                  []ArtifactPackage          `json:"packages"`
	ExtensionProfile          json.RawMessage            `json:"extension_profile"`
	PythonImportInventory     *pythonImportInventory     `json:"python_import_inventory,omitempty"`
	PythonImportQualification *pythonImportQualification `json:"python_import_qualification,omitempty"`
	Limitations               []string                   `json:"limitations"`
}

type numpyExtensionProfile struct {
	Filename       string   `json:"filename"`
	ManifestSHA256 string   `json:"manifest_sha256"`
	Profile        string   `json:"profile"`
	Modules        []string `json:"modules"`
	LinkInputCount int      `json:"link_input_count"`
}

type attrsExtensionProfile struct {
	SchemaVersion int                   `json:"schema_version"`
	Kind          string                `json:"kind"`
	Profile       string                `json:"profile"`
	Package       attrsExtensionPackage `json:"package"`
}

type attrsExtensionPackage struct {
	Name                string `json:"name"`
	Version             string `json:"version"`
	Status              string `json:"status"`
	ImportRoot          string `json:"import_root"`
	InstallPath         string `json:"install_path"`
	RepositoryLicenseID string `json:"repository_license_id"`
	SourceCommit        string `json:"source_commit"`
	SourceArchiveSHA256 string `json:"source_archive_sha256"`
	PatchSHA256         string `json:"patch_sha256"`
	TreeSHA256          string `json:"tree_sha256"`
	FileCount           int    `json:"file_count"`
	TotalBytes          int    `json:"total_bytes"`
}

type distributionSource struct {
	ID               string `json:"id"`
	Version          string `json:"version"`
	URL              string `json:"url"`
	SHA256           string `json:"sha256"`
	License          string `json:"license"`
	Role             string `json:"role"`
	ArtifactRelation string `json:"artifact_relation"`
}

func VerifyDistributionArtifact(artifactFilename string, artifact, manifestBytes, importInventoryBytes, importQualificationBytes []byte) (VerifiedArtifactIdentity, error) {
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
	if err := validateDistributionManifest(manifest, artifactFilename, artifact, importInventoryBytes, importQualificationBytes); err != nil {
		return VerifiedArtifactIdentity{}, fmt.Errorf("%w: %v", ErrInvalidArtifactManifest, err)
	}
	artifactSum := sha256.Sum256(artifact)
	manifestSum := sha256.Sum256(manifestBytes)
	packages := append([]ArtifactPackage(nil), manifest.Packages...)
	var importRoots []string
	var qualifiedImportRoots []string
	if manifest.PythonImportInventory != nil {
		importRoots = append([]string(nil), manifest.PythonImportInventory.DiscoverableRoots...)
	}
	if manifest.PythonImportQualification != nil {
		qualifiedImportRoots = append([]string(nil), manifest.PythonImportQualification.QualifiedRoots...)
	}
	return VerifiedArtifactIdentity{
		ProfileID:            manifest.ArtifactProfile,
		ArtifactSHA256:       "sha256:" + hex.EncodeToString(artifactSum[:]),
		ManifestSHA256:       "sha256:" + hex.EncodeToString(manifestSum[:]),
		RepositoryCommit:     manifest.Build.RepositoryCommit,
		ABIVersion:           manifest.ABIVersion,
		Target:               manifest.Target,
		Packages:             packages,
		ImportRoots:          importRoots,
		QualifiedImportRoots: qualifiedImportRoots,
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
	if (envelope.SchemaVersion != 3 && envelope.SchemaVersion != 4) || envelope.Inventory == nil || envelope.Inventory.Filename != "import-inventory.json" {
		return "", false, ErrInvalidArtifactManifest
	}
	return envelope.Inventory.Filename, true, nil
}

func DistributionImportQualificationFilename(manifestBytes []byte) (string, bool, error) {
	if len(manifestBytes) == 0 || len(manifestBytes) > maxArtifactManifestBytes || rejectDuplicateBoundedJSON(manifestBytes) != nil {
		return "", false, ErrInvalidArtifactManifest
	}
	var envelope struct {
		SchemaVersion int `json:"schema_version"`
		Qualification *struct {
			Filename string `json:"filename"`
		} `json:"python_import_qualification"`
	}
	decoder := json.NewDecoder(bytes.NewReader(manifestBytes))
	if err := decoder.Decode(&envelope); err != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		return "", false, ErrInvalidArtifactManifest
	}
	if (envelope.SchemaVersion == 2 || envelope.SchemaVersion == 3) && envelope.Qualification == nil {
		return "", false, nil
	}
	if envelope.SchemaVersion != 4 || envelope.Qualification == nil || envelope.Qualification.Filename != "import-qualification.json" {
		return "", false, ErrInvalidArtifactManifest
	}
	return envelope.Qualification.Filename, true, nil
}

func validateDistributionManifest(manifest distributionArtifactManifest, artifactFilename string, artifact, importInventoryBytes, importQualificationBytes []byte) error {
	expectedFilename := map[string]string{
		"base":       "agent-python-runtime.wasm",
		"numpy-core": "agent-python-runtime-numpy-core.wasm",
		"attrs-770":  "agent-python-runtime-attrs-770.wasm",
	}[manifest.ArtifactProfile]
	if expectedFilename == "" || (manifest.SchemaVersion != 2 && manifest.SchemaVersion != 3 && manifest.SchemaVersion != 4) || manifest.ABIVersion != "v1" || manifest.Target != "wasm32-wasip1" ||
		manifest.Artifact.Filename != expectedFilename || artifactFilename != expectedFilename || manifest.Artifact.Size != int64(len(artifact)) ||
		!artifactHexDigestPattern.MatchString(manifest.Artifact.SHA256) || !artifactCommitPattern.MatchString(manifest.Build.RepositoryCommit) ||
		manifest.Build.SourceDateEpoch == "" || manifest.Build.CompilerTarget != "wasm32-wasip1" || manifest.Build.ExecutionModel != "reactor" ||
		len(manifest.Sources) == 0 || len(manifest.Wasm) == 0 || manifest.Packages == nil || manifest.ExtensionProfile == nil || manifest.Limitations == nil {
		return errors.New("manifest identity is incomplete")
	}
	if manifest.ArtifactProfile == "attrs-770" && manifest.SchemaVersion != 4 {
		return errors.New("attrs-770 profile requires schema v4")
	}
	artifactSum := sha256.Sum256(artifact)
	if manifest.Artifact.SHA256 != hex.EncodeToString(artifactSum[:]) {
		return errors.New("artifact digest mismatch")
	}
	if err := validateArtifactPackages(manifest.ArtifactProfile, manifest.Packages); err != nil {
		return err
	}
	if manifest.SchemaVersion == 2 {
		if manifest.PythonImportInventory != nil || manifest.PythonImportQualification != nil || len(importInventoryBytes) != 0 || len(importQualificationBytes) != 0 {
			return errors.New("legacy manifest contains Python import evidence")
		}
	} else {
		if err := validatePythonImportInventory(manifest.ArtifactProfile, manifest.PythonImportInventory); err != nil {
			return err
		}
		if err := validatePythonImportInventorySidecar(manifest.ArtifactProfile, manifest.PythonImportInventory, importInventoryBytes); err != nil {
			return err
		}
		if manifest.SchemaVersion == 3 {
			if manifest.PythonImportQualification != nil || len(importQualificationBytes) != 0 {
				return errors.New("schema-v3 manifest contains import qualification")
			}
		} else {
			if err := validatePythonImportQualification(manifest.ArtifactProfile, manifest.PythonImportInventory, manifest.PythonImportQualification); err != nil {
				return err
			}
			if err := validatePythonImportQualificationSidecar(manifest.ArtifactProfile, manifest.PythonImportQualification, importQualificationBytes); err != nil {
				return err
			}
		}
	}
	switch manifest.ArtifactProfile {
	case "base":
		if !bytes.Equal(bytes.TrimSpace(manifest.ExtensionProfile), []byte("null")) {
			return errors.New("base profile contains extension metadata")
		}
	case "numpy-core":
		var extension numpyExtensionProfile
		extensionDecoder := json.NewDecoder(bytes.NewReader(manifest.ExtensionProfile))
		extensionDecoder.DisallowUnknownFields()
		if err := extensionDecoder.Decode(&extension); err != nil || !errors.Is(extensionDecoder.Decode(&struct{}{}), io.EOF) ||
			extension.Filename != "extension-selection.json" || !artifactHexDigestPattern.MatchString(extension.ManifestSHA256) || extension.Profile != "core" ||
			len(extension.Modules) != 2 || extension.Modules[0] != "numpy._core._multiarray_umath" || extension.Modules[1] != "numpy.linalg._umath_linalg" || extension.LinkInputCount <= 0 {
			return errors.New("numpy extension profile is invalid")
		}
	case "attrs-770":
		if err := validateAttrsExtensionProfile(manifest.ExtensionProfile, manifest.Sources); err != nil {
			return err
		}
	default:
		return errors.New("artifact extension profile is unsupported")
	}
	return nil
}

func validateAttrsExtensionProfile(encoded, sourcesJSON []byte) error {
	var extension attrsExtensionProfile
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&extension); err != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		return errors.New("attrs extension profile is invalid")
	}
	packageIdentity := extension.Package
	if extension.SchemaVersion != 1 || extension.Kind != "pure-python-package" || extension.Profile != "attrs-770" ||
		packageIdentity.Name != "attrs" || packageIdentity.Version != "20.3.0-39-g58d2adc" || packageIdentity.Status != "selected-pure-python" ||
		packageIdentity.ImportRoot != "attr" || packageIdentity.InstallPath != "site-packages/attr" || packageIdentity.RepositoryLicenseID != "MIT" ||
		packageIdentity.SourceCommit != "58d2adce57f2c4e447eb12b892ebbb09cccbdcc3" ||
		packageIdentity.SourceArchiveSHA256 != "62aacc4a0014118dfedcca0f59767e21ba85aff60d3ac2c7b67caf97bda22f2b" ||
		packageIdentity.PatchSHA256 != "fdbfbdbb113809ae7982eb85e221ae5ddfdac9774a787114424e6ed2785f236e" ||
		packageIdentity.TreeSHA256 != "f1e3b25ec86f639a4ce256f5c1216fd585527142a08a284cc5fd9c9de603229f" ||
		packageIdentity.FileCount != 20 || packageIdentity.TotalBytes != 162921 {
		return errors.New("attrs extension package identity is invalid")
	}
	var sources []distributionSource
	sourceDecoder := json.NewDecoder(bytes.NewReader(sourcesJSON))
	sourceDecoder.DisallowUnknownFields()
	if err := sourceDecoder.Decode(&sources); err != nil || !errors.Is(sourceDecoder.Decode(&struct{}{}), io.EOF) {
		return errors.New("artifact sources are invalid")
	}
	matches := 0
	for _, source := range sources {
		if source.ID != "attrs-source" {
			continue
		}
		matches++
		if source.Version != packageIdentity.Version || source.URL != "https://codeload.github.com/python-attrs/attrs/tar.gz/58d2adce57f2c4e447eb12b892ebbb09cccbdcc3" ||
			source.SHA256 != packageIdentity.SourceArchiveSHA256 || source.License != packageIdentity.RepositoryLicenseID ||
			source.Role != "python-package" || source.ArtifactRelation != "packaged" {
			return errors.New("attrs extension source identity is invalid")
		}
	}
	if matches != 1 {
		return errors.New("attrs extension source is not unique")
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
	} else if profile == "attrs-770" {
		required = append(required, "attr")
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

func validatePythonImportQualification(profile string, inventory *pythonImportInventory, qualification *pythonImportQualification) error {
	if inventory == nil || qualification == nil || qualification.SchemaVersion != 1 || qualification.Filename != "import-qualification.json" ||
		!artifactHexDigestPattern.MatchString(qualification.SHA256) || qualification.Probe != "guest-import-exec-v1" ||
		qualification.Implementation != inventory.Implementation || qualification.PythonVersion != inventory.PythonVersion ||
		len(qualification.QualifiedRoots) == 0 || len(qualification.QualifiedRoots) > 64 ||
		len(qualification.Results) == 0 || len(qualification.Results) > 64 {
		return errors.New("Python import qualification identity is invalid")
	}
	discoverable := make(map[string]struct{}, len(inventory.DiscoverableRoots))
	for _, root := range inventory.DiscoverableRoots {
		discoverable[root] = struct{}{}
	}
	qualified := make(map[string]struct{}, len(qualification.QualifiedRoots))
	previous := ""
	for _, root := range qualification.QualifiedRoots {
		if !validImportName(root) || strings.Contains(root, ".") || root <= previous {
			return errors.New("Python import qualification roots are invalid")
		}
		if _, ok := discoverable[root]; !ok {
			return errors.New("Python import qualification root is not discoverable")
		}
		qualified[root] = struct{}{}
		previous = root
	}
	required := []string{"agent_runtime", "json", "sys"}
	if profile == "numpy-core" {
		required = append(required, "numpy")
	} else if profile == "attrs-770" {
		required = append(required, "attr", "types", "typing")
	}
	for _, root := range required {
		if _, ok := qualified[root]; !ok {
			return errors.New("Python import qualification omits a required profile root")
		}
	}
	derived := make([]string, 0, len(qualification.Results))
	expectedOperations := map[string]string{
		"agent_runtime": "import", "attr": "generic_dynamic_class", "base64": "roundtrip", "collections": "counter", "csv": "roundtrip",
		"datetime": "date_isoformat", "decimal": "add", "fractions": "add", "functools": "reduce",
		"hashlib": "sha256", "itertools": "islice", "json": "roundtrip", "math": "sqrt",
		"pathlib": "pure_path", "re": "fullmatch", "statistics": "mean", "sys": "version_info",
		"types": "new_class", "typing": "generic_alias", "urllib": "parse", "xml": "etree_roundtrip",
	}
	if profile == "numpy-core" {
		expectedOperations["numpy"] = "array_sum"
	}
	previous = ""
	for _, result := range qualification.Results {
		if !validImportName(result.Name) || strings.Contains(result.Name, ".") || result.Name <= previous ||
			expectedOperations[result.Name] != result.Operation {
			return errors.New("Python import qualification result is invalid")
		}
		if _, ok := discoverable[result.Name]; !ok {
			return errors.New("Python import qualification result is not discoverable")
		}
		switch result.Status {
		case "qualified":
			if result.Error != "" {
				return errors.New("qualified Python import contains an error")
			}
			derived = append(derived, result.Name)
		case "import_failed", "operation_failed":
			if len(result.Error) == 0 || len(result.Error) > 128 {
				return errors.New("failed Python import qualification omits its error class")
			}
		default:
			return errors.New("Python import qualification status is invalid")
		}
		previous = result.Name
	}
	if !reflect.DeepEqual(derived, qualification.QualifiedRoots) {
		return errors.New("Python import qualification roots do not match results")
	}
	return nil
}

func validatePythonImportQualificationSidecar(profile string, qualification *pythonImportQualification, encoded []byte) error {
	if qualification == nil || len(encoded) == 0 || len(encoded) > maxArtifactManifestBytes || rejectDuplicateBoundedJSON(encoded) != nil {
		return errors.New("Python import qualification sidecar is invalid")
	}
	digest := sha256.Sum256(encoded)
	if qualification.SHA256 != hex.EncodeToString(digest[:]) {
		return errors.New("Python import qualification sidecar digest mismatch")
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var sidecar artifactImportQualificationSidecar
	if err := decoder.Decode(&sidecar); err != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		return errors.New("Python import qualification sidecar is invalid")
	}
	expected := artifactImportQualificationSidecar{
		SchemaVersion: qualification.SchemaVersion, ArtifactProfile: profile, Probe: qualification.Probe,
		Implementation: qualification.Implementation, PythonVersion: qualification.PythonVersion,
		QualifiedRoots: qualification.QualifiedRoots, Results: qualification.Results,
	}
	if !reflect.DeepEqual(sidecar, expected) {
		return errors.New("Python import qualification sidecar does not match manifest")
	}
	return nil
}

func validateArtifactPackages(profile string, packages []ArtifactPackage) error {
	expected := []struct{ name, status string }{{"cpython", "core"}}
	if profile == "numpy-core" {
		expected = append(expected, struct{ name, status string }{"numpy", "selected-core"})
	} else if profile == "attrs-770" {
		expected = append(expected, struct{ name, status string }{"attrs", "selected-pure-python"})
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

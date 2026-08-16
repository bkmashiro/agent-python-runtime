package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestVerifyDistributionArtifactBindsProfilePackagesAndDigests(t *testing.T) {
	artifact := []byte("verified-wasm")
	manifest := distributionManifestFixture(t, artifact, "base")
	inventory := distributionImportInventoryFixture(t, "base")
	qualification := distributionImportQualificationFixture(t, "base")
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := VerifyDistributionArtifact("agent-python-runtime.wasm", artifact, encoded, inventory, qualification)
	if err != nil {
		t.Fatal(err)
	}
	artifactSum := sha256.Sum256(artifact)
	manifestSum := sha256.Sum256(encoded)
	if identity.ProfileID != "base" || identity.ArtifactSHA256 != "sha256:"+hex.EncodeToString(artifactSum[:]) || identity.ManifestSHA256 != "sha256:"+hex.EncodeToString(manifestSum[:]) {
		t.Fatalf("identity=%+v", identity)
	}
	if len(identity.Packages) != 1 || identity.Packages[0].Name != "cpython" || identity.Packages[0].Status != "core" {
		t.Fatalf("packages=%+v", identity.Packages)
	}
	if strings.Join(identity.ImportRoots, ",") != "agent_runtime,json,math,sys" {
		t.Fatalf("imports=%v", identity.ImportRoots)
	}
	if strings.Join(identity.QualifiedImportRoots, ",") != "agent_runtime,json,sys" {
		t.Fatalf("qualified imports=%v", identity.QualifiedImportRoots)
	}
}

func TestVerifyDistributionArtifactBindsAttrsPackageProfile(t *testing.T) {
	artifact := []byte("verified-wasm")
	manifest := distributionManifestFixture(t, artifact, "attrs-770")
	inventory := distributionImportInventoryFixture(t, "attrs-770")
	qualification := distributionImportQualificationFixture(t, "attrs-770")
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := VerifyDistributionArtifact("agent-python-runtime-attrs-770.wasm", artifact, encoded, inventory, qualification)
	if err != nil {
		t.Fatal(err)
	}
	if identity.ProfileID != "attrs-770" || len(identity.Packages) != 2 || identity.Packages[1].Name != "attrs" || identity.Packages[1].Status != "selected-pure-python" ||
		strings.Join(identity.QualifiedImportRoots, ",") != "agent_runtime,attr,json,sys,types,typing" {
		t.Fatalf("identity=%+v", identity)
	}
	manifest["extension_profile"].(map[string]any)["package"].(map[string]any)["patch_sha256"] = strings.Repeat("0", 64)
	mutated, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyDistributionArtifact("agent-python-runtime-attrs-770.wasm", artifact, mutated, inventory, qualification); !errors.Is(err, ErrInvalidArtifactManifest) {
		t.Fatalf("patch drift err=%v", err)
	}
}

func TestVerifyDistributionArtifactFailsClosed(t *testing.T) {
	artifact := []byte("verified-wasm")
	valid := distributionManifestFixture(t, artifact, "numpy-core")
	inventory := distributionImportInventoryFixture(t, "numpy-core")
	qualification := distributionImportQualificationFixture(t, "numpy-core")
	cases := map[string]func(map[string]any){
		"unknown profile":        func(value map[string]any) { value["artifact_profile"] = "everything" },
		"profile filename drift": func(value map[string]any) { value["artifact_profile"] = "base" },
		"artifact digest drift":  func(value map[string]any) { value["artifact"].(map[string]any)["sha256"] = strings.Repeat("0", 64) },
		"package set drift": func(value map[string]any) {
			value["packages"] = []any{map[string]any{"name": "cpython", "version": "3.14.0", "status": "core"}}
		},
		"extension profile missing": func(value map[string]any) { value["extension_profile"] = nil },
		"import inventory drift": func(value map[string]any) {
			value["python_import_inventory"].(map[string]any)["discoverable_roots"] = []any{"numpy", "json"}
		},
		"qualification drift": func(value map[string]any) {
			value["python_import_qualification"].(map[string]any)["qualified_roots"] = []any{"agent_runtime", "numpy", "sys"}
		},
		"unknown top-level field": func(value map[string]any) { value["authority"] = "guest" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			clone := cloneManifestFixture(t, valid)
			mutate(clone)
			encoded, err := json.Marshal(clone)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := VerifyDistributionArtifact("agent-python-runtime-numpy-core.wasm", artifact, encoded, inventory, qualification); !errors.Is(err, ErrInvalidArtifactManifest) {
				t.Fatalf("err=%v", err)
			}
		})
	}
	encodedValid, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	mutatedInventory := append([]byte(nil), inventory...)
	mutatedInventory[len(mutatedInventory)-1] = ' '
	if _, err := VerifyDistributionArtifact("agent-python-runtime-numpy-core.wasm", artifact, encodedValid, mutatedInventory, qualification); !errors.Is(err, ErrInvalidArtifactManifest) {
		t.Fatalf("inventory sidecar drift err=%v", err)
	}
	mutatedQualification := append([]byte(nil), qualification...)
	mutatedQualification[len(mutatedQualification)-1] = ' '
	if _, err := VerifyDistributionArtifact("agent-python-runtime-numpy-core.wasm", artifact, encodedValid, inventory, mutatedQualification); !errors.Is(err, ErrInvalidArtifactManifest) {
		t.Fatalf("qualification sidecar drift err=%v", err)
	}
	var invalidQualification artifactImportQualificationSidecar
	if err := json.Unmarshal(qualification, &invalidQualification); err != nil {
		t.Fatal(err)
	}
	invalidQualification.Results[1].Operation = "arbitrary_operation"
	invalidQualificationBytes, err := json.Marshal(invalidQualification)
	if err != nil {
		t.Fatal(err)
	}
	invalidManifest := distributionManifestFixture(t, artifact, "numpy-core")
	invalidManifest["python_import_qualification"] = distributionImportQualificationRecordFixture(t, invalidQualificationBytes)
	invalidManifestBytes, err := json.Marshal(invalidManifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyDistributionArtifact("agent-python-runtime-numpy-core.wasm", artifact, invalidManifestBytes, inventory, invalidQualificationBytes); !errors.Is(err, ErrInvalidArtifactManifest) {
		t.Fatalf("unrecognized qualification operation err=%v", err)
	}
	duplicate := []byte(`{"schema_version":4,"schema_version":4}`)
	if _, err := VerifyDistributionArtifact("agent-python-runtime-numpy-core.wasm", artifact, duplicate, nil, nil); !errors.Is(err, ErrInvalidArtifactManifest) {
		t.Fatalf("duplicate err=%v", err)
	}
}

func TestVerifyLegacyArtifactIdentityCannotBindProfile(t *testing.T) {
	artifact := []byte("verified-wasm")
	manifest := distributionManifestFixture(t, artifact, "base")
	manifest["schema_version"] = 3
	delete(manifest, "python_import_qualification")
	inventory := distributionImportInventoryFixture(t, "base")
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := VerifyDistributionArtifact("agent-python-runtime.wasm", artifact, encoded, inventory, nil)
	if err != nil || len(identity.ImportRoots) == 0 || len(identity.QualifiedImportRoots) != 0 {
		t.Fatalf("schema-v3 identity=%+v err=%v", identity, err)
	}
	profile, err := NewExecutionProfile("base", []string{"json"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := profile.BindVerifiedArtifact(identity); !errors.Is(err, ErrExecutionProfileImportUnavailable) {
		t.Fatalf("schema-v3 bind err=%v", err)
	}

	manifest["schema_version"] = 2
	delete(manifest, "python_import_inventory")
	encoded, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	identity, err = VerifyDistributionArtifact("agent-python-runtime.wasm", artifact, encoded, nil, nil)
	if err != nil || len(identity.ImportRoots) != 0 || len(identity.QualifiedImportRoots) != 0 {
		t.Fatalf("schema-v2 identity=%+v err=%v", identity, err)
	}
	if _, err := profile.BindVerifiedArtifact(identity); !errors.Is(err, ErrExecutionProfileImportUnavailable) {
		t.Fatalf("schema-v2 bind err=%v", err)
	}
}

func TestBindExecutionProfileToVerifiedArtifact(t *testing.T) {
	profile, err := NewExecutionProfile("base", []string{"csv", "json"})
	if err != nil {
		t.Fatal(err)
	}
	identity := VerifiedArtifactIdentity{
		ProfileID:            "base",
		ArtifactSHA256:       "sha256:" + strings.Repeat("1", 64),
		ManifestSHA256:       "sha256:" + strings.Repeat("2", 64),
		Packages:             []ArtifactPackage{{Name: "cpython", Version: "3.14.0", Status: "core"}},
		ImportRoots:          []string{"agent_runtime", "csv", "json", "sys"},
		QualifiedImportRoots: []string{"agent_runtime", "csv", "json", "sys"},
	}
	bound, err := profile.BindVerifiedArtifact(identity)
	if err != nil {
		t.Fatal(err)
	}
	if profile.ArtifactSHA256() != "" || bound.ArtifactSHA256() != identity.ArtifactSHA256 || bound.ManifestSHA256() != identity.ManifestSHA256 {
		t.Fatalf("original=%q bound=%q/%q", profile.ArtifactSHA256(), bound.ArtifactSHA256(), bound.ManifestSHA256())
	}
	if strings.Join(bound.QualifiedImports(), ",") != "agent_runtime,csv,json,sys" {
		t.Fatalf("qualified=%v", bound.QualifiedImports())
	}
	wrong := identity
	wrong.ProfileID = "other"
	if _, err := profile.BindVerifiedArtifact(wrong); !errors.Is(err, ErrExecutionProfileArtifactMismatch) {
		t.Fatalf("err=%v", err)
	}
	missingImport := identity
	missingImport.QualifiedImportRoots = []string{"agent_runtime", "json", "sys"}
	if _, err := profile.BindVerifiedArtifact(missingImport); !errors.Is(err, ErrExecutionProfileImportUnavailable) {
		t.Fatalf("missing qualified import err=%v", err)
	}
	undiscoverable := identity
	undiscoverable.QualifiedImportRoots = append(undiscoverable.QualifiedImportRoots, "statistics")
	if _, err := profile.BindVerifiedArtifact(undiscoverable); !errors.Is(err, ErrExecutionProfileArtifactMismatch) {
		t.Fatalf("undiscoverable qualification err=%v", err)
	}
	legacy := identity
	legacy.QualifiedImportRoots = nil
	if _, err := profile.BindVerifiedArtifact(legacy); !errors.Is(err, ErrExecutionProfileImportUnavailable) {
		t.Fatalf("legacy err=%v", err)
	}
}

func distributionImportQualificationRecordFixture(t *testing.T, encoded []byte) map[string]any {
	t.Helper()
	var record map[string]any
	if err := json.Unmarshal(encoded, &record); err != nil {
		t.Fatal(err)
	}
	delete(record, "artifact_profile")
	digest := sha256.Sum256(encoded)
	record["filename"] = "import-qualification.json"
	record["sha256"] = hex.EncodeToString(digest[:])
	return record
}

func distributionManifestFixture(t *testing.T, artifact []byte, profile string) map[string]any {
	t.Helper()
	artifactSum := sha256.Sum256(artifact)
	inventorySum := sha256.Sum256(distributionImportInventoryFixture(t, profile))
	qualificationSum := sha256.Sum256(distributionImportQualificationFixture(t, profile))
	filename := "agent-python-runtime.wasm"
	packages := []any{map[string]any{"name": "cpython", "version": "3.14.0", "status": "core"}}
	var extension any
	if profile == "numpy-core" {
		filename = "agent-python-runtime-numpy-core.wasm"
		packages = append(packages, map[string]any{"name": "numpy", "version": "2.3.0", "status": "selected-core"})
		extension = map[string]any{"filename": "extension-selection.json", "manifest_sha256": strings.Repeat("3", 64), "profile": "core", "modules": []any{"numpy._core._multiarray_umath", "numpy.linalg._umath_linalg"}, "link_input_count": 2}
	} else if profile == "attrs-770" {
		filename = "agent-python-runtime-attrs-770.wasm"
		packages = append(packages, map[string]any{"name": "attrs", "version": "20.3.0-39-g58d2adc", "status": "selected-pure-python"})
		extension = map[string]any{
			"schema_version": 1, "kind": "pure-python-package", "profile": "attrs-770",
			"package": map[string]any{
				"name": "attrs", "version": "20.3.0-39-g58d2adc", "status": "selected-pure-python",
				"import_root": "attr", "install_path": "site-packages/attr", "repository_license_id": "MIT",
				"source_commit":         "58d2adce57f2c4e447eb12b892ebbb09cccbdcc3",
				"source_archive_sha256": "62aacc4a0014118dfedcca0f59767e21ba85aff60d3ac2c7b67caf97bda22f2b", "patch_sha256": "fdbfbdbb113809ae7982eb85e221ae5ddfdac9774a787114424e6ed2785f236e",
				"tree_sha256": "f1e3b25ec86f639a4ce256f5c1216fd585527142a08a284cc5fd9c9de603229f", "file_count": 20, "total_bytes": 162921,
			},
		}
	}
	imports := []any{"agent_runtime", "json", "math", "sys"}
	if profile == "numpy-core" {
		imports = []any{"agent_runtime", "json", "math", "numpy", "sys"}
	} else if profile == "attrs-770" {
		imports = []any{"agent_runtime", "attr", "json", "math", "sys", "types", "typing"}
	}
	return map[string]any{
		"schema_version":    4,
		"abi_version":       "v1",
		"artifact_profile":  profile,
		"target":            "wasm32-wasip1",
		"artifact":          map[string]any{"filename": filename, "size": len(artifact), "sha256": hex.EncodeToString(artifactSum[:])},
		"build":             map[string]any{"repository_commit": strings.Repeat("a", 40), "source_date_epoch": "1", "compiler_target": "wasm32-wasip1", "execution_model": "reactor"},
		"sources":           distributionSources(profile),
		"wasm":              map[string]any{"imports": []any{}, "exports": []any{"_start"}},
		"packages":          packages,
		"extension_profile": extension,
		"python_import_inventory": map[string]any{
			"schema_version": 1, "filename": "import-inventory.json", "sha256": hex.EncodeToString(inventorySum[:]),
			"probe": "guest-importlib-find-spec-v1", "implementation": "cpython", "python_version": "3.14.0",
			"discoverable_roots": imports, "failures": []any{},
		},
		"python_import_qualification": map[string]any{
			"schema_version": 1, "filename": "import-qualification.json", "sha256": hex.EncodeToString(qualificationSum[:]),
			"probe": "guest-import-exec-v1", "implementation": "cpython", "python_version": "3.14.0",
			"qualified_roots": qualificationRoots(profile), "results": qualificationResults(profile),
		},
		"limitations": []any{"bounded"},
	}
}

func qualificationRoots(profile string) []string {
	roots := []string{"agent_runtime", "json", "sys"}
	if profile == "numpy-core" {
		roots = []string{"agent_runtime", "json", "numpy", "sys"}
	} else if profile == "attrs-770" {
		roots = []string{"agent_runtime", "attr", "json", "sys", "types", "typing"}
	}
	return roots
}

func qualificationResults(profile string) []any {
	operations := map[string]string{"agent_runtime": "import", "attr": "generic_dynamic_class", "json": "roundtrip", "numpy": "array_sum", "sys": "version_info", "types": "new_class", "typing": "generic_alias"}
	results := make([]any, 0, len(qualificationRoots(profile)))
	for _, root := range qualificationRoots(profile) {
		results = append(results, map[string]any{"name": root, "operation": operations[root], "status": "qualified", "error": ""})
	}
	return results
}

func distributionImportInventoryFixture(t *testing.T, profile string) []byte {
	t.Helper()
	roots := []string{"agent_runtime", "json", "math", "sys"}
	if profile == "numpy-core" {
		roots = []string{"agent_runtime", "json", "math", "numpy", "sys"}
	} else if profile == "attrs-770" {
		roots = []string{"agent_runtime", "attr", "json", "math", "sys", "types", "typing"}
	}
	encoded, err := json.Marshal(map[string]any{
		"schema_version": 1, "artifact_profile": profile, "probe": "guest-importlib-find-spec-v1",
		"implementation": "cpython", "python_version": "3.14.0", "discoverable_roots": roots, "failures": []any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func distributionImportQualificationFixture(t *testing.T, profile string) []byte {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{
		"schema_version": 1, "artifact_profile": profile, "probe": "guest-import-exec-v1",
		"implementation": "cpython", "python_version": "3.14.0", "qualified_roots": qualificationRoots(profile),
		"results": qualificationResults(profile),
	})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func distributionSources(profile string) []any {
	if profile != "attrs-770" {
		return []any{}
	}
	return []any{map[string]any{
		"id": "attrs-source", "version": "20.3.0-39-g58d2adc",
		"url":     "https://codeload.github.com/python-attrs/attrs/tar.gz/58d2adce57f2c4e447eb12b892ebbb09cccbdcc3",
		"sha256":  "62aacc4a0014118dfedcca0f59767e21ba85aff60d3ac2c7b67caf97bda22f2b",
		"license": "MIT", "role": "python-package", "artifact_relation": "packaged",
	}}
}

func cloneManifestFixture(t *testing.T, value map[string]any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var clone map[string]any
	if err := json.Unmarshal(encoded, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

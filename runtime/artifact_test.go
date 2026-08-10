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
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := VerifyDistributionArtifact("agent-python-runtime.wasm", artifact, encoded)
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
}

func TestVerifyDistributionArtifactFailsClosed(t *testing.T) {
	artifact := []byte("verified-wasm")
	valid := distributionManifestFixture(t, artifact, "numpy-core")
	cases := map[string]func(map[string]any){
		"unknown profile":        func(value map[string]any) { value["artifact_profile"] = "everything" },
		"profile filename drift": func(value map[string]any) { value["artifact_profile"] = "base" },
		"artifact digest drift":  func(value map[string]any) { value["artifact"].(map[string]any)["sha256"] = strings.Repeat("0", 64) },
		"package set drift": func(value map[string]any) {
			value["packages"] = []any{map[string]any{"name": "cpython", "version": "3.14.0", "status": "core"}}
		},
		"extension profile missing": func(value map[string]any) { value["extension_profile"] = nil },
		"unknown top-level field":   func(value map[string]any) { value["authority"] = "guest" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			clone := cloneManifestFixture(t, valid)
			mutate(clone)
			encoded, err := json.Marshal(clone)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := VerifyDistributionArtifact("agent-python-runtime-numpy-core.wasm", artifact, encoded); !errors.Is(err, ErrInvalidArtifactManifest) {
				t.Fatalf("err=%v", err)
			}
		})
	}
	duplicate := []byte(`{"schema_version":2,"schema_version":2}`)
	if _, err := VerifyDistributionArtifact("agent-python-runtime-numpy-core.wasm", artifact, duplicate); !errors.Is(err, ErrInvalidArtifactManifest) {
		t.Fatalf("duplicate err=%v", err)
	}
}

func TestBindExecutionProfileToVerifiedArtifact(t *testing.T) {
	profile, err := NewExecutionProfile("numpy-core", []string{"json", "numpy"})
	if err != nil {
		t.Fatal(err)
	}
	identity := VerifiedArtifactIdentity{
		ProfileID:      "numpy-core",
		ArtifactSHA256: "sha256:" + strings.Repeat("1", 64),
		ManifestSHA256: "sha256:" + strings.Repeat("2", 64),
		Packages:       []ArtifactPackage{{Name: "cpython", Version: "3.14.0", Status: "core"}, {Name: "numpy", Version: "2.3.0", Status: "selected-core"}},
	}
	bound, err := profile.BindVerifiedArtifact(identity)
	if err != nil {
		t.Fatal(err)
	}
	if profile.ArtifactSHA256() != "" || bound.ArtifactSHA256() != identity.ArtifactSHA256 || bound.ManifestSHA256() != identity.ManifestSHA256 {
		t.Fatalf("original=%q bound=%q/%q", profile.ArtifactSHA256(), bound.ArtifactSHA256(), bound.ManifestSHA256())
	}
	wrong := identity
	wrong.ProfileID = "base"
	if _, err := profile.BindVerifiedArtifact(wrong); !errors.Is(err, ErrExecutionProfileArtifactMismatch) {
		t.Fatalf("err=%v", err)
	}
}

func distributionManifestFixture(t *testing.T, artifact []byte, profile string) map[string]any {
	t.Helper()
	artifactSum := sha256.Sum256(artifact)
	filename := "agent-python-runtime.wasm"
	packages := []any{map[string]any{"name": "cpython", "version": "3.14.0", "status": "core"}}
	var extension any
	if profile == "numpy-core" {
		filename = "agent-python-runtime-numpy-core.wasm"
		packages = append(packages, map[string]any{"name": "numpy", "version": "2.3.0", "status": "selected-core"})
		extension = map[string]any{"filename": "numpy-core-selection.json", "manifest_sha256": strings.Repeat("3", 64), "profile": "core", "modules": []any{"numpy._core._multiarray_umath", "numpy.linalg._umath_linalg"}, "link_input_count": 2}
	}
	return map[string]any{
		"schema_version":    2,
		"abi_version":       "v1",
		"artifact_profile":  profile,
		"target":            "wasm32-wasip1",
		"artifact":          map[string]any{"filename": filename, "size": len(artifact), "sha256": hex.EncodeToString(artifactSum[:])},
		"build":             map[string]any{"repository_commit": strings.Repeat("a", 40), "source_date_epoch": "1", "compiler_target": "wasm32-wasip1", "execution_model": "reactor"},
		"sources":           []any{},
		"wasm":              map[string]any{"imports": []any{}, "exports": []any{"_start"}},
		"packages":          packages,
		"extension_profile": extension,
		"limitations":       []any{"bounded"},
	}
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

package hermesbridge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writePinnedFixture(t *testing.T) (string, string, []byte) {
	t.Helper()
	root := t.TempDir()
	artifact := filepath.Join(root, "agent-python-runtime.wasm")
	wasm := []byte("\x00asm\x01\x00\x00\x00fixture")
	if err := os.WriteFile(artifact, wasm, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(wasm)
	manifestDocument := map[string]any{
		"schema_version": 2, "abi_version": "v1", "target": "wasm32-wasip1",
		"artifact": map[string]any{"filename": filepath.Base(artifact), "sha256": hex.EncodeToString(sum[:]), "size": len(wasm)},
		"build":    map[string]any{"repository_commit": "7f3070cc155373791010f4de53e9e2b9f7ae3060"},
	}
	manifestBytes, err := json.Marshal(manifestDocument)
	if err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(root, "manifest.json")
	if err := os.WriteFile(manifest, manifestBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return artifact, manifest, wasm
}

func TestLoadPinnedArtifactVerifiesManifestIdentity(t *testing.T) {
	artifact, manifest, want := writePinnedFixture(t)
	got, provenance, err := LoadPinnedArtifact(artifact, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) || provenance.ArtifactSHA256 != digestBytes(want) ||
		provenance.ManifestSHA256 == "" || provenance.RepositoryCommit != "7f3070cc155373791010f4de53e9e2b9f7ae3060" {
		t.Fatalf("unexpected artifact/provenance: %q %#v", got, provenance)
	}
}

func TestLoadPinnedArtifactRejectsDriftAndUnsafePaths(t *testing.T) {
	for name, mutate := range map[string]func(*testing.T, string, string) (string, string){
		"artifact drift": func(t *testing.T, artifact, manifest string) (string, string) {
			if err := os.WriteFile(artifact, []byte("\x00asm\x01\x00\x00\x00changed"), 0o600); err != nil {
				t.Fatal(err)
			}
			return artifact, manifest
		},
		"artifact symlink": func(t *testing.T, artifact, manifest string) (string, string) {
			link := filepath.Join(filepath.Dir(artifact), "guest-link.wasm")
			if err := os.Symlink(artifact, link); err != nil {
				t.Fatal(err)
			}
			return link, manifest
		},
		"manifest symlink": func(t *testing.T, artifact, manifest string) (string, string) {
			link := filepath.Join(filepath.Dir(manifest), "manifest-link.json")
			if err := os.Symlink(manifest, link); err != nil {
				t.Fatal(err)
			}
			return artifact, link
		},
		"relative path": func(t *testing.T, artifact, manifest string) (string, string) {
			return filepath.Base(artifact), manifest
		},
	} {
		t.Run(name, func(t *testing.T) {
			artifact, manifest, _ := writePinnedFixture(t)
			artifact, manifest = mutate(t, artifact, manifest)
			if _, _, err := LoadPinnedArtifact(artifact, manifest); err == nil {
				t.Fatal("unsafe or drifted artifact was accepted")
			}
		})
	}
}

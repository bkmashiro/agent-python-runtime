package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestVerifyArtifactManifestBindsRegularArtifactAndCommit(t *testing.T) {
	directory := t.TempDir()
	artifact := []byte("guest")
	artifactPath := filepath.Join(directory, "agent-python-runtime.wasm")
	if err := os.WriteFile(artifactPath, artifact, 0o600); err != nil {
		t.Fatal(err)
	}
	commit := "0123456789abcdef0123456789abcdef01234567"
	manifest := map[string]any{
		"artifact": map[string]any{"filename": filepath.Base(artifactPath), "sha256": sha(artifact)[7:]},
		"build":    map[string]any{"repository_commit": commit},
	}
	raw, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(directory, "manifest.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyArtifactManifest(artifactPath, commit, artifact); err != nil {
		t.Fatal(err)
	}
	if err := verifyArtifactManifest(artifactPath, "abcdef0123456789abcdef0123456789abcdef01", artifact); err == nil {
		t.Fatal("commit drift admitted")
	}
	linkPath := filepath.Join(directory, "linked.wasm")
	if err := os.Symlink(artifactPath, linkPath); err != nil {
		t.Fatal(err)
	}
	manifest["artifact"].(map[string]any)["filename"] = filepath.Base(linkPath)
	raw, _ = json.Marshal(manifest)
	_ = os.WriteFile(filepath.Join(directory, "manifest.json"), raw, 0o600)
	if err := verifyArtifactManifest(linkPath, commit, artifact); err == nil {
		t.Fatal("artifact symlink admitted")
	}
}

func TestAtomicWriteReplacesCompleteFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "evidence.json")
	if err := atomicWrite(path, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(path, []byte("second")); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != "second" {
		t.Fatalf("content=%q err=%v", content, err)
	}
}

func TestStableGuestResultExcludesInvocationMetadata(t *testing.T) {
	first, err := stableGuestResult([]byte(`{"status":"ok","result":{"value":2},"error":null,"receipt":"one"}`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := stableGuestResult([]byte(`{"receipt":"two","error":null,"result":{"value":2},"status":"ok"}`))
	if err != nil || string(first) != string(second) || string(first) != `{"value":2}` {
		t.Fatalf("first=%s second=%s err=%v", first, second, err)
	}
	if _, err := stableGuestResult([]byte(`{"status":"error","result":null,"error":{"class":"failure"}}`)); err == nil {
		t.Fatal("failed Guest response admitted")
	}
}

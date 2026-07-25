package agentic

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func datasetRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test path")
	}
	return filepath.Join(filepath.Dir(file), "v1")
}

func TestLoadPinnedBFCLSubset(t *testing.T) {
	dataset, err := Load(datasetRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(dataset.Tasks) != 20 {
		t.Fatalf("tasks=%d want=20", len(dataset.Tasks))
	}
	counts := map[string]int{}
	tracks := map[string]int{}
	for _, task := range dataset.Tasks {
		counts[task.Split]++
		tracks[task.Track]++
		if task.Source.Benchmark != "BFCL" || task.Source.Version != "v4" {
			t.Fatalf("unexpected source: %+v", task.Source)
		}
		if task.Source.Revision != dataset.Manifest.Sources[0].Revision {
			t.Fatalf("task revision drift: %s", task.ID)
		}
		if !task.Safety.NetworkDisabled || task.Safety.RealWorldEffects || task.Safety.Credentials != "none" {
			t.Fatalf("unsafe task: %s", task.ID)
		}
	}
	if counts["dev"] != 10 || counts["evaluation"] != 10 {
		t.Fatalf("split counts=%v", counts)
	}
	if tracks["stateless_function_calling"] != 10 || tracks["stateful_local_tools"] != 10 {
		t.Fatalf("track counts=%v", tracks)
	}
}

func TestLoadRejectsTaskDigestTamper(t *testing.T) {
	root := copyDataset(t)
	manifest, err := LoadManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, filepath.FromSlash(manifest.Tasks[0].Path))
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root); err == nil {
		t.Fatal("expected digest mismatch")
	}
}

func TestLoadRejectsTaskSymlink(t *testing.T) {
	root := copyDataset(t)
	manifest, err := LoadManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, filepath.FromSlash(manifest.Tasks[0].Path))
	target := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(target, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := Load(root); err == nil {
		t.Fatal("expected symlink rejection")
	}
}

func TestLoadRejectsExternalToolSchemaReference(t *testing.T) {
	root := copyDataset(t)
	manifest, err := LoadManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	entry := &manifest.Tasks[0]
	path := filepath.Join(root, filepath.FromSlash(entry.Path))
	var task map[string]any
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &task) != nil {
		t.Fatal("load task")
	}
	tools := task["tools"].([]any)
	tool := tools[0].(map[string]any)
	tool["parameters"] = map[string]any{
		"type":       "object",
		"properties": map[string]any{"value": map[string]any{"$ref": "https://attacker.invalid/schema.json"}},
	}
	data, err = json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	entry.SHA256 = digest(data)
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), manifestData, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root); err == nil {
		t.Fatal("expected external schema reference rejection")
	}
}

func copyDataset(t *testing.T) string {
	t.Helper()
	src := datasetRoot(t)
	dst := filepath.Join(t.TempDir(), "dataset")
	if err := filepath.WalkDir(src, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(out, 0o700)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(out, data, 0o600)
	}); err != nil {
		t.Fatal(err)
	}
	return dst
}

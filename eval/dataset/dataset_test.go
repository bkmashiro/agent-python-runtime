package dataset_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/eval/dataset"
)

func datasetRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "v1")
}

func TestDatasetLoadsBothSplitsWithEveryFamilyAndHidesOracleFromModelView(t *testing.T) {
	value, err := dataset.Load(datasetRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(value.IDs("dev")) != 10 || len(value.IDs("evaluation")) != 10 || len(value.Scenarios) != 20 {
		t.Fatalf("unexpected split sizes dev=%d eval=%d all=%d", len(value.IDs("dev")), len(value.IDs("evaluation")), len(value.Scenarios))
	}
	view, err := value.ModelView(value.IDs("evaluation")[0])
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(view)
	if strings.Contains(string(encoded), "oracle") || strings.Contains(string(encoded), "expected_result") || strings.Contains(string(encoded), "initial_state_digest") {
		t.Fatalf("model view leaked oracle or fixture state: %s", encoded)
	}
}

func TestDatasetRejectsScenarioSymlinkEscape(t *testing.T) {
	root := datasetRoot(t)
	copyRoot := t.TempDir()
	if err := os.CopyFS(copyRoot, os.DirFS(root)); err != nil {
		t.Fatal(err)
	}
	relative := "scenarios/dev/dev_simple_read_001.json"
	target := filepath.Join(copyRoot, relative)
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, relative), target); err != nil {
		t.Fatal(err)
	}
	if _, err := dataset.Load(copyRoot); !errors.Is(err, dataset.ErrInvalidDataset) {
		t.Fatalf("symlink escape accepted: %v", err)
	}
}

func TestDatasetRejectsScenarioDrift(t *testing.T) {
	source := datasetRoot(t)
	target := t.TempDir()
	if err := os.CopyFS(target, os.DirFS(source)); err != nil {
		t.Fatal(err)
	}
	value, err := dataset.Load(target)
	if err != nil {
		t.Fatal(err)
	}
	id := value.IDs("dev")[0]
	path := filepath.Join(target, value.Entries[id].Path)
	data, _ := os.ReadFile(path)
	data = append(data, ' ')
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := dataset.Load(target); !errors.Is(err, dataset.ErrInvalidDataset) {
		t.Fatalf("drift err=%v", err)
	}
}

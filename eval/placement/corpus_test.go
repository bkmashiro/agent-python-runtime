package placement

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPublishedPlacementCorpusContract(t *testing.T) {
	root := filepath.Join("..", "agentic", "placement", "v1")
	corpus, err := Load(root)
	if err != nil {
		t.Fatalf("load placement corpus: %v", err)
	}
	if len(corpus.Tasks) != 60 {
		t.Fatalf("tasks=%d want=60", len(corpus.Tasks))
	}
	wantSplits := map[string]int{"development": 40, "decision": 20}
	wantStrata := map[string]int{
		"direct_favored":   12,
		"pysolate_favored": 18,
		"mixed_capability": 12,
		"computer_favored": 10,
		"boundary":         8,
	}
	gotSplits := map[string]int{}
	gotStrata := map[string]int{}
	for _, task := range corpus.Tasks {
		gotSplits[task.Split]++
		gotStrata[task.Stratum]++
		if task.Split == "decision" && task.ModelVisible {
			t.Fatalf("sealed task %s is model-visible before freeze", task.ID)
		}
	}
	for split, want := range wantSplits {
		if gotSplits[split] != want {
			t.Fatalf("split %s=%d want=%d", split, gotSplits[split], want)
		}
	}
	for stratum, want := range wantStrata {
		if gotStrata[stratum] != want {
			t.Fatalf("stratum %s=%d want=%d", stratum, gotStrata[stratum], want)
		}
	}
	if corpus.Manifest.SelectionSeed == "" || corpus.Manifest.Status != "frozen_pre_model" {
		t.Fatalf("manifest is not frozen pre-model: %+v", corpus.Manifest)
	}
	if _, err := os.Stat(filepath.Join(root, "candidate-pool.json")); err != nil {
		t.Fatalf("candidate pool missing: %v", err)
	}
}

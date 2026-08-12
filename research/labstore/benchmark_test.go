package labstore_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/labstore"
)

func TestStatsSeparateObjectsIndexesAndLogicalBodies(t *testing.T) {
	store, err := labstore.Open(filepath.Join(t.TempDir(), "store"), labstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	one, _, err := store.Put(labstore.KindPrompt, []byte("same"), privatePolicy())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Put(labstore.KindPrompt, []byte("same"), privatePolicy()); err != nil {
		t.Fatal(err)
	}
	policy := privatePolicy()
	policy.Links = []labstore.Ref{one}
	two, _, err := store.PutJSON(labstore.KindMetadataEvent, []byte(`{"event":"uses prompt"}`), policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Pin("study", two); err != nil {
		t.Fatal(err)
	}
	stats, err := store.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.SchemaVersion != labstore.StatsSchemaVersion || stats.ObjectCount != 2 || stats.RootCount != 1 || stats.LinkCount != 1 || stats.LogicalBodyBytes != uint64(len("same")+len(`{"event":"uses prompt"}`)) {
		t.Fatalf("stats=%#v", stats)
	}
	if stats.ObjectFileBytes == 0 || stats.IndexBytes == 0 || stats.StoredBytes != stats.ObjectFileBytes+stats.IndexBytes || stats.PrivateObjects != 2 || stats.PortableObjects != 0 {
		t.Fatalf("physical stats=%#v", stats)
	}
}

func TestSyntheticBenchmarkReportHasRequiredBoundedShapes(t *testing.T) {
	report, err := labstore.RunBenchmarks(filepath.Join(t.TempDir(), "bench"), labstore.BenchmarkConfig{
		LongSteps:      4,
		BranchChildren: 3,
		SwarmAgents:    2,
		SwarmSteps:     3,
		LowReuseItems:  4,
		PayloadBytes:   128,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != labstore.BenchmarkSchemaVersion || len(report.Shapes) != 4 || report.StoreFormat != labstore.ObjectSchemaVersion {
		t.Fatalf("report header=%#v", report)
	}
	wantNames := []string{"long_sequential", "branch_children", "shared_swarm", "low_reuse_control"}
	for index, metrics := range report.Shapes {
		if metrics.Shape != wantNames[index] || metrics.RawDuplicatedBytes == 0 || metrics.StoredBytes == 0 || metrics.UniqueObjects == 0 || metrics.PutOperations == 0 || metrics.IngestNanoseconds <= 0 || metrics.QueryNanoseconds <= 0 {
			t.Fatalf("shape[%d]=%#v", index, metrics)
		}
		if metrics.StoredBytes != metrics.ObjectFileBytes+metrics.IndexBytes {
			t.Fatalf("shape byte accounting=%#v", metrics)
		}
		if metrics.UniqueObjects != metrics.PrivateObjects+metrics.PortableObjects {
			t.Fatalf("shape privacy accounting=%#v", metrics)
		}
		if index < 3 && metrics.ReusedPuts == 0 {
			t.Fatalf("high-reuse shape had no dedup: %#v", metrics)
		}
		if index == 3 && metrics.ReusedPuts != 0 {
			t.Fatalf("low-reuse control unexpectedly reused objects: %#v", metrics)
		}
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var decoded labstore.BenchmarkReport
	if err := json.Unmarshal(encoded, &decoded); err != nil || decoded.SchemaVersion != report.SchemaVersion {
		t.Fatalf("machine-readable report err=%v decoded=%#v", err, decoded)
	}
}

func TestSyntheticBenchmarkRejectsUnboundedOrExistingDestination(t *testing.T) {
	temporary := t.TempDir()
	if _, err := labstore.RunBenchmarks(filepath.Join(temporary, "too-large"), labstore.BenchmarkConfig{LongSteps: 1_000_001}); err == nil {
		t.Fatal("unbounded benchmark accepted")
	}
	existing := filepath.Join(temporary, "existing")
	if err := os.Mkdir(existing, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := labstore.RunBenchmarks(existing, labstore.BenchmarkConfig{}); err == nil {
		t.Fatal("existing benchmark destination accepted")
	}
}

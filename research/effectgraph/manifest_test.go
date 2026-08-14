package effectgraph_test

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/effectgraph"
	"github.com/bkmashiro/agent-python-runtime/research/workloads"
)

func TestCheckedInManifestBuildsBodySafeCorpus(t *testing.T) {
	file, err := os.Open("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	manifest, err := effectgraph.DecodeManifest(file)
	if err != nil {
		t.Fatal(err)
	}
	corpus, err := effectgraph.BuildCorpus(manifest, ".", testCorpus().Target)
	if err != nil {
		t.Fatal(err)
	}
	if len(corpus.Programs) != 18 || len(corpus.HistoricalSeeds) != 3 {
		t.Fatalf("programs=%d seeds=%d", len(corpus.Programs), len(corpus.HistoricalSeeds))
	}
	counts := map[string]uint32{}
	for _, program := range corpus.Programs {
		for _, candidate := range program.StructuralCandidates {
			counts[candidate.Kind] += candidate.Occurrences
		}
	}
	want := map[string]uint32{
		effectgraph.CandidateExactRegionReuse: 2, effectgraph.CandidateNativePlacement: 3,
		effectgraph.CandidateOverlapWindow: 5, effectgraph.CandidatePreDispatch: 10,
		effectgraph.CandidateWASMPlacement: 15,
	}
	if !reflect.DeepEqual(counts, want) {
		t.Fatalf("candidate counts=%v want=%v", counts, want)
	}
}

func TestDecodeManifestRejectsDocumentsBeyondBound(t *testing.T) {
	valid, err := os.ReadFile("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	oversized := append(append([]byte(nil), valid...), bytes.Repeat([]byte(" "), (1<<20)-len(valid))...)
	oversized = append(oversized, 'x')
	if _, err := effectgraph.DecodeManifest(bytes.NewReader(oversized)); err == nil {
		t.Fatal("oversized manifest accepted")
	}
}

func TestBuildCorpusBindsExactSourceAndTarget(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "program.py"), []byte("result = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := effectgraph.Manifest{
		SchemaVersion: effectgraph.ManifestSchemaVersion,
		Programs: []effectgraph.ManifestProgram{{
			ID: "program", Provenance: effectgraph.ProvenancePublicSynthetic,
			SourcePath: "program.py", OracleClass: effectgraph.OraclePureResult,
			StructuralCandidates: []effectgraph.Candidate{{Kind: effectgraph.CandidateWASMPlacement, Occurrences: 1}},
			InputsCanonical:      true, OutputsCanonical: true,
		}},
		HistoricalSeeds: []effectgraph.HistoricalSeed{},
	}
	target := testCorpus().Target
	corpus, err := effectgraph.BuildCorpus(manifest, root, target)
	if err != nil {
		t.Fatal(err)
	}
	if corpus.Target != target || corpus.Programs[0].SourceSHA256 != sha([]byte("result = 1\n")) {
		t.Fatalf("corpus=%+v", corpus)
	}
	encoded, err := effectgraph.EncodeCorpus(corpus)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := effectgraph.DecodeCorpus(bytes.NewReader(encoded))
	if err != nil || decoded.Programs[0].SourceSHA256 != corpus.Programs[0].SourceSHA256 {
		t.Fatalf("decoded=%+v err=%v", decoded, err)
	}
}

func TestBuildCorpusRejectsSymlinkSources(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.py")
	if err := os.WriteFile(outside, []byte("result = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "program.py")); err != nil {
		t.Skip(err)
	}
	manifest := effectgraph.Manifest{
		SchemaVersion: effectgraph.ManifestSchemaVersion,
		Programs: []effectgraph.ManifestProgram{{
			ID: "program", Provenance: effectgraph.ProvenancePublicSynthetic,
			SourcePath: "program.py", OracleClass: effectgraph.OraclePureResult,
			StructuralCandidates: []effectgraph.Candidate{}, InputsCanonical: true, OutputsCanonical: true,
		}},
		HistoricalSeeds: []effectgraph.HistoricalSeed{},
	}
	if _, err := effectgraph.BuildCorpus(manifest, root, testCorpus().Target); err == nil {
		t.Fatal("symlink source accepted")
	}
	parentRoot := t.TempDir()
	if err := os.Symlink(filepath.Dir(outside), filepath.Join(parentRoot, "nested")); err != nil {
		t.Skip(err)
	}
	manifest.Programs[0].SourcePath = "nested/outside.py"
	if _, err := effectgraph.BuildCorpus(manifest, parentRoot, testCorpus().Target); err == nil {
		t.Fatal("symlink parent escape accepted")
	}
}

func TestRuntimeWorkloadCopiesMatchCanonicalSources(t *testing.T) {
	definitions, err := workloads.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]string{
		"bounded-planning-v1":  "testdata/programs/bounded_planning_v1.py",
		"stateful-local-v1":    "testdata/programs/stateful_local_v1.py",
		"structured-source-v1": "testdata/programs/structured_source_v1.py",
	}
	for _, definition := range definitions {
		source, err := os.ReadFile(paths[definition.ID])
		if err != nil || string(source) != definition.Code {
			t.Fatalf("%s runtime source drift err=%v", definition.ID, err)
		}
	}
}

func TestManifestRejectsEmbeddedBodiesAndMissingFiles(t *testing.T) {
	raw := []byte(`{"schema_version":"pysolate.effectgraph-manifest.v0","programs":[{"id":"program","provenance":"public_synthetic","source_path":"program.py","source":"private","oracle_class":"pure_result","structural_candidates":[],"inputs_canonical":true,"outputs_canonical":true}],"historical_seeds":[]}`)
	if _, err := effectgraph.DecodeManifest(bytes.NewReader(raw)); err == nil {
		t.Fatal("manifest embedded source accepted")
	}
	manifest := effectgraph.Manifest{
		SchemaVersion: effectgraph.ManifestSchemaVersion,
		Programs: []effectgraph.ManifestProgram{{
			ID: "program", Provenance: effectgraph.ProvenancePublicSynthetic,
			SourcePath: "missing.py", OracleClass: effectgraph.OraclePureResult,
			StructuralCandidates: []effectgraph.Candidate{}, InputsCanonical: true, OutputsCanonical: true,
		}}, HistoricalSeeds: []effectgraph.HistoricalSeed{},
	}
	if _, err := effectgraph.BuildCorpus(manifest, t.TempDir(), testCorpus().Target); err == nil {
		t.Fatal("missing source accepted")
	}
}

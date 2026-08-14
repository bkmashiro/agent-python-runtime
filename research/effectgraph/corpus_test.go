package effectgraph_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/effectgraph"
)

func TestDecodeCorpusRejectsUnknownFieldsAndTrailingJSON(t *testing.T) {
	valid := validCorpusJSON()
	if _, err := effectgraph.DecodeCorpus(bytes.NewReader(append(valid, []byte(` {}`)...))); !errors.Is(err, effectgraph.ErrInvalidCorpus) {
		t.Fatalf("trailing error=%v", err)
	}
	unknown := bytes.Replace(valid, []byte(`"programs"`), []byte(`"unknown":true,"programs"`), 1)
	if _, err := effectgraph.DecodeCorpus(bytes.NewReader(unknown)); !errors.Is(err, effectgraph.ErrInvalidCorpus) {
		t.Fatalf("unknown error=%v", err)
	}
	duplicate := bytes.Replace(valid, []byte(`"id":"pure-compute"`), []byte(`"id":"shadow","id":"pure-compute"`), 1)
	if _, err := effectgraph.DecodeCorpus(bytes.NewReader(duplicate)); !errors.Is(err, effectgraph.ErrInvalidCorpus) {
		t.Fatalf("duplicate error=%v", err)
	}
}

func TestDecodeCorpusRejectsDocumentsBeyondBound(t *testing.T) {
	valid := validCorpusJSON()
	oversized := append(append([]byte(nil), valid...), bytes.Repeat([]byte(" "), (1<<20)-len(valid))...)
	oversized = append(oversized, 'x')
	if _, err := effectgraph.DecodeCorpus(bytes.NewReader(oversized)); !errors.Is(err, effectgraph.ErrInvalidCorpus) {
		t.Fatalf("oversized error=%v", err)
	}
}

func TestCorpusSeparatesAnalyzableProgramsFromBodyFreeHistoricalSeeds(t *testing.T) {
	corpus, err := effectgraph.DecodeCorpus(bytes.NewReader(validCorpusJSON()))
	if err != nil {
		t.Fatal(err)
	}
	if len(corpus.Programs) != 1 || corpus.Programs[0].SourcePath != "testdata/programs/pure.py" {
		t.Fatalf("programs=%+v", corpus.Programs)
	}
	if len(corpus.HistoricalSeeds) != 1 || corpus.HistoricalSeeds[0].SourceStatus != effectgraph.SourceNotGenerated {
		t.Fatalf("historical_seeds=%+v", corpus.HistoricalSeeds)
	}
}

func TestCorpusRejectsPrivateBodiesAndUnboundSourceRows(t *testing.T) {
	privateBody := bytes.Replace(validCorpusJSON(), []byte(`"source_status":"not_generated"`), []byte(`"source_status":"not_generated","source":"secret"`), 1)
	if _, err := effectgraph.DecodeCorpus(bytes.NewReader(privateBody)); !errors.Is(err, effectgraph.ErrInvalidCorpus) {
		t.Fatalf("private body error=%v", err)
	}
	missingDigest := bytes.Replace(validCorpusJSON(), []byte(`"source_sha256":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`), []byte(`"source_sha256":""`), 1)
	if _, err := effectgraph.DecodeCorpus(bytes.NewReader(missingDigest)); !errors.Is(err, effectgraph.ErrInvalidCorpus) {
		t.Fatalf("missing digest error=%v", err)
	}
}

func TestCorpusRejectsInvalidCandidateCountsAndOrdering(t *testing.T) {
	zero := bytes.Replace(validCorpusJSON(), []byte(`"occurrences":1`), []byte(`"occurrences":0`), 1)
	if _, err := effectgraph.DecodeCorpus(bytes.NewReader(zero)); !errors.Is(err, effectgraph.ErrInvalidCorpus) {
		t.Fatalf("zero count error=%v", err)
	}
	duplicate := bytes.Replace(validCorpusJSON(), []byte(`{"kind":"wasm_placement","occurrences":1}`), []byte(`{"kind":"exact_region_reuse","occurrences":1}`), 1)
	if _, err := effectgraph.DecodeCorpus(bytes.NewReader(duplicate)); !errors.Is(err, effectgraph.ErrInvalidCorpus) {
		t.Fatalf("duplicate candidate error=%v", err)
	}
}

func validCorpusJSON() []byte {
	return []byte(`{
		"schema_version":"pysolate.effectgraph-corpus.v0",
		"target":{"artifact_source_commit":"950249a92eaec648b88850300c5653ab62aff888","artifact_sha256":"sha256:1111111111111111111111111111111111111111111111111111111111111111","execution_profile_sha256":"sha256:2222222222222222222222222222222222222222222222222222222222222222","import_closure_sha256":"sha256:3333333333333333333333333333333333333333333333333333333333333333","capability_plan_sha256":"sha256:4444444444444444444444444444444444444444444444444444444444444444","contract_set_sha256":"sha256:5555555555555555555555555555555555555555555555555555555555555555"},
		"programs":[{"id":"pure-compute","provenance":"public_synthetic","source_path":"testdata/programs/pure.py","source_sha256":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","oracle_class":"pure_result","structural_candidates":[{"kind":"exact_region_reuse","occurrences":1},{"kind":"wasm_placement","occurrences":1}],"inputs_canonical":true,"outputs_canonical":true}],
		"historical_seeds":[{"identity_sha256":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","task_shape":"runtime_implementation","source_status":"not_generated","prospective_replay_required":true}]
	}`)
}

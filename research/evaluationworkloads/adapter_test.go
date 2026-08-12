package evaluationworkloads_test

import (
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/evaluation"
	"github.com/bkmashiro/agent-python-runtime/research/evaluationworkloads"
	"github.com/bkmashiro/agent-python-runtime/research/workloads"
)

func TestCorpusFromCanonicalWorkloadsPreservesExactIdentities(t *testing.T) {
	definitions, err := workloads.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	corpus, err := evaluationworkloads.Corpus(definitions)
	if err != nil {
		t.Fatal(err)
	}
	if corpus.SchemaVersion != evaluation.CorpusSchemaVersion || corpus.EvidenceClass != evaluation.EvidenceMechanismOnly || len(corpus.Workloads) != 3 {
		t.Fatalf("corpus=%+v", corpus)
	}
	for i, row := range corpus.Workloads {
		definition := definitions[i]
		if row.ID != definition.ID || row.CodeSHA256 != definition.CodeSHA256 || row.InputSHA256 != definition.InputSHA256 || row.WorkspaceSeedSHA256 != definition.WorkspaceSeedSHA256 || row.Oracle.ExpectedResultSHA256 != definition.ExpectedResultSHA256 || row.Oracle.ExpectedCapabilityCalls != definition.ExpectedCapabilityCalls {
			t.Fatalf("row[%d]=%+v definition=%+v", i, row, definition)
		}
		if len(definition.ExpectedWorkspace) > 0 && row.Oracle.ExpectedWorkspaceSHA256 == "" {
			t.Fatalf("workspace oracle[%d] missing", i)
		}
	}
	if len(corpus.Workloads[0].Treatments) != 3 || len(corpus.Workloads[1].Treatments) != 2 || len(corpus.Workloads[2].Treatments) != 4 {
		t.Fatalf("treatments=%+v", corpus.Workloads)
	}
}

func TestCorpusFromWorkloadsRejectsTamper(t *testing.T) {
	definitions, _ := workloads.Canonical()
	definitions[0].CodeSHA256 = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := evaluationworkloads.Corpus(definitions); err == nil {
		t.Fatal("tampered workload accepted")
	}
}

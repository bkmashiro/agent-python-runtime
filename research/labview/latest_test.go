package labview_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/labview"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestBuildLatestSnapshotProjectsEightVisibleMechanisms(t *testing.T) {
	root := repoRoot(t)
	read := func(path string) []byte {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	snapshot, err := labview.BuildLatestSnapshot(labview.LatestInputs{
		SourcePrefixContract: read("docs/evidence/source-prefix-overlap-contract-v1.json"),
		SourcePrefixEvidence: read("docs/evidence/source-prefix-overlap-v1.json"),
		CampaignManifest:     read("docs/evidence/authority-transparent-campaign-manifest-v1.json"),
		CampaignProjection:   read("docs/evidence/authority-transparent-campaign-v1.json"),
		SemanticPredispatch:  read("docs/evidence/semantic-predispatch-experiment.json"),
		SemanticReuse:        read("docs/evidence/semantic-reuse-observation.json"),
		COWGrowable:          read("docs/evidence/linux-cow-growable-outcome.json"),
		ColdIO:               read("docs/evidence/linux-cold-io-continuation-observation.json"),
		Composable:           read("docs/evidence/spark-composable-direct-report.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.SchemaVersion != labview.LatestSnapshotSchema || len(snapshot.Demos) != 8 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	byID := map[string]labview.LatestDemo{}
	for _, demo := range snapshot.Demos {
		byID[demo.ID] = demo
		if demo.Source == "" || len(demo.Annotations) == 0 || len(demo.Metrics) < 2 || len(demo.Metrics) > 3 {
			t.Fatalf("incomplete demo %+v", demo)
		}
	}
	if byID["source-prefix-overlap"].Metrics[2].Value != "1.923×" {
		t.Fatalf("source-prefix metrics=%+v", byID["source-prefix-overlap"].Metrics)
	}
	if byID["exact-request-sharing"].Metrics[0].Value != "2" || byID["exact-request-sharing"].Metrics[1].Value != "1" || byID["exact-request-sharing"].Annotations[1].Tone != "shared_skip" {
		t.Fatalf("sharing metrics=%+v", byID["exact-request-sharing"].Metrics)
	}
	if byID["source-mismatch-fallback"].Status != "control" || byID["source-mismatch-fallback"].Metrics[1].Value != "2" {
		t.Fatalf("fallback=%+v", byID["source-mismatch-fallback"])
	}
	if byID["semantic-predispatch"].Metrics[2].Value != "1018 ms" || byID["whole-run-retention"].Metrics[0].Value != "3" || byID["cow-fresh-memory"].Metrics[1].Value != "384 MiB" || byID["cold-io-continuation"].Metrics[1].Value != "0 MiB" || byID["fresh-reevaluation"].Metrics[1].Value != "fresh Guest" {
		t.Fatalf("expanded demos are incomplete: %+v", byID)
	}
	encoded, err := labview.EncodeLatestSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := labview.DecodeLatestSnapshot(encoded)
	if err != nil || decoded.Identity != snapshot.Identity {
		t.Fatalf("decode=%+v err=%v", decoded, err)
	}
	firstSVG, err := labview.PaperFigureSVG(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	secondSVG, err := labview.PaperFigureSVG(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstSVG, secondSVG) || !strings.Contains(string(firstSVG), "duplicate physical run skipped") || strings.Contains(strings.ToLower(string(firstSVG)), "cached") {
		t.Fatal("paper figure is nondeterministic or has incorrect sharing semantics")
	}
}

func TestBuildLatestSnapshotFailsClosedOnInputDrift(t *testing.T) {
	root := repoRoot(t)
	read := func(path string) []byte {
		raw, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	base := labview.LatestInputs{
		SourcePrefixContract: read("docs/evidence/source-prefix-overlap-contract-v1.json"),
		SourcePrefixEvidence: read("docs/evidence/source-prefix-overlap-v1.json"),
		CampaignManifest:     read("docs/evidence/authority-transparent-campaign-manifest-v1.json"),
		CampaignProjection:   read("docs/evidence/authority-transparent-campaign-v1.json"),
		SemanticPredispatch:  read("docs/evidence/semantic-predispatch-experiment.json"),
		SemanticReuse:        read("docs/evidence/semantic-reuse-observation.json"),
		COWGrowable:          read("docs/evidence/linux-cow-growable-outcome.json"),
		ColdIO:               read("docs/evidence/linux-cold-io-continuation-observation.json"),
		Composable:           read("docs/evidence/spark-composable-direct-report.json"),
	}
	for name, mutate := range map[string]func(*labview.LatestInputs){
		"source prefix": func(value *labview.LatestInputs) { value.SourcePrefixEvidence = []byte(`{}`) },
		"manifest":      func(value *labview.LatestInputs) { value.CampaignManifest = []byte(`{}`) },
		"campaign":      func(value *labview.LatestInputs) { value.CampaignProjection = []byte(`{}`) },
		"predispatch":   func(value *labview.LatestInputs) { value.SemanticPredispatch = []byte(`{}`) },
		"reuse":         func(value *labview.LatestInputs) { value.SemanticReuse = []byte(`{}`) },
		"cow":           func(value *labview.LatestInputs) { value.COWGrowable = []byte(`{}`) },
		"cold io":       func(value *labview.LatestInputs) { value.ColdIO = []byte(`{}`) },
		"composable":    func(value *labview.LatestInputs) { value.Composable = []byte(`{}`) },
	} {
		t.Run(name, func(t *testing.T) {
			value := base
			mutate(&value)
			if _, err := labview.BuildLatestSnapshot(value); err == nil {
				t.Fatal("invalid latest Lab input accepted")
			}
		})
	}
}

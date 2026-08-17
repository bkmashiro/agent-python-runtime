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

func TestBuildLatestSnapshotProjectsThreeVisibleRealDemos(t *testing.T) {
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
		SourcePrefixCensus:   read("docs/evidence/source-prefix-opportunity-census-v1.json"),
		CampaignManifest:     read("docs/evidence/authority-transparent-campaign-manifest-v1.json"),
		CampaignProjection:   read("docs/evidence/authority-transparent-campaign-v1.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.SchemaVersion != labview.LatestSnapshotSchema || len(snapshot.Demos) != 3 || snapshot.Headline.OptimizationWins != 2 || snapshot.Headline.SafetyControls != 1 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	byID := map[string]labview.LatestDemo{}
	for _, demo := range snapshot.Demos {
		byID[demo.ID] = demo
		if demo.Source == "" || len(demo.Annotations) == 0 || len(demo.Lanes) == 0 || len(demo.Metrics) < 3 || demo.ClaimBoundary == "" {
			t.Fatalf("incomplete demo %+v", demo)
		}
	}
	if byID["source-prefix-overlap"].Metrics[2].Value != "1.923×" {
		t.Fatalf("source-prefix metrics=%+v", byID["source-prefix-overlap"].Metrics)
	}
	if byID["exact-request-sharing"].Metrics[0].Value != "2" || byID["exact-request-sharing"].Metrics[1].Value != "1" || byID["exact-request-sharing"].Annotations[1].Tone != "shared_skip" {
		t.Fatalf("sharing metrics=%+v", byID["exact-request-sharing"].Metrics)
	}
	if byID["source-mismatch-fallback"].Status != "safety_control" || byID["source-mismatch-fallback"].Metrics[1].Value != "2" {
		t.Fatalf("fallback=%+v", byID["source-mismatch-fallback"])
	}
	if snapshot.Boundary.Events != 36 || snapshot.Boundary.StructurallyEligible != 0 || snapshot.Boundary.PerformanceSupported {
		t.Fatalf("boundary=%+v", snapshot.Boundary)
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
		SourcePrefixCensus:   read("docs/evidence/source-prefix-opportunity-census-v1.json"),
		CampaignManifest:     read("docs/evidence/authority-transparent-campaign-manifest-v1.json"),
		CampaignProjection:   read("docs/evidence/authority-transparent-campaign-v1.json"),
	}
	for name, mutate := range map[string]func(*labview.LatestInputs){
		"source prefix": func(value *labview.LatestInputs) { value.SourcePrefixEvidence = []byte(`{}`) },
		"census":        func(value *labview.LatestInputs) { value.SourcePrefixCensus = []byte(`{}`) },
		"manifest":      func(value *labview.LatestInputs) { value.CampaignManifest = []byte(`{}`) },
		"campaign":      func(value *labview.LatestInputs) { value.CampaignProjection = []byte(`{}`) },
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

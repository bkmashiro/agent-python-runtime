package labview

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/workflowbench"
)

func acceptedCampaignFixture(t *testing.T) (workflowbench.CampaignManifest, campaignProjection) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	manifestRaw, err := os.ReadFile(filepath.Join(root, "docs/evidence/authority-transparent-campaign-manifest-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	projectionRaw, err := os.ReadFile(filepath.Join(root, "docs/evidence/authority-transparent-campaign-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest workflowbench.CampaignManifest
	var projection campaignProjection
	if err := strictLatestDecode(manifestRaw, &manifest); err != nil {
		t.Fatal(err)
	}
	if err := strictLatestDecode(projectionRaw, &projection); err != nil {
		t.Fatal(err)
	}
	return manifest, projection
}

func acceptedLatestSnapshot(t *testing.T) LatestSnapshot {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	read := func(path string) []byte {
		raw, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	snapshot, err := BuildLatestSnapshot(LatestInputs{
		SourcePrefixContract: read("docs/evidence/source-prefix-overlap-contract-v1.json"),
		SourcePrefixEvidence: read("docs/evidence/source-prefix-overlap-v1.json"),
		CampaignManifest:     read("docs/evidence/authority-transparent-campaign-manifest-v1.json"),
		CampaignProjection:   read("docs/evidence/authority-transparent-campaign-v1.json"),
		SemanticPredispatch:  read("docs/evidence/semantic-predispatch-day-trip-v1.json"),
		SemanticReuse:        read("docs/evidence/semantic-reuse-observation.json"),
		COWGrowable:          read("docs/evidence/linux-cow-growable-outcome.json"),
		ColdIO:               read("docs/evidence/linux-cold-io-continuation-observation.json"),
		Composable:           read("docs/evidence/spark-composable-direct-report.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestCampaignProjectionRejectsCanonicalIdentityDrift(t *testing.T) {
	manifest, projection := acceptedCampaignFixture(t)
	projection.Programs[1].ID = projection.Programs[0].ID
	if err := validateCampaignPrograms(manifest, projection); err == nil {
		t.Fatal("duplicate canonical program identity accepted")
	}

	_, projection = acceptedCampaignFixture(t)
	projection.Source.ManifestSHA256 = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := validateCampaignPrograms(manifest, projection); err == nil {
		t.Fatal("forged manifest identity accepted")
	}
}

func TestCampaignDemoJoinRejectsUnrelatedPhysicalOwner(t *testing.T) {
	_, projection := acceptedCampaignFixture(t)
	programs := make(map[string]campaignProgramProjection, len(projection.Programs))
	for _, program := range projection.Programs {
		programs[program.ID] = program
	}
	for index := range projection.WalkthroughEvents {
		event := &projection.WalkthroughEvents[index]
		if event.Type == "logical.terminal" && (event.ProgramID == "P05" || event.ProgramID == "P06") {
			event.PhysicalExecutionID = "campaign-guest-1"
		}
	}
	if err := validateCampaignDemoJoins(projection, programs); err == nil {
		t.Fatal("P05/P06 accepted with P01 physical owner")
	}
}

func TestLatestSnapshotPrivateMarkersFailClosed(t *testing.T) {
	snapshot := LatestSnapshot{Demos: []LatestDemo{{Source: "private://trace/body"}}}
	if !latestContainsPrivateMarker(snapshot) {
		t.Fatal("private body marker was not detected")
	}
}

func TestPaperFigureReadsMetricsAndRejectsLayoutDrift(t *testing.T) {
	snapshot := acceptedLatestSnapshot(t)
	snapshot.Demos[0].Metrics[0].Value = "9999 ms"
	snapshot.Demos[0].Metrics[1].Value = "8888 ms"
	snapshot.Demos[0].Metrics[2].Value = "7.777×"
	identity, err := latestSnapshotIdentity(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Identity = identity
	figure, err := PaperFigureSVG(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(figure, []byte("9999 ms → 8888 ms")) || !bytes.Contains(figure, []byte("7.777× mechanism window")) || bytes.Contains(figure, []byte("2950 ms → 1534 ms")) {
		t.Fatal("paper figure did not follow validated snapshot metrics")
	}

	snapshot = acceptedLatestSnapshot(t)
	snapshot.Demos[2].Source = "result = 1\n"
	identity, err = latestSnapshotIdentity(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Identity = identity
	if _, err := PaperFigureSVG(snapshot); err == nil {
		t.Fatal("paper figure accepted source layout drift")
	}

	snapshot = acceptedLatestSnapshot(t)
	snapshot.Demos[0].Annotations = snapshot.Demos[0].Annotations[1:]
	identity, err = latestSnapshotIdentity(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Identity = identity
	if _, err := PaperFigureSVG(snapshot); err == nil {
		t.Fatal("paper figure accepted missing required annotation")
	}
}

func TestLatestSnapshotRejectsMalformedVisibleFields(t *testing.T) {
	mutations := map[string]func(*LatestSnapshot){
		"metric tone":            func(snapshot *LatestSnapshot) { snapshot.Demos[3].Metrics[0].Tone = "forged" },
		"empty metric note":      func(snapshot *LatestSnapshot) { snapshot.Demos[4].Metrics[0].Note = "" },
		"segment tone":           func(snapshot *LatestSnapshot) { snapshot.Demos[6].Lanes[0].Segments[0].Tone = "forged" },
		"missing timeline":       func(snapshot *LatestSnapshot) { snapshot.Demos[6].Lanes = nil },
		"null state flow lanes":  func(snapshot *LatestSnapshot) { snapshot.Demos[4].Lanes = nil },
		"timeline on state flow": func(snapshot *LatestSnapshot) { snapshot.Demos[4].Lanes = snapshot.Demos[0].Lanes },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			snapshot := acceptedLatestSnapshot(t)
			mutate(&snapshot)
			identity, err := latestSnapshotIdentity(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			snapshot.Identity = identity
			if err := ValidateLatestSnapshot(snapshot); err == nil {
				t.Fatal("identity-valid malformed visible field accepted")
			}
		})
	}
}

func TestCampaignTerminalMustBeUnique(t *testing.T) {
	_, projection := acceptedCampaignFixture(t)
	terminal, err := campaignTerminal(projection.WalkthroughEvents, "P05")
	if err != nil {
		t.Fatal(err)
	}
	projection.WalkthroughEvents = append(projection.WalkthroughEvents, terminal)
	if _, err := campaignTerminal(projection.WalkthroughEvents, "P05"); err == nil {
		t.Fatal("duplicate terminal accepted")
	}
}

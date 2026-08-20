package e2e_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/semanticspeculation"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
)

func TestExactGuestCampaignExecutableRunsAndPersistsPureLocalCoordinate(t *testing.T) {
	artifactPath := guestArtifact(t)
	artifact, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(artifactPath)
	manifest, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	imports, err := os.ReadFile(filepath.Join(root, "import-inventory.json"))
	if err != nil {
		t.Fatal(err)
	}
	profile, err := runtimeconfig.NewExecutionProfile("base", []string{"json"})
	if err != nil {
		t.Fatal(err)
	}
	profile, err = profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{
		ProfileID: "base", ArtifactSHA256: testDigestBytes(artifact), ManifestSHA256: testDigestBytes(manifest),
		ImportRoots: []string{"json"}, QualifiedImportRoots: []string{"json"},
	})
	if err != nil {
		t.Fatal(err)
	}
	runConfig := runtimeconfig.DefaultRunConfig()
	runConfig.ExecutionProfile = &profile
	workspaceRoot := t.TempDir()
	campaign, err := semanticspeculation.NewExactGuestCampaign(semanticspeculation.ExactGuestCampaignConfig{
		Artifact: artifact, Manifest: manifest, ImportInventory: imports, RunConfig: runConfig,
		WorkspaceRoot: workspaceRoot, PhysicalDelay: 250 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := campaign.RunCoordinate(context.Background(), semanticspeculation.Phase3SyntheticCases()[5], 2)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Aggregate.CaseID != "pure_local" || evidence.Aggregate.TrialIndex != 2 || len(evidence.Records) != 3 {
		t.Fatalf("evidence=%+v", evidence)
	}
	evidenceRoot := t.TempDir()
	if err := os.Chmod(evidenceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	ref, err := semanticspeculation.WriteMatchedCaseEvidenceFile(evidenceRoot, evidence)
	if err != nil || ref.Identity != evidence.Identity || ref.CaseID != "pure_local" || ref.TrialIndex != 2 {
		t.Fatalf("ref=%+v err=%v", ref, err)
	}
}

package e2e_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/semanticspeculation"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
)

func TestExactGuestPhase4CampaignColdShortCoordinate(t *testing.T) {
	artifactPath := guestArtifact(t)
	artifact, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(filepath.Dir(artifactPath), "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	digest := func(value []byte) string { sum := sha256.Sum256(value); return fmt.Sprintf("sha256:%x", sum) }
	profile, err := runtimeconfig.NewExecutionProfile("base", []string{"json"})
	if err != nil {
		t.Fatal(err)
	}
	profile, err = profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{ProfileID: "base", ArtifactSHA256: digest(artifact), ManifestSHA256: digest(manifest), ImportRoots: []string{"json"}, QualifiedImportRoots: []string{"json"}})
	if err != nil {
		t.Fatal(err)
	}
	config := runtimeconfig.DefaultRunConfig()
	config.ExecutionProfile = &profile
	for _, profileID := range []string{"cold_end_to_end", "preprovisioned_equivalent_capacity"} {
		t.Run(profileID, func(t *testing.T) {
			record, runErr := semanticspeculation.RunPhase4CampaignCoordinate(context.Background(), semanticspeculation.Phase4CampaignConfig{Artifact: artifact, RunConfig: config, WorkspaceRoot: t.TempDir()}, semanticspeculation.Phase4CampaignCoordinate{Profile: profileID, CaseID: "direct_read_short_control", Treatment: "semantic_pre_dispatch", TrialIndex: 1})
			if runErr != nil {
				t.Fatal(runErr)
			}
			if record.FinalProgramOutcome != "success" || record.LogicalCallCount != 1 || record.AnalyzerSessionCount != 1 || record.FormalGuestExecutions != 1 || record.OrphanedPhysicalCount != 0 {
				t.Fatalf("record=%+v", record)
			}
			if profileID == "preprovisioned_equivalent_capacity" && record.PreparedOrCOWHitCount != 1 {
				t.Fatalf("preprovisioned record=%+v", record)
			}
		})
	}
}

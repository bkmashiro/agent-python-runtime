package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/semanticspeculation"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
)

func main() {
	artifactRoot := flag.String("artifact-root", "", "verified exact Guest artifact directory")
	evidenceRoot := flag.String("evidence-root", "", "new private evidence directory")
	workspaceRoot := flag.String("workspace-root", "", "new private disposable workspace directory")
	sourceCommit := flag.String("source-commit", "", "40-hex campaign source commit")
	flag.Parse()
	if flag.NArg() != 0 || *artifactRoot == "" || *evidenceRoot == "" || *workspaceRoot == "" || *sourceCommit == "" {
		fatalf("all flags are required and positional arguments are forbidden")
	}
	if err := os.Mkdir(*evidenceRoot, 0o700); err != nil {
		fatalf("create evidence root: %v", err)
	}
	if err := os.Mkdir(*workspaceRoot, 0o700); err != nil {
		fatalf("create workspace root: %v", err)
	}
	defer os.RemoveAll(*workspaceRoot)

	artifact := mustRead(filepath.Join(*artifactRoot, "agent-python-runtime.wasm"))
	manifest := mustRead(filepath.Join(*artifactRoot, "manifest.json"))
	imports := mustRead(filepath.Join(*artifactRoot, "import-inventory.json"))
	artifactSHA := digest(artifact)
	manifestSHA := digest(manifest)
	profile, err := runtimeconfig.NewExecutionProfile("base", []string{"json"})
	if err != nil {
		fatalf("create execution profile: %v", err)
	}
	profile, err = profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{
		ProfileID: "base", ArtifactSHA256: artifactSHA, ManifestSHA256: manifestSHA,
		ImportRoots: []string{"json"}, QualifiedImportRoots: []string{"json"},
	})
	if err != nil {
		fatalf("bind execution profile: %v", err)
	}
	runConfig := runtimeconfig.DefaultRunConfig()
	runConfig.ExecutionProfile = &profile
	campaign, err := semanticspeculation.NewExactGuestCampaign(semanticspeculation.ExactGuestCampaignConfig{
		Artifact: artifact, Manifest: manifest, ImportInventory: imports, RunConfig: runConfig,
		WorkspaceRoot: *workspaceRoot, PhysicalDelay: 250 * time.Millisecond,
	})
	if err != nil {
		fatalf("create campaign: %v", err)
	}

	coordinates := semanticspeculation.Phase3CampaignCoordinates()
	refs := make([]semanticspeculation.MatchedCaseEvidenceReference, 0, len(coordinates))
	for index, coordinate := range coordinates {
		fixture, ok := semanticspeculation.FrozenPhase3Case(coordinate.CaseID)
		if !ok {
			fatalf("unknown frozen case %q", coordinate.CaseID)
		}
		fmt.Fprintf(os.Stderr, "[%02d/%02d] %s trial %d\n", index+1, len(coordinates), coordinate.CaseID, coordinate.TrialIndex)
		evidence, runErr := campaign.RunCoordinate(context.Background(), fixture, coordinate.TrialIndex)
		if runErr != nil {
			fatalf("run %s trial %d: %v", coordinate.CaseID, coordinate.TrialIndex, runErr)
		}
		ref, writeErr := semanticspeculation.WriteMatchedCaseEvidenceFile(*evidenceRoot, evidence)
		if writeErr != nil {
			fatalf("write %s trial %d: %v", coordinate.CaseID, coordinate.TrialIndex, writeErr)
		}
		refs = append(refs, ref)
	}
	sealed, err := semanticspeculation.SealCampaignEvidenceManifest(*sourceCommit, campaign.Bindings(), refs)
	if err != nil {
		fatalf("seal campaign manifest: %v", err)
	}
	if err := semanticspeculation.VerifyCampaignEvidenceFiles(*evidenceRoot, sealed); err != nil {
		fatalf("verify campaign files: %v", err)
	}
	manifestRef, err := semanticspeculation.WriteCampaignEvidenceManifestFile(*evidenceRoot, sealed)
	if err != nil {
		fatalf("write campaign manifest: %v", err)
	}
	encoded, _ := json.Marshal(manifestRef)
	fmt.Println(string(encoded))
}

func mustRead(path string) []byte {
	value, err := os.ReadFile(path)
	if err != nil {
		fatalf("read %s: %v", path, err)
	}
	return value
}

func digest(value []byte) string {
	// Use the runtime package's canonical profile requirement through a local
	// JSON-free helper to keep this command's output body-free.
	return fmt.Sprintf("sha256:%x", sha256Sum(value))
}

func sha256Sum(value []byte) [32]byte {
	return sha256.Sum256(value)
}

func fatalf(format string, arguments ...any) {
	fmt.Fprintf(os.Stderr, "semantic-speculation-campaign: "+format+"\n", arguments...)
	os.Exit(1)
}

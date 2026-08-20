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
	artifactPath := flag.String("artifact", "", "exact Guest WASM")
	manifestPath := flag.String("manifest", "", "artifact manifest")
	matrixPath := flag.String("matrix", "", "frozen Phase 4 matrix")
	outputRoot := flag.String("output-root", "", "new private raw evidence root")
	workspaceRoot := flag.String("workspace-root", "", "new private disposable workspace root")
	useCOW := flag.Bool("cow", false, "use Linux private COW for preprovisioned semantic capacity")
	flag.Parse()
	if flag.NArg() != 0 || *artifactPath == "" || *manifestPath == "" || *matrixPath == "" || *outputRoot == "" || *workspaceRoot == "" {
		fatalf("all paths are required")
	}
	matrixRaw := mustRead(*matrixPath)
	if _, err := semanticspeculation.DecodePhase4ExtensionMatrix(matrixRaw); err != nil {
		fatalf("matrix: %v", err)
	}
	if err := os.Mkdir(*outputRoot, 0700); err != nil {
		fatalf("output root: %v", err)
	}
	if err := os.Mkdir(*workspaceRoot, 0700); err != nil {
		fatalf("workspace root: %v", err)
	}
	defer os.RemoveAll(*workspaceRoot)
	artifact := mustRead(*artifactPath)
	manifest := mustRead(*manifestPath)
	artifactSHA := digest(artifact)
	manifestSHA := digest(manifest)
	profile, err := runtimeconfig.NewExecutionProfile("base", []string{"json"})
	if err != nil {
		fatalf("profile: %v", err)
	}
	profile, err = profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{ProfileID: "base", ArtifactSHA256: artifactSHA, ManifestSHA256: manifestSHA, ImportRoots: []string{"json"}, QualifiedImportRoots: []string{"json"}})
	if err != nil {
		fatalf("bind profile: %v", err)
	}
	runConfig := runtimeconfig.DefaultRunConfig()
	runConfig.ExecutionProfile = &profile
	coordinates := semanticspeculation.Phase4CampaignCoordinates()
	for index, coordinate := range coordinates {
		fmt.Fprintf(os.Stderr, "[%03d/%03d] %s %s %s trial %d\n", index+1, len(coordinates), coordinate.Profile, coordinate.CaseID, coordinate.Treatment, coordinate.TrialIndex)
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		record, runErr := semanticspeculation.RunPhase4CampaignCoordinate(ctx, semanticspeculation.Phase4CampaignConfig{Artifact: artifact, RunConfig: runConfig, WorkspaceRoot: *workspaceRoot, UseLinuxCOW: *useCOW}, coordinate)
		cancel()
		if runErr != nil {
			fatalf("coordinate %d: %v", index, runErr)
		}
		raw, _ := json.Marshal(record)
		raw = append(raw, '\n')
		name := fmt.Sprintf("%03d-%s-%s-%s-%d.json", index, coordinate.Profile, coordinate.CaseID, coordinate.Treatment, coordinate.TrialIndex)
		file, err := os.OpenFile(filepath.Join(*outputRoot, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			fatalf("create %s: %v", name, err)
		}
		if _, err = file.Write(raw); err != nil {
			file.Close()
			fatalf("write %s: %v", name, err)
		}
		if err = file.Close(); err != nil {
			fatalf("close %s: %v", name, err)
		}
	}
	fmt.Printf("records=%d artifact=%s matrix=%s\n", len(coordinates), artifactSHA, semanticspeculation.Phase4ExtensionMatrixIdentity)
}
func mustRead(path string) []byte {
	value, err := os.ReadFile(path)
	if err != nil {
		fatalf("read %s: %v", path, err)
	}
	return value
}
func digest(value []byte) string { sum := sha256.Sum256(value); return fmt.Sprintf("sha256:%x", sum) }
func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "semantic-phase4-campaign: "+format+"\n", args...)
	os.Exit(1)
}

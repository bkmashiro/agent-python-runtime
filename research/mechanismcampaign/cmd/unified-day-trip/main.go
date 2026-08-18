package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/agenttrajectory"
	"github.com/bkmashiro/agent-python-runtime/research/mechanismcampaign"
)

func main() {
	artifactPath := flag.String("artifact", "", "verified Guest WASM artifact")
	fixturePath := flag.String("fixture", "research/agenttrajectory/testdata/day-trip-planning", "public day-trip fixture")
	outputPath := flag.String("output", "docs/evidence/unified-day-trip-campaign-v2.json", "evidence output")
	workspaceRoot := flag.String("workspace-root", "", "private scratch root")
	repository := flag.String("repository", ".", "clean Git checkout used for the campaign")
	sourceCommit := flag.String("source-commit", "", "optional expected implementation commit")
	pairs := flag.Int("pairs", 3, "matched baseline/optimized pairs")
	enableCOW := flag.Bool("cow", runtime.GOOS == "linux", "require Linux memory COW")
	enableColdIO := flag.Bool("cold-io", runtime.GOOS == "linux", "require cold-I/O continuation")
	flag.Parse()
	if *artifactPath == "" || *workspaceRoot == "" || *pairs != 3 {
		fatalf("artifact and workspace-root are required; pairs must equal the preregistered value 3")
	}
	verifiedCommit, err := verifyCleanCommit(*repository, *sourceCommit)
	if err != nil {
		fatalf("verify source commit: %v", err)
	}
	artifact, err := os.ReadFile(*artifactPath)
	if err != nil {
		fatalf("read artifact: %v", err)
	}
	fixture, err := agenttrajectory.LoadFixture(*fixturePath)
	if err != nil {
		fatalf("load fixture: %v", err)
	}
	fixtureJSON, err := json.Marshal(fixture)
	if err != nil {
		fatalf("marshal fixture: %v", err)
	}
	if err := os.MkdirAll(*workspaceRoot, 0o700); err != nil {
		fatalf("create workspace root: %v", err)
	}
	const statementStep = 600 * time.Millisecond
	const finalizationDelay = 7 * time.Second
	campaign, err := mechanismcampaign.RunCampaign(context.Background(), mechanismcampaign.CampaignConfig{
		ArtifactPath: *artifactPath, Fixture: fixture, WorkspaceRoot: filepath.Join(*workspaceRoot, "full"),
		GenerationStep: statementStep, FinalizationDelay: finalizationDelay,
		EnableCOW: *enableCOW, EnableColdIO: *enableColdIO, ColdPayloadBytes: 200_000_000,
	})
	if err != nil {
		fatalf("run full campaign: %v", err)
	}
	matched, err := mechanismcampaign.RunMatchedControls(context.Background(), mechanismcampaign.MatchedControlConfig{
		ArtifactPath: *artifactPath, Fixture: fixture, WorkspaceRoot: filepath.Join(*workspaceRoot, "matched"),
		GenerationStep: statementStep, FinalizationDelay: finalizationDelay, Pairs: *pairs,
	})
	if err != nil {
		fatalf("run matched controls: %v", err)
	}
	evidence, err := mechanismcampaign.ProjectEvidence(
		campaign, matched, "day-trip-unified-v2", verifiedCommit, digest(artifact), digest(fixtureJSON),
		mechanismcampaign.GenerationSchedule{StatementStepMS: statementStep.Milliseconds(), FinalizationDelayMS: finalizationDelay.Milliseconds()},
	)
	if err != nil {
		fatalf("project evidence: %v", err)
	}
	encoded, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		fatalf("marshal evidence: %v", err)
	}
	encoded = append(encoded, '\n')
	if err := os.MkdirAll(filepath.Dir(*outputPath), 0o755); err != nil {
		fatalf("create output directory: %v", err)
	}
	temporary := *outputPath + ".tmp"
	if err := os.WriteFile(temporary, encoded, 0o644); err != nil {
		fatalf("write evidence: %v", err)
	}
	if err := os.Rename(temporary, *outputPath); err != nil {
		fatalf("publish evidence: %v", err)
	}
	fmt.Printf("evidence=%s\nsha256=%s\nbaseline_median_ns=%d\noptimized_median_ns=%d\nsavings_ns=%d\n", *outputPath, digest(encoded), matched.BaselineMedianNS, matched.OptimizedMedianNS, matched.SavingsNS)
}

func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func verifyCleanCommit(repository, expected string) (string, error) {
	head, err := exec.Command("git", "-C", repository, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", err
	}
	commit := strings.TrimSpace(string(head))
	if expected != "" && expected != commit {
		return "", fmt.Errorf("expected commit %s, checkout is %s", expected, commit)
	}
	status, err := exec.Command("git", "-C", repository, "status", "--porcelain").Output()
	if err != nil {
		return "", err
	}
	if len(status) != 0 {
		return "", fmt.Errorf("repository is not clean")
	}
	return commit, nil
}

func fatalf(format string, arguments ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", arguments...)
	os.Exit(1)
}

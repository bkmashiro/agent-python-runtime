package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/bkmashiro/agent-python-runtime/research/workflowbench"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
)

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

func main() {
	artifactPath := flag.String("artifact", "", "verified Linux Guest artifact")
	artifactSourceCommit := flag.String("artifact-source-commit", "", "exact 40-hex Guest source commit")
	harnessSourceCommit := flag.String("harness-source-commit", "", "exact 40-hex experiment-driver source commit")
	output := flag.String("output", "", "output evidence JSON")
	seed := flag.Uint64("seed", 20260815, "nonzero deterministic shuffle seed")
	flag.Parse()
	if *artifactPath == "" || *output == "" || !commitPattern.MatchString(*artifactSourceCommit) || !commitPattern.MatchString(*harnessSourceCommit) || *seed == 0 {
		fmt.Fprintln(os.Stderr, "artifact, exact source commits, output and nonzero seed are required")
		os.Exit(2)
	}
	if err := run(context.Background(), *artifactPath, *artifactSourceCommit, *harnessSourceCommit, *output, *seed); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, artifactPath, artifactSourceCommit, harnessSourceCommit, output string, seed uint64) error {
	artifact, err := os.ReadFile(artifactPath)
	if err != nil {
		return err
	}
	if err := verifyArtifactManifest(artifactPath, artifactSourceCommit, artifact); err != nil {
		return err
	}
	runConfig := runtimeconfig.DefaultRunConfig()
	profileJSON, err := json.Marshal(runConfig)
	if err != nil {
		return err
	}
	identity := workflowbench.RuntimeIdentity{
		SourceCommit:           artifactSourceCommit,
		ArtifactSHA256:         sha(artifact),
		ExecutionProfileSHA256: sha(profileJSON),
		CapabilityPlanSHA256:   sha([]byte(`{"capabilities":[]}`)),
		HarnessSHA256:          sha([]byte("pysolate.workflowbench.v0\x00" + harnessSourceCommit)),
	}
	manifest, err := workflowbench.GenerateManifest(seed, identity)
	if err != nil {
		return err
	}
	runner, err := (wazeroengine.Factory{}).New(ctx, artifact, runConfig)
	if err != nil {
		return err
	}
	defer runner.Close(context.Background())
	evidence, err := workflowbench.ExecutePair(ctx, manifest, func(ctx context.Context, task workflowbench.Task) (string, error) {
		request, err := json.Marshal(map[string]any{
			"run_id": task.TaskID,
			"code":   "result = {'value': inputs['value'] + 1}",
			"inputs": map[string]any{"value": task.SubmissionOrder},
		})
		if err != nil {
			return "", err
		}
		response, err := runner.Run(ctx, request, "")
		if err != nil {
			return "", err
		}
		result, err := stableGuestResult(response)
		if err != nil {
			return "", err
		}
		return sha(result), nil
	})
	if err != nil {
		return err
	}
	encoded, err := workflowbench.EncodeEvidence(evidence)
	if err != nil {
		return err
	}
	if err := atomicWrite(output, encoded); err != nil {
		return err
	}
	fmt.Printf("seed=%d tasks=%d divergences=%d baseline_physical=%d optimized_physical=%d evidence_sha256=%s\n",
		seed, len(evidence.Tasks), evidence.Divergences, evidence.BaselinePhysicalExecutions, evidence.OptimizedPhysicalExecutions, sha(encoded))
	return nil
}

func stableGuestResult(payload []byte) ([]byte, error) {
	var response struct {
		Status string          `json:"status"`
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(payload, &response); err != nil || response.Status != "ok" || len(response.Result) == 0 || (len(response.Error) != 0 && string(response.Error) != "null") {
		return nil, fmt.Errorf("Guest result is not publishable")
	}
	var value any
	if err := json.Unmarshal(response.Result, &value); err != nil {
		return nil, fmt.Errorf("Guest result is not canonical JSON")
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("Guest result is not canonical JSON")
	}
	return canonical, nil
}

func verifyArtifactManifest(artifactPath, sourceCommit string, artifact []byte) error {
	artifactInfo, err := os.Lstat(artifactPath)
	if err != nil || !artifactInfo.Mode().IsRegular() {
		return fmt.Errorf("artifact must be a regular file")
	}
	manifestPath := filepath.Join(filepath.Dir(artifactPath), "manifest.json")
	manifestInfo, err := os.Lstat(manifestPath)
	if err != nil || !manifestInfo.Mode().IsRegular() {
		return fmt.Errorf("manifest must be a regular file")
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	var manifest struct {
		Artifact struct {
			Filename string `json:"filename"`
			SHA256   string `json:"sha256"`
		} `json:"artifact"`
		Build struct {
			RepositoryCommit string `json:"repository_commit"`
		} `json:"build"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return err
	}
	expectedArtifact := sha(artifact)[7:]
	if manifest.Build.RepositoryCommit != sourceCommit || manifest.Artifact.Filename != filepath.Base(artifactPath) || manifest.Artifact.SHA256 != expectedArtifact {
		return fmt.Errorf("artifact manifest identity mismatch")
	}
	return nil
}

func atomicWrite(path string, content []byte) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".workflow-benchmark-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func sha(value []byte) string {
	sum := sha256.Sum256(value)
	return fmt.Sprintf("sha256:%x", sum[:])
}

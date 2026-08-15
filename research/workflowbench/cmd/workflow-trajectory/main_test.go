package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/trajectory"
	"github.com/bkmashiro/agent-python-runtime/research/workflowbench"
)

func TestGenerateMaterializesBalancedCPUExperimentAsTrajectory(t *testing.T) {
	manifest, err := workflowbench.GenerateManifest(20260815, workflowbench.RuntimeIdentity{
		SourceCommit:   "0123456789abcdef0123456789abcdef01234567",
		ArtifactSHA256: digest("artifact"), ExecutionProfileSHA256: digest("profile"),
		CapabilityPlanSHA256: digest("plan"), HarnessSHA256: digest("harness"),
	})
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := workflowbench.ExecutePair(context.Background(), manifest, func(ctx context.Context, task workflowbench.Task) (string, error) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Millisecond):
			return digest(task.TaskID), nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := workflowbench.EncodeEvidence(evidence)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	evidencePath, output := filepath.Join(root, "evidence.json"), filepath.Join(root, "trajectory.json")
	if err := os.WriteFile(evidencePath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := generate(evidencePath, filepath.Join(root, "store"), output, "0123456789abcdef0123456789abcdef01234567"); err != nil {
		t.Fatal(err)
	}
	encoded, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var exported trajectory.PrivateExport
	if err := json.Unmarshal(encoded, &exported); err != nil || trajectory.ValidateExport(exported) != nil {
		t.Fatalf("invalid export err=%v", err)
	}
	if len(exported.Events) < 100 {
		t.Fatalf("trajectory too small: %d", len(exported.Events))
	}
	body := string(encoded)
	for _, expected := range []string{"process_user_plus_system_delta", "baseline_optimized", "optimized_baseline", "physical_execution_id", "workflowbench.execute_pair"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("missing %q", expected)
		}
	}
}

func digest(seed string) string {
	value := make([]byte, 64)
	for index := range value {
		value[index] = "0123456789abcdef"[(index+len(seed))%16]
	}
	return "sha256:" + string(value)
}

package placement_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/eval/agentic"
	"github.com/bkmashiro/agent-python-runtime/eval/placement"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
)

func TestProfileQualifiedPysolateRealGuest(t *testing.T) {
	artifactDir := os.Getenv("APYRUN_GUEST_ARTIFACT_DIR")
	if artifactDir == "" {
		t.Skip("set APYRUN_GUEST_ARTIFACT_DIR for the pinned real Guest integration")
	}
	bundle, err := placement.LoadGuestBundle(artifactDir, placement.AgentStdlibWorkspaceImports(), placement.GuestIdentityExpectation{
		ArtifactSHA256: "sha256:4078dbcec0307e5636c86b84523b8349a557db115bfac7569ff5d003b08ceadb",
		ManifestSHA256: "sha256:b6baa6f5adb27263ef586faed897cde42c1815b4ce7c415333696800b3bbb6a6",
	})
	if err != nil {
		t.Fatal(err)
	}
	dataset, err := agentic.LoadRoutingDataset(filepath.Join("..", "agentic", "routing", "v1"))
	if err != nil {
		t.Fatal(err)
	}
	var task agentic.Task
	for _, candidate := range dataset.Tasks {
		if candidate.ID == "rd-003" {
			task = candidate
			break
		}
	}
	if task.ID == "" {
		t.Fatal("missing rd-003")
	}
	tools, err := agentic.NewToolRuntime(task)
	if err != nil || tools.SetTurn(0) != nil {
		t.Fatal(err)
	}
	profile := bundle.Profile
	executor, err := agentic.NewWASIPythonExecutor(context.Background(), bundle.WASM, runtimeconfig.RunConfig{
		Timeout: 30 * time.Second, MaxRequestBytes: 1 << 20, MaxResponseBytes: 1 << 20,
		MemoryLimitPages: 8192, ExecutionProfile: &profile,
	}, tools)
	if err != nil {
		t.Fatal(err)
	}
	defer executor.Close(context.Background())
	program := `import csv
cd(folder="Documents")
raw = cat(file_name="metrics.csv")["file_content"]
labels = []
for label, value in csv.reader(raw.splitlines()):
    if int(value) > 4:
        labels.append(label)
touch(file_name="high_value_rows.txt")
echo(content=",".join(labels), file_name="high_value_rows.txt")
result = {"status": "completed"}
`
	result, err := executor.ExecuteProfileQualified(context.Background(), "placement-profile-real-1", program, "base", []string{"csv"}, 4)
	if err != nil || !result.Success {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	request, err := runtimeconfig.DecodeRunRequest(result.RawRequest)
	if err != nil || request.Compatibility == nil || len(request.Compatibility.Imports) != 1 || request.Compatibility.Imports[0] != "csv" {
		t.Fatalf("csv compatibility declaration missing: request=%+v err=%v", request, err)
	}
	response, err := runtimeconfig.DecodeAndValidateRunResponse(request, result.RawResponse)
	if err != nil || response.RunPlan == nil || response.RunPlan.ProfileID() != "base" ||
		response.ImportReceipts == nil || response.ImportReceipts.Validate() != nil {
		t.Fatalf("profile evidence missing or invalid: response=%+v err=%v", response, err)
	}
	score, err := agentic.ScoreStateful(task, tools.Trace(), tools.FileSystem())
	if err != nil || !score.TracePassed || !score.FinalStatePassed {
		t.Fatalf("score=%+v err=%v", score, err)
	}
}

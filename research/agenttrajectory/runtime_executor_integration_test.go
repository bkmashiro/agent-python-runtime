package agenttrajectory_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/agenttrajectory"
)

func TestPysolateCandidateExecutorRunsTwoFreshGuestsBeforeSelection(t *testing.T) {
	artifactPath := os.Getenv("AGENT_RUNTIME_GUEST")
	if artifactPath == "" {
		t.Skip("AGENT_RUNTIME_GUEST not configured")
	}
	artifact, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := agenttrajectory.LoadFixture("testdata/day-trip-planning")
	if err != nil {
		t.Fatal(err)
	}
	executor, err := agenttrajectory.NewPysolateCandidateExecutor(agenttrajectory.PysolateExecutorConfig{
		Artifact: artifact, Fixture: fixture, WorkspaceRoot: filepath.Join(t.TempDir(), "workspaces"),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	defer executor.Close(context.Background())
	candidates := []agenttrajectory.CandidateResponse{
		{SchemaVersion: agenttrajectory.CandidateResponseSchemaVersion, CandidateID: agenttrajectory.CandidateBrighton, Summary: "Use the observed Brighton weather, rail, and Royal Pavilion prices.", PythonSource: "import travel\nweather = travel.weather(\"brighton\")\nrail = travel.rail(\"brighton\", travellers=2)\nattraction = travel.attractions(\"brighton\")\nresult = {\"candidate_id\": \"brighton\", \"weather\": weather, \"rail\": rail, \"attraction\": attraction, \"total_cost_gbp\": rail[\"total_cost_gbp\"] + attraction[\"entry_cost_gbp\"] * 2}"},
		{SchemaVersion: agenttrajectory.CandidateResponseSchemaVersion, CandidateID: agenttrajectory.CandidateOxford, Summary: "Use the observed Oxford weather, rail, and Ashmolean prices.", PythonSource: "import travel\nweather = travel.weather(\"oxford\")\nrail = travel.rail(\"oxford\", travellers=2)\nattraction = travel.attractions(\"oxford\")\nresult = {\"candidate_id\": \"oxford\", \"weather\": weather, \"rail\": rail, \"attraction\": attraction, \"total_cost_gbp\": rail[\"total_cost_gbp\"] + attraction[\"entry_cost_gbp\"] * 2}"},
	}
	observed, err := executor.ExecuteCandidates(ctx, candidates)
	if err != nil {
		t.Fatal(err)
	}
	if len(observed) != 2 || len(observed[0].Output) == 0 || len(observed[1].Output) == 0 || observed[0].WorkspaceSHA256 == observed[1].WorkspaceSHA256 {
		t.Fatalf("observed=%+v", observed)
	}
	branch, err := executor.Seal(ctx, agenttrajectory.CandidateOxford)
	if err != nil {
		t.Fatal(err)
	}
	if branch.SelectedCandidateID != agenttrajectory.CandidateOxford || len(branch.DiscardedCandidateIDs) != 1 || branch.DiscardedCandidateIDs[0] != agenttrajectory.CandidateBrighton || branch.SelectedRootSHA256 == "" {
		t.Fatalf("branch=%+v", branch)
	}
}

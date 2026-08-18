package mechanismcampaign

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/agenttrajectory"
)

func TestUnifiedDayTripCampaignRunsAllPortableMechanismsInOneWorkflow(t *testing.T) {
	artifactPath := os.Getenv("AGENT_RUNTIME_GUEST")
	if artifactPath == "" {
		t.Skip("AGENT_RUNTIME_GUEST is required")
	}
	fixture, err := agenttrajectory.LoadFixture(filepath.Join("..", "agenttrajectory", "testdata", "day-trip-planning"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := RunCampaign(t.Context(), CampaignConfig{
		ArtifactPath: artifactPath, Fixture: fixture, WorkspaceRoot: filepath.Join(t.TempDir(), "campaign"),
		GenerationStep: 600 * time.Millisecond, FinalizationDelay: 7 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := result.Validate(false, false); err != nil {
		t.Fatal(err)
	}
	if result.Origin.Retained.PhysicalExecutionID != result.Origin.LeaderPhysicalID {
		t.Fatalf("origin result=%+v", result.Origin)
	}
	if result.Resume.BoundRoot.IdentitySHA256 != result.Candidates.SelectedRoot.IdentitySHA256 {
		t.Fatalf("resume root=%+v selected=%+v", result.Resume.BoundRoot, result.Candidates.SelectedRoot)
	}
	if len(result.Events) < 40 {
		t.Fatalf("events=%d", len(result.Events))
	}
	for index, event := range result.Events {
		if event.Sequence != uint64(index+1) || (index > 0 && event.AtNS < result.Events[index-1].AtNS) {
			t.Fatalf("event ordering at %d: %+v", index, event)
		}
	}
}

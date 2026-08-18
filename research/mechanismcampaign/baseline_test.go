package mechanismcampaign

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/agenttrajectory"
)

func TestBaselineWaitsForCompleteSourceBeforeStartingFreshCandidates(t *testing.T) {
	artifact := os.Getenv("AGENT_RUNTIME_GUEST")
	if artifact == "" {
		t.Skip("AGENT_RUNTIME_GUEST is required")
	}
	fixture, err := agenttrajectory.LoadFixture(filepath.Join("..", "agenttrajectory", "testdata", "day-trip-planning"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := RunBaselineCandidateStage(t.Context(), CandidateStageConfig{
		ArtifactPath: artifact, Fixture: fixture, WorkspaceRoot: filepath.Join(t.TempDir(), "baseline"),
		GenerationStep: 100 * time.Millisecond, FinalizationDelay: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.LatencyNS == 0 || result.Candidates["oxford"].TotalCostGBP != 78 {
		t.Fatalf("baseline=%+v", result)
	}
	complete := map[string]int64{}
	for _, event := range result.Events {
		if event.Type == "source.feed.complete" {
			complete[event.ActorID] = event.AtNS
		}
		if event.Type == "guest.start" && event.AtNS < complete[event.ActorID] {
			t.Fatalf("Guest started before complete source: %+v", event)
		}
	}
}

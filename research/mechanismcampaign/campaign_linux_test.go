package mechanismcampaign

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/agenttrajectory"
)

func TestUnifiedDayTripCampaignSelectsLinuxCOWAndColdIO(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("COW and cold I/O evidence require Linux")
	}
	artifactPath := os.Getenv("AGENT_RUNTIME_GUEST")
	if artifactPath == "" {
		t.Skip("AGENT_RUNTIME_GUEST is required")
	}
	fixture, err := agenttrajectory.LoadFixture(filepath.Join("..", "agenttrajectory", "testdata", "day-trip-planning"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := RunCampaign(t.Context(), CampaignConfig{
		ArtifactPath: artifactPath, Fixture: fixture, WorkspaceRoot: filepath.Join(t.TempDir(), "campaign-linux"),
		GenerationStep: 600 * time.Millisecond, FinalizationDelay: 7 * time.Second, EnableCOW: true, EnableColdIO: true, ColdPayloadBytes: 200_000_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := result.Validate(true, true); err != nil {
		t.Fatal(err)
	}
	if result.Resume.ColdIO.PageOutAttempts != 1 || result.Resume.ColdIO.ColdSucceeded != 1 {
		t.Fatalf("cold evidence=%+v", result.Resume.ColdIO)
	}
}

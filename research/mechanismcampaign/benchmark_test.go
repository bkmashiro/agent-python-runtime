package mechanismcampaign

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/agenttrajectory"
)

func TestMatchedControlPreservesOutputsAcrossBaselineAndPrefixPreDispatch(t *testing.T) {
	artifact := os.Getenv("AGENT_RUNTIME_GUEST")
	if artifact == "" {
		t.Skip("AGENT_RUNTIME_GUEST is required")
	}
	fixture, err := agenttrajectory.LoadFixture(filepath.Join("..", "agenttrajectory", "testdata", "day-trip-planning"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := RunMatchedControls(t.Context(), MatchedControlConfig{
		ArtifactPath: artifact, Fixture: fixture, WorkspaceRoot: filepath.Join(t.TempDir(), "matched"),
		GenerationStep: 100 * time.Millisecond, FinalizationDelay: 100 * time.Millisecond, Pairs: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Pairs) != 1 || result.Pairs[0].ResultSHA256 == "" || result.BaselineMedianNS == 0 || result.OptimizedMedianNS == 0 {
		t.Fatalf("matched=%+v", result)
	}
}

package mechanismcampaign

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/agentfunction"
)

func TestRealOriginBriefingSharesOneFreshGuestAndRetainsItsResult(t *testing.T) {
	artifactPath := os.Getenv("AGENT_RUNTIME_GUEST")
	if artifactPath == "" {
		t.Skip("AGENT_RUNTIME_GUEST is required")
	}
	absoluteArtifact, err := filepath.Abs(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	result, err := RunOriginSharingStage(t.Context(), OriginSharingConfig{
		ArtifactPath: absoluteArtifact, StoreRoot: filepath.Join(t.TempDir(), "store"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := result.RetainForMain(t.Context()); err != nil {
		t.Fatal(err)
	}
	var value struct {
		Origin string `json:"origin"`
		Day    string `json:"day"`
		Status string `json:"status"`
	}
	if json.Unmarshal(result.Value, &value) != nil || value.Origin != "london" || value.Day != "saturday" || value.Status != "ready" {
		t.Fatalf("value=%s", result.Value)
	}
	leaders, waiters := 0, 0
	for _, disposition := range result.LogicalDispositions {
		switch disposition {
		case agentfunction.Leader:
			leaders++
		case agentfunction.Waiter:
			waiters++
		}
	}
	if result.PhysicalComputes != 1 || leaders != 1 || waiters != 1 || result.Retained.Disposition != agentfunction.Retained ||
		result.Retained.PhysicalExecutionID != result.LeaderPhysicalID {
		t.Fatalf("result=%+v", result)
	}
	physicalStarts, physicalEnds, retained := 0, 0, 0
	for _, event := range result.Events {
		switch event.Type {
		case "function.physical.start":
			physicalStarts++
		case "function.physical.end":
			physicalEnds++
		case "function.retained":
			retained++
		}
	}
	if physicalStarts != 1 || physicalEnds != 1 || retained != 1 {
		t.Fatalf("physical starts=%d ends=%d retained=%d", physicalStarts, physicalEnds, retained)
	}
}

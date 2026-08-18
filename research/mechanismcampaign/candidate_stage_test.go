package mechanismcampaign

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/agenttrajectory"
)

func TestRealDayTripCandidateStagePreDispatchesSixReadsBeforeFreshGuests(t *testing.T) {
	artifactPath := os.Getenv("AGENT_RUNTIME_GUEST")
	if artifactPath == "" {
		t.Skip("AGENT_RUNTIME_GUEST is required")
	}
	absoluteArtifact, err := filepath.Abs(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := agenttrajectory.LoadFixture(filepath.Join("..", "agenttrajectory", "testdata", "day-trip-planning"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := RunCandidateStage(t.Context(), CandidateStageConfig{
		ArtifactPath: absoluteArtifact, Fixture: fixture, WorkspaceRoot: filepath.Join(t.TempDir(), "workspace"),
		GenerationStep: 600 * time.Millisecond, FinalizationDelay: 7 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Candidates["brighton"].TotalCostGBP != 118.4 || result.Candidates["oxford"].TotalCostGBP != 78 {
		t.Fatalf("candidate outputs=%+v", result.Candidates)
	}
	if len(result.SelectedCapsule) == 0 || result.SelectedInfo.WorkspaceSHA256 == "" {
		t.Fatalf("selected capsule info=%+v bytes=%d", result.SelectedInfo, len(result.SelectedCapsule))
	}
	for id, output := range result.Candidates {
		if output.ControllerSnapshot.PhysicalIssues != 3 || output.ControllerSnapshot.LogicalClaims != 3 || output.ControllerSnapshot.Consumed != 3 ||
			!output.ControllerSnapshot.SourceSealed || output.AdmissionSnapshot.QualifiedCallCount != 3 || !output.AdmissionSnapshot.Complete {
			t.Fatalf("%s controller=%+v admission=%+v", id, output.ControllerSnapshot, output.AdmissionSnapshot)
		}
	}
	feedComplete := map[string]int64{}
	sealed := map[string]int64{}
	guestStart := map[string]int64{}
	guestEnd := map[string]int64{}
	requestStarts := map[string]int64{}
	var branchSeal, capsuleExport, branchDiscard int64
	semanticIssues, semanticClaims := 0, 0
	for _, event := range result.Events {
		switch event.Type {
		case "source.feed.complete":
			feedComplete[event.ActorID] = event.AtNS
		case "source.sealed":
			sealed[event.ActorID] = event.AtNS
		case "guest.start":
			guestStart[event.ActorID] = event.AtNS
		case "guest.end":
			guestEnd[event.ActorID] = event.AtNS
		case "request.start":
			requestStarts[event.LogicalID] = event.AtNS
		case "semantic.issue":
			semanticIssues++
		case "semantic.claim":
			semanticClaims++
		case "branch.seal":
			branchSeal = event.AtNS
		case "capsule.export":
			capsuleExport = event.AtNS
		case "branch.discard":
			branchDiscard = event.AtNS
		}
	}
	for _, id := range []string{"brighton", "oxford"} {
		if feedComplete[id] == 0 || sealed[id] < feedComplete[id] || guestStart[id] <= sealed[id] || guestEnd[id] <= guestStart[id] {
			t.Fatalf("%s feed_complete=%d sealed=%d guest=[%d,%d]", id, feedComplete[id], sealed[id], guestStart[id], guestEnd[id])
		}
		for _, api := range []string{"weather", "rail", "attractions"} {
			if started := requestStarts[id+"-"+api]; started == 0 || started >= feedComplete[id] {
				t.Fatalf("%s %s request_start=%d feed_complete=%d", id, api, started, feedComplete[id])
			}
		}
	}
	if max64(guestStart["brighton"], guestStart["oxford"]) >= min64(guestEnd["brighton"], guestEnd["oxford"]) {
		t.Fatalf("candidate Guests did not overlap: starts=%v ends=%v", guestStart, guestEnd)
	}
	if semanticIssues != 6 || semanticClaims != 6 {
		t.Fatalf("semantic issues=%d claims=%d", semanticIssues, semanticClaims)
	}
	if lastGuestEnd := max64(guestEnd["brighton"], guestEnd["oxford"]); branchSeal <= lastGuestEnd || capsuleExport <= branchSeal || branchDiscard <= capsuleExport {
		t.Fatalf("selected workspace order guest_end=%d seal=%d export=%d discard=%d", lastGuestEnd, branchSeal, capsuleExport, branchDiscard)
	}
}

func min64(left, right int64) int64 {
	if left < right {
		return left
	}
	return right
}

func max64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

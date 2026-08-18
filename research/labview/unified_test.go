package labview

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildUnifiedSnapshotProjectsOneCampaign(t *testing.T) {
	raw := readUnifiedEvidence(t)
	snapshot, err := BuildUnifiedSnapshot(raw)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.SchemaVersion != UnifiedSnapshotSchema || snapshot.Selected != "oxford" || snapshot.FinalTotalGBP != 78 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	if len(snapshot.Candidates) != 2 || len(snapshot.Phases) != 6 || len(snapshot.Events) < 20 {
		t.Fatalf("incomplete projection: candidates=%d phases=%d events=%d", len(snapshot.Candidates), len(snapshot.Phases), len(snapshot.Events))
	}
	if snapshot.MatchedControl.PairCount != 3 || snapshot.MatchedControl.MedianSavingsNS <= 0 || !snapshot.MatchedControl.EquivalentResults {
		t.Fatalf("invalid matched control: %+v", snapshot.MatchedControl)
	}
	candidates := map[string]UnifiedCandidate{}
	for _, candidate := range snapshot.Candidates {
		candidates[candidate.ID] = candidate
	}
	steps := map[string]int{}
	for _, event := range snapshot.Events {
		if event.Type != "source.statement.complete" {
			continue
		}
		steps[event.ActorID]++
		if event.SourceStep != steps[event.ActorID] || event.SourceStepCount == 0 || event.SourceDelta == "" ||
			!bytes.HasSuffix([]byte(event.SourcePrefix), []byte(event.SourceDelta)) || snapshotSHA([]byte(event.SourcePrefix)) != event.SourcePrefixSHA256 {
			t.Fatalf("invalid projected source prefix at %s step %d", event.ActorID, event.SourceStep)
		}
		if event.SourceStep == event.SourceStepCount && event.SourcePrefix != candidates[event.ActorID].ExecutedSource {
			t.Fatalf("final source prefix does not close for %s", event.ActorID)
		}
	}
	if steps["brighton"] != 12 || steps["oxford"] != 6 {
		t.Fatalf("unexpected source step counts: %+v", steps)
	}
	encoded, err := EncodeUnifiedSnapshot(snapshot)
	if err != nil || bytes.Contains(bytes.ToLower(encoded), []byte("/users/")) || bytes.Contains(bytes.ToLower(encoded), []byte(".hermes")) {
		t.Fatalf("unsafe encoded projection: err=%v", err)
	}
}

func TestBuildUnifiedSnapshotRejectsMutationAndDuplicateKey(t *testing.T) {
	raw := readUnifiedEvidence(t)
	mutated := bytes.Replace(raw, []byte(`"main_total_gbp": 78`), []byte(`"main_total_gbp": 77`), 1)
	if _, err := BuildUnifiedSnapshot(mutated); err == nil {
		t.Fatal("expected evidence mutation rejection")
	}
	duplicate := bytes.Replace(raw, []byte(`"campaign_id": "day-trip-unified-v2"`), []byte(`"campaign_id": "day-trip-unified-v2", "campaign_id": "day-trip-unified-v2"`), 1)
	if _, err := BuildUnifiedSnapshot(duplicate); err == nil {
		t.Fatal("expected duplicate key rejection")
	}
}

func readUnifiedEvidence(t *testing.T) []byte {
	t.Helper()
	path := filepath.Join("..", "..", "docs", "evidence", "unified-day-trip-campaign-v2.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

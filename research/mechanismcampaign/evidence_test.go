package mechanismcampaign

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestUnifiedEvidenceRejectsBindingAndPrivacyMutations(t *testing.T) {
	valid := validUnifiedEvidence(t)
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	mutations := []func(*Evidence){
		func(value *Evidence) { value.FullRun.SourceMismatchRejected = false },
		func(value *Evidence) { value.FullRun.Candidates["oxford"] = CandidateEvidence{} },
		func(value *Evidence) { value.Platform = "/Users/private" },
		func(value *Evidence) { value.MatchedControl.Pairs[1].ResultSHA256 = digestTextValue("different") },
		func(value *Evidence) { value.MatchedControl.BaselineMedianNS++ },
		func(value *Evidence) { value.MatchedControl.SavingsNS++ },
		func(value *Evidence) { value.MatchedControl.Pairs[1].FirstLane = "baseline" },
		func(value *Evidence) { value.FullRun.CandidateResultSHA256 = digestTextValue("different") },
		func(value *Evidence) { value.FullRun.Events[2].AtNS = -1 },
		func(value *Evidence) { value.FullRun.Candidates["york"] = value.FullRun.Candidates["oxford"] },
		func(value *Evidence) {
			candidate := value.FullRun.Candidates["brighton"]
			candidate.SourceSHA256 = digestTextValue("different")
			value.FullRun.Candidates["brighton"] = candidate
		},
		func(value *Evidence) { value.FullRun.ImportedWorkspaceSHA256 = digestTextValue("different") },
		func(value *Evidence) { value.FullRun.Events[14].Outcome = "error" },
		func(value *Evidence) { value.FullRun.Events[2].Outcome = "access_token=deadbeef" },
		func(value *Evidence) { value.Mechanisms[0].EventIDs[0] = value.Mechanisms[1].EventIDs[0] },
	}
	for index, mutate := range mutations {
		candidate := valid
		candidate.FullRun.Candidates = cloneCandidates(valid.FullRun.Candidates)
		candidate.FullRun.Events = append([]Event(nil), valid.FullRun.Events...)
		candidate.MatchedControl.Pairs = append([]MatchedPair(nil), valid.MatchedControl.Pairs...)
		candidate.Mechanisms = cloneMechanisms(valid.Mechanisms)
		mutate(&candidate)
		if candidate.Validate() == nil {
			t.Fatalf("mutation %d accepted", index)
		}
	}
}

func validUnifiedEvidence(t *testing.T) Evidence {
	t.Helper()
	digest := digestTextValue("evidence")
	brightonChunks, _ := candidateSourceChunks("brighton", "")
	oxfordChunks, _ := candidateSourceChunks("oxford", "")
	brightonSource, oxfordSource := strings.Join(brightonChunks, ""), strings.Join(oxfordChunks, "")
	brightonDigest, oxfordDigest := digestTextValue(brightonSource), digestTextValue(oxfordSource)
	brightonResponse := json.RawMessage(`{"status":"ok","result":{"candidate_id":"brighton","total_cost_gbp":118.4}}`)
	oxfordResponse := json.RawMessage(`{"status":"ok","result":{"candidate_id":"oxford","total_cost_gbp":78}}`)
	mainResponse := json.RawMessage(`{"status":"ok","result":{"selected":"oxford","total_gbp":78}}`)
	events := []Event{
		{Type: "function.logical", ActorID: "brighton", LogicalID: "origin-brighton"},
		{Type: "function.physical.start", ActorID: "host", PhysicalID: "origin-physical"},
		{Type: "function.physical.end", ActorID: "host", PhysicalID: "origin-physical", Outcome: "ok"},
		{Type: "function.leader", ActorID: "brighton", LogicalID: "origin-brighton", PhysicalID: "origin-physical", Outcome: "leader"},
		{Type: "function.waiter", ActorID: "oxford", LogicalID: "origin-oxford", PhysicalID: "origin-physical", Outcome: "waiter"},

		{Type: "source.statement.complete", ActorID: "brighton", LogicalID: "brighton-statement"},
		{Type: "semantic.issue", ActorID: "host", LogicalID: "brighton-weather", PhysicalID: "request-brighton-weather", IdentitySHA256: digest},
		{Type: "request.start", ActorID: "host", LogicalID: "brighton-weather", PhysicalID: "request-brighton-weather"},
		{Type: "request.start", ActorID: "host", LogicalID: "brighton-rail", PhysicalID: "request-brighton-rail"},
		{Type: "request.start", ActorID: "host", LogicalID: "brighton-attractions", PhysicalID: "request-brighton-attractions"},
		{Type: "source.feed.complete", ActorID: "brighton", LogicalID: "source-brighton", IdentitySHA256: digest},
		{Type: "source.sealed", ActorID: "brighton", LogicalID: "source-brighton", IdentitySHA256: digest},
		{Type: "guest.start", ActorID: "brighton", LogicalID: "candidate-brighton", PhysicalID: "guest-brighton"},
		{Type: "guest.end", ActorID: "brighton", LogicalID: "candidate-brighton", PhysicalID: "guest-brighton", Outcome: "ok"},
		{Type: "cow.selected", ActorID: "brighton", LogicalID: "candidate-brighton", PhysicalID: "guest-brighton", Outcome: "private_memory"},
		{Type: "source.statement.complete", ActorID: "oxford", LogicalID: "oxford-statement"},
		{Type: "semantic.issue", ActorID: "host", LogicalID: "oxford-weather", PhysicalID: "request-oxford-weather", IdentitySHA256: digest},
		{Type: "request.start", ActorID: "host", LogicalID: "oxford-weather", PhysicalID: "request-oxford-weather"},
		{Type: "request.start", ActorID: "host", LogicalID: "oxford-rail", PhysicalID: "request-oxford-rail"},
		{Type: "request.start", ActorID: "host", LogicalID: "oxford-attractions", PhysicalID: "request-oxford-attractions"},
		{Type: "source.feed.complete", ActorID: "oxford", LogicalID: "source-oxford", IdentitySHA256: digest},
		{Type: "source.sealed", ActorID: "oxford", LogicalID: "source-oxford", IdentitySHA256: digest},
		{Type: "guest.start", ActorID: "oxford", LogicalID: "candidate-oxford", PhysicalID: "guest-oxford"},
		{Type: "guest.end", ActorID: "oxford", LogicalID: "candidate-oxford", PhysicalID: "guest-oxford", Outcome: "ok"},
		{Type: "cow.selected", ActorID: "oxford", LogicalID: "candidate-oxford", PhysicalID: "guest-oxford", Outcome: "private_memory"},
		{Type: "function.retained", ActorID: "main", LogicalID: "origin-main", PhysicalID: "origin-physical", Outcome: "retained"},
		{Type: "capsule.export", ActorID: "host", LogicalID: "oxford", IdentitySHA256: digest, Outcome: "serialized"},
		{Type: "capsule.import", ActorID: "host", LogicalID: "oxford", IdentitySHA256: digest, Outcome: "verified"},
		{Type: "capsule.bind", ActorID: "host", LogicalID: "oxford", IdentitySHA256: digest, Outcome: "portable_root_bound"},
		{Type: "cold_io.resume", ActorID: "main", LogicalID: "resume-main", PhysicalID: "guest-main", Outcome: "fresh_continuation"},
		{Type: "control.argument_mismatch", ActorID: "host", LogicalID: "control-argument", Outcome: "rejected"},
		{Type: "control.source_mismatch", ActorID: "host", LogicalID: "control-source", Outcome: "rejected"},
	}
	for index := range events {
		events[index].Sequence = uint64(index + 1)
		events[index].ID = "event-" + twoDigit(index+1)
		events[index].AtNS = int64(index + 1)
		if events[index].Type == "source.feed.complete" || events[index].Type == "source.sealed" {
			if events[index].ActorID == "brighton" {
				events[index].IdentitySHA256 = brightonDigest
			} else {
				events[index].IdentitySHA256 = oxfordDigest
			}
		}
	}
	full := FullRunEvidence{
		DurationNS: 100, OriginLogicalCalls: 3, OriginPhysicalComputes: 1, OriginRetained: true,
		Candidates: map[string]CandidateEvidence{
			"brighton": {TotalCostGBP: 118.4, SourceSHA256: brightonDigest, PhysicalIssues: 3, LogicalClaims: 3, SourceSealed: true, COWSelected: true, ModelSource: configSource(brightonSource), ExecutedSource: brightonSource, GuestResponse: brightonResponse},
			"oxford":   {TotalCostGBP: 78, SourceSHA256: oxfordDigest, PhysicalIssues: 3, LogicalClaims: 3, SourceSealed: true, COWSelected: true, ModelSource: configSource(oxfordSource), ExecutedSource: oxfordSource, GuestResponse: oxfordResponse},
		},
		CandidateResultSHA256: digest,
		SelectedCandidate:     "oxford", SelectedRootSHA256: digest, SelectedTreeSHA256: digest, ImportedWorkspaceSHA256: digest, BoundRootSHA256: digest,
		MainSelected: "oxford", MainTotalGBP: 78, MainGuestResponse: mainResponse, ColdWaits: 1, ColdSucceeded: 1, PageOutSucceeded: 1,
		ColdResumes: 1, ColdAdvisedBytes: 4096, ArgumentMismatchRejected: true, SourceMismatchRejected: true, Events: events,
	}
	matched := MatchedControlResult{BaselineMedianNS: 20, OptimizedMedianNS: 10, SavingsNS: 10}
	for index := 1; index <= 3; index++ {
		matched.Pairs = append(matched.Pairs, MatchedPair{PairIndex: index, FirstLane: []string{"baseline", "optimized"}[(index-1)%2], BaselineLatencyNS: 20, OptimizedLatencyNS: 10, ResultSHA256: digest})
	}
	evidence := Evidence{
		SchemaVersion: SchemaVersion, CampaignID: "day-trip-unified-v2", SourceCommit: "0000000000000000000000000000000000000000",
		ArtifactSHA256: digest, FixtureSHA256: digest, Platform: "linux/amd64",
		Schedule: GenerationSchedule{StatementStepMS: 600, FinalizationDelayMS: 7000}, FullRun: full, MatchedControl: matched,
	}
	evidence.Mechanisms = projectMechanisms(events)
	return evidence
}

func cloneCandidates(input map[string]CandidateEvidence) map[string]CandidateEvidence {
	output := make(map[string]CandidateEvidence, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func cloneMechanisms(input []Mechanism) []Mechanism {
	output := append([]Mechanism(nil), input...)
	for index := range output {
		output[index].EventIDs = append([]string(nil), output[index].EventIDs...)
	}
	return output
}

func twoDigit(value int) string {
	const digits = "0123456789"
	return string([]byte{digits[(value/10)%10], digits[value%10]})
}

package agenttrajectory_test

import (
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/agenttrajectory"
)

func TestProjectObservedCandidateStreamMeasuresRealArrivalWindowAgainstStrongBaseline(t *testing.T) {
	content := `{"schema_version":"pysolate.day-trip-candidate.v1","candidate_id":"oxford","summary":"observed","python_source":"weather = travel.weather(\"oxford\")\nrail = travel.rail(\"oxford\", travellers=2)\nattraction = travel.attractions(\"oxford\")\nresult = {\"candidate_id\":\"oxford\",\"weather\":weather,\"rail\":rail,\"attraction\":attraction,\"total_cost_gbp\":rail[\"total_cost_gbp\"]+attraction[\"entry_cost_gbp\"]*2}"}`
	chunks := []agenttrajectory.StreamChunk{
		{ElapsedNS: uint64(100 * time.Millisecond), Content: content[:150]},
		{ElapsedNS: uint64(300 * time.Millisecond), Content: content[150:220]},
		{ElapsedNS: uint64(500 * time.Millisecond), Content: content[220:]},
	}
	evidence, err := agenttrajectory.ProjectObservedCandidateStream(agenttrajectory.RawCandidateStream{
		SchemaVersion: agenttrajectory.RawCandidateStreamSchemaVersion,
		CandidateID:   "oxford", Model: "deepseek-v4-flash", ResponseID: "response-1",
		HeadersElapsedNS: uint64(50 * time.Millisecond), Chunks: chunks, Content: content,
	}, map[string]time.Duration{"weather": 300 * time.Millisecond, "rail": 600 * time.Millisecond, "attractions": 400 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if evidence.SourceCompleteNS == 0 || evidence.StreamCompleteNS != uint64(500*time.Millisecond) || len(evidence.Statements) != 4 || len(evidence.ToolCalls) != 3 {
		t.Fatalf("unexpected projection: %+v", evidence)
	}
	if evidence.NativeParallelReadyNS != evidence.SourceCompleteNS+uint64(600*time.Millisecond) {
		t.Fatalf("parallel ready=%d source=%d", evidence.NativeParallelReadyNS, evidence.SourceCompleteNS)
	}
	if evidence.PrefixPreDispatchReadyNS > evidence.NativeParallelReadyNS || evidence.SavingsVsParallelNS != evidence.NativeParallelReadyNS-evidence.PrefixPreDispatchReadyNS {
		t.Fatalf("opportunity projection drift: %+v", evidence)
	}
}

func TestProjectObservedCandidateStreamRejectsMissingOrInconsistentTrace(t *testing.T) {
	_, err := agenttrajectory.ProjectObservedCandidateStream(agenttrajectory.RawCandidateStream{SchemaVersion: agenttrajectory.RawCandidateStreamSchemaVersion}, map[string]time.Duration{"weather": time.Second})
	if err == nil {
		t.Fatal("invalid trace accepted")
	}
}

func TestCandidateModelRequestUsesTheHarnessContract(t *testing.T) {
	fixture, err := agenttrajectory.LoadFixture("testdata/day-trip-planning")
	if err != nil {
		t.Fatal(err)
	}
	request, err := agenttrajectory.NewCandidateModelRequest(fixture, agenttrajectory.CandidateOxford)
	if err != nil {
		t.Fatal(err)
	}
	if request.CallID != "candidate-oxford" || request.ResponseKind != agenttrajectory.ResponseCandidate || len(request.Messages) != 2 {
		t.Fatalf("request=%+v", request)
	}
}

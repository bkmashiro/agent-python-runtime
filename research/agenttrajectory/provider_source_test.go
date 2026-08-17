package agenttrajectory_test

import (
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/agenttrajectory"
)

func TestCandidateContractAcceptsSemicolonSeparatedSafeSource(t *testing.T) {
	candidate := agenttrajectory.CandidateResponse{
		SchemaVersion: agenttrajectory.CandidateResponseSchemaVersion,
		CandidateID:   agenttrajectory.CandidateOxford,
		Summary:       "Observe Oxford fixture data without inventing values.",
		PythonSource:  `import travel; weather = travel.weather("oxford"); rail = travel.rail("oxford", travellers=2); attraction = travel.attractions("oxford"); result = {"candidate_id":"oxford","weather":weather,"rail":rail,"attraction":attraction,"total_cost_gbp":rail["total_cost_gbp"]+attraction["entry_cost_gbp"]*2}`,
	}
	if candidate.Validate() != nil {
		t.Fatal("safe semicolon-separated provider source was rejected")
	}
}

package releasereadiness_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/agenttrajectory"
	"github.com/bkmashiro/agent-python-runtime/research/releasereadiness"
)

func TestProjectUsesNaturalStatementClosureAndStrongParallelBaseline(t *testing.T) {
	lines := []string{
		`metrics = ops.query_metrics(service="checkout", window="6h")`,
		`logs = ops.query_logs(service="checkout", severity="error", window="6h")`,
		`deployment = ops.latest_deployment(repository="shop/checkout")`,
		`config = ops.read_deployment(cluster="prod-eu", namespace="checkout")`,
	}
	for len(lines) < 59 {
		lines = append(lines, fmt.Sprintf("check_%02d = %d > -1", len(lines), len(lines)))
	}
	lines = append(lines, `result = {"status": "ready", "checks": []}`)
	response := releasereadiness.ProgramResponse{SchemaVersion: releasereadiness.ProgramSchemaVersion, Summary: "release readiness", PythonSource: strings.Join(lines, "\n")}
	body, _ := json.Marshal(response)
	raw := agenttrajectory.RawCandidateStream{SchemaVersion: agenttrajectory.RawCandidateStreamSchemaVersion, CandidateID: "release-readiness", Model: "deepseek-v4-flash", ResponseID: "response-1", StartedAt: "2026-08-18T00:00:00Z", DoneElapsedNS: uint64(5 * time.Second), Content: string(body), Chunks: []agenttrajectory.StreamChunk{
		{ElapsedNS: uint64(1 * time.Second), Reasoning: "reason"},
		{ElapsedNS: uint64(2 * time.Second), Content: string(body[:len(body)/2])},
		{ElapsedNS: uint64(4 * time.Second), Content: string(body[len(body)/2:])},
	}}
	evidence, err := releasereadiness.Project(raw, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence.Statements) != 60 || len(evidence.ToolCalls) != 4 {
		t.Fatalf("unexpected coverage: %d statements, %d calls", len(evidence.Statements), len(evidence.ToolCalls))
	}
	if evidence.Projection.SequentialReadyNS != uint64(11700*time.Millisecond) {
		t.Fatalf("sequential=%d", evidence.Projection.SequentialReadyNS)
	}
	if evidence.Projection.ParallelReadyNS != uint64(7100*time.Millisecond) {
		t.Fatalf("parallel=%d", evidence.Projection.ParallelReadyNS)
	}
	if evidence.Projection.PrefixReadyNS != uint64(5100*time.Millisecond) {
		t.Fatalf("prefix=%d", evidence.Projection.PrefixReadyNS)
	}
}

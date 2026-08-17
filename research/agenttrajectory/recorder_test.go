package agenttrajectory_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/agenttrajectory"
	"github.com/bkmashiro/agent-python-runtime/research/labstore"
	"github.com/bkmashiro/agent-python-runtime/research/trajectory"
)

func TestPrivateRecorderPersistsCompleteProviderBodiesAndLineage(t *testing.T) {
	root := filepath.Join(t.TempDir(), "recording")
	recorder, err := agenttrajectory.NewPrivateRecorder(root, "0123456789abcdef0123456789abcdef01234567")
	if err != nil {
		t.Fatal(err)
	}
	request := agenttrajectory.ModelRequest{CallID: "main-plan", ActorID: "main", ResponseKind: agenttrajectory.ResponsePlanningBrief, Messages: []agenttrajectory.ModelMessage{{Role: "system", Content: "public system"}, {Role: "user", Content: "public request"}}}
	result := agenttrajectory.ModelResult{
		CallID: "main-plan", ActorID: "main", Content: `{"schema_version":"pysolate.day-trip-planning-brief.v1","task":"Plan a Saturday day trip for two people from London within a GBP 100 total budget.","candidate_ids":["brighton","oxford"]}`,
		ResponseID: "response-1", Model: "deepseek-v4-flash",
		RawRequest:  []byte(`{"messages":[{"content":"public request","role":"user"}],"model":"deepseek-v4-flash"}`),
		RawResponse: []byte(`{"id":"response-1","model":"deepseek-v4-flash","choices":[{"message":{"role":"assistant","content":"{\"schema_version\":\"pysolate.day-trip-planning-brief.v1\",\"task\":\"Plan a Saturday day trip for two people from London within a GBP 100 total budget.\",\"candidate_ids\":[\"brighton\",\"oxford\"]}"}}]}`),
	}
	if err := recorder.RecordModelCall(context.Background(), request, result); err != nil {
		t.Fatal(err)
	}
	exported, err := recorder.Complete(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if exported.Profile != trajectory.ProfileExperimentFull || exported.Privacy != labstore.PrivacyPrivate || len(exported.Events) != 5 {
		t.Fatalf("export=%+v", exported)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	rootInfo, _ := os.Stat(root)
	traceInfo, _ := os.Stat(filepath.Join(root, "trajectory.jsonl"))
	if rootInfo.Mode().Perm() != 0o700 || traceInfo.Mode().Perm() != 0o600 {
		t.Fatalf("modes root=%o trace=%o", rootInfo.Mode().Perm(), traceInfo.Mode().Perm())
	}
	store, err := labstore.Open(filepath.Join(root, "store"), labstore.Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := trajectory.ValidateEvidenceExportWithStore(exported, store); err != nil {
		t.Fatalf("private export invalid: %v", err)
	}
	var modelBodies, modelOutputs int
	for _, event := range exported.Events {
		if event.Type == trajectory.EventModelBody {
			modelBodies++
		}
		if event.Type == trajectory.EventModelOutput && event.Body != nil {
			modelOutputs++
		}
	}
	if modelBodies != 1 || modelOutputs != 1 {
		t.Fatalf("model bodies=%d outputs=%d", modelBodies, modelOutputs)
	}
}

func TestPrivateRecorderRejectsMismatchedCallIdentity(t *testing.T) {
	recorder, err := agenttrajectory.NewPrivateRecorder(filepath.Join(t.TempDir(), "recording"), "0123456789abcdef0123456789abcdef01234567")
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	request := agenttrajectory.ModelRequest{CallID: "main-plan", ActorID: "main", ResponseKind: agenttrajectory.ResponsePlanningBrief, Messages: []agenttrajectory.ModelMessage{{Role: "user", Content: "request"}}}
	result := agenttrajectory.ModelResult{CallID: "other", ActorID: "main", Content: `{}`, RawRequest: []byte(`{}`), RawResponse: []byte(`{"choices":[{"message":{"content":"{}"}}]}`)}
	if err := recorder.RecordModelCall(context.Background(), request, result); err == nil {
		t.Fatal("mismatched call identity accepted")
	}
}

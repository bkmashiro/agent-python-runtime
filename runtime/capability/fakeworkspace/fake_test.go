package fakeworkspace_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability/fakeworkspace"
	"github.com/bkmashiro/agent-python-runtime/runtime/transaction"
)

const (
	revision = "0123456789abcdef0123456789abcdef01234567"
	catalog  = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

type testIDs struct{ next int }

func (source *testIDs) New(prefix string) (string, error) {
	source.next++
	return fmt.Sprintf("%s_%d", prefix, source.next), nil
}

type brokerFixture struct {
	broker *capability.Broker
	next   int
}

func buildBroker(t *testing.T, store *fakeworkspace.Store, binding fakeworkspace.Binding) brokerFixture {
	t.Helper()
	specs, err := fakeworkspace.HandlerSpecs(store, binding)
	if err != nil {
		t.Fatal(err)
	}
	registry := capability.NewRegistry()
	grants := map[string]capability.ToolGrant{}
	for _, spec := range specs {
		if err := registry.Register(spec); err != nil {
			t.Fatal(err)
		}
		grants[spec.ToolID] = capability.ToolGrant{ToolID: spec.ToolID, HandlerVersion: spec.HandlerVersion, EffectClass: "read_only", Policy: "AUTO_COMMIT", PolicyVersion: "workspace:v1", MaxCalls: 16}
	}
	now := time.Unix(500, 0).UTC()
	coordinator := transaction.NewCoordinator(transaction.NewMemoryLedger(), &testIDs{}, func() time.Time { return now }, nil)
	tx, err := coordinator.Begin(transaction.BeginRequest{RunID: binding.RunIdentity, CatalogDigest: catalog, Mode: transaction.TransactionModeWorkflow})
	if err != nil {
		t.Fatal(err)
	}
	binder, err := capability.NewCoordinatorBinder(coordinator, tx.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	broker, err := capability.NewBroker(capability.Config{RunIdentity: binding.RunIdentity, CatalogDigest: catalog, Registry: registry, Binder: binder, ToolGrants: grants, MaxTransactionCalls: 32}, capability.FetcherFunc(func(context.Context, capability.ResolvedRequest, uint32) (capability.FetchOutput, error) {
		return capability.FetchOutput{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	return brokerFixture{broker: broker}
}

type toolResponse struct {
	Status string          `json:"status"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code string `json:"code"`
	} `json:"error"`
}

func (fixture *brokerFixture) call(t *testing.T, toolID string, arguments any) toolResponse {
	t.Helper()
	fixture.next++
	argumentBytes, err := json.Marshal(arguments)
	if err != nil {
		t.Fatal(err)
	}
	envelope := map[string]any{"call_id": fmt.Sprintf("call:%d", fixture.next), "capability": toolID, "catalog_digest": catalog, "handler_version": fakeworkspace.HandlerVersion, "arguments": json.RawMessage(argumentBytes)}
	payload, _ := json.Marshal(envelope)
	responseBytes, err := fixture.broker.Call(context.Background(), payload)
	if err != nil {
		t.Fatal(err)
	}
	var response toolResponse
	if err := json.Unmarshal(responseBytes, &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func newStore(t *testing.T, limits fakeworkspace.Limits) (*fakeworkspace.Store, map[string][]byte) {
	t.Helper()
	files := map[string][]byte{
		"README.md":    []byte("# Demo\nneedle one\n"),
		"src/main.py":  []byte("def run():\n    return 'needle two'\n"),
		"src/other.py": []byte("VALUE = 42\n"),
	}
	store, err := fakeworkspace.NewStore([]fakeworkspace.Fixture{{Alias: "demo", Revision: revision, Files: files}}, limits)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	return store, files
}

func openWorkspace(t *testing.T, broker *brokerFixture) map[string]any {
	t.Helper()
	response := broker.call(t, fakeworkspace.RepoOpenToolID, map[string]any{"alias": "demo", "revision": revision})
	if response.Status != "ok" || response.Error != nil {
		t.Fatalf("open response=%+v", response)
	}
	var result map[string]any
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestFakeWorkspaceOpenSearchReadAndReplay(t *testing.T) {
	store, sourceFiles := newStore(t, fakeworkspace.DefaultLimits())
	broker := buildBroker(t, store, fakeworkspace.Binding{RunIdentity: "run:workspace", TaskIdentity: "task:workspace"})
	opened := openWorkspace(t, &broker)
	workspaceID := opened["workspace_id"].(string)
	if opened["resolved_revision"] != revision || opened["file_count"] != float64(3) || opened["manifest_digest"] == "" {
		t.Fatalf("opened=%+v", opened)
	}

	// The fixture is cloned into Host-owned storage.
	sourceFiles["README.md"][0] = 'X'
	search := broker.call(t, fakeworkspace.WorkspaceSearchToolID, map[string]any{"workspace_id": workspaceID, "query": "needle", "max_results": 10})
	if search.Status != "ok" {
		t.Fatalf("search=%+v", search)
	}
	var searchResult struct {
		Matches []struct {
			Path   string `json:"path"`
			Line   uint32 `json:"line"`
			Column uint32 `json:"column"`
		} `json:"matches"`
		Truncated bool `json:"truncated"`
	}
	if json.Unmarshal(search.Result, &searchResult) != nil || len(searchResult.Matches) != 2 || searchResult.Matches[0].Path != "README.md" || searchResult.Matches[0].Line != 2 || searchResult.Truncated {
		t.Fatalf("search result=%s", search.Result)
	}
	read := broker.call(t, fakeworkspace.WorkspaceReadManyToolID, map[string]any{"workspace_id": workspaceID, "paths": []string{"README.md", "src/main.py"}})
	var readResult struct {
		Items []struct {
			Path    string `json:"path"`
			Content string `json:"content"`
			SHA256  string `json:"sha256"`
		} `json:"items"`
		TotalBytes uint64 `json:"total_bytes"`
	}
	if read.Status != "ok" || json.Unmarshal(read.Result, &readResult) != nil || len(readResult.Items) != 2 || readResult.Items[0].Content != "# Demo\nneedle one\n" || readResult.Items[0].SHA256 == "" || readResult.TotalBytes == 0 {
		t.Fatalf("read=%+v result=%s", read, read.Result)
	}
	if err := broker.broker.FinalizeRun(context.Background(), true, "success"); err != nil {
		t.Fatal(err)
	}
	inspection, err := broker.broker.InspectTransaction()
	if err != nil || inspection.Transaction.State != transaction.TransactionCommitted || len(inspection.Operations) != 3 {
		t.Fatalf("inspection=%+v err=%v", inspection, err)
	}
}

func TestFakeWorkspaceOpenReplayDoesNotDuplicateOperation(t *testing.T) {
	store, _ := newStore(t, fakeworkspace.DefaultLimits())
	fixture := buildBroker(t, store, fakeworkspace.Binding{RunIdentity: "run:replay", TaskIdentity: "task:replay"})
	payload := []byte(`{"call_id":"call:fixed","capability":"repo.open","catalog_digest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","handler_version":"fake-workspace-v1","arguments":{"alias":"demo","revision":"0123456789abcdef0123456789abcdef01234567"}}`)
	first, err := fixture.broker.Call(context.Background(), payload)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fixture.broker.Call(context.Background(), payload)
	if err != nil || string(first) != string(second) {
		t.Fatalf("first=%s second=%s err=%v", first, second, err)
	}
	inspection, err := fixture.broker.InspectTransaction()
	if err != nil || len(inspection.Operations) != 1 || len(inspection.Attempts) != 1 {
		t.Fatalf("inspection=%+v err=%v", inspection, err)
	}
}

func TestFakeWorkspaceDeniesTraversalUnknownFixtureAndCrossRunAccess(t *testing.T) {
	store, _ := newStore(t, fakeworkspace.DefaultLimits())
	owner := buildBroker(t, store, fakeworkspace.Binding{RunIdentity: "run:owner", TaskIdentity: "task:owner"})
	workspaceID := openWorkspace(t, &owner)["workspace_id"].(string)
	traversal := owner.call(t, fakeworkspace.WorkspaceReadManyToolID, map[string]any{"workspace_id": workspaceID, "paths": []string{"../secret"}})
	if traversal.Error == nil || traversal.Error.Code != "path_denied" {
		t.Fatalf("traversal=%+v", traversal)
	}
	missingBroker := buildBroker(t, store, fakeworkspace.Binding{RunIdentity: "run:missing", TaskIdentity: "task:missing"})
	missing := missingBroker.call(t, fakeworkspace.RepoOpenToolID, map[string]any{"alias": "unknown", "revision": revision})
	if missing.Error == nil || missing.Error.Code != "fixture_denied" {
		t.Fatalf("missing=%+v", missing)
	}
	otherTask := buildBroker(t, store, fakeworkspace.Binding{RunIdentity: "run:owner", TaskIdentity: "task:other"})
	crossTask := otherTask.call(t, fakeworkspace.WorkspaceReadManyToolID, map[string]any{"workspace_id": workspaceID, "paths": []string{"README.md"}})
	if crossTask.Error == nil || crossTask.Error.Code != "workspace_denied" {
		t.Fatalf("cross task=%+v", crossTask)
	}
	other := buildBroker(t, store, fakeworkspace.Binding{RunIdentity: "run:other", TaskIdentity: "task:other"})
	crossRun := other.call(t, fakeworkspace.WorkspaceReadManyToolID, map[string]any{"workspace_id": workspaceID, "paths": []string{"README.md"}})
	if crossRun.Error == nil || crossRun.Error.Code != "workspace_denied" {
		t.Fatalf("cross run=%+v", crossRun)
	}
}

func TestFakeWorkspaceEnforcesReadAndSearchQuotas(t *testing.T) {
	limits := fakeworkspace.DefaultLimits()
	limits.MaxReadPaths = 1
	limits.MaxReadBytes = 20
	limits.MaxSearchResults = 1
	store, _ := newStore(t, limits)
	tooManyBroker := buildBroker(t, store, fakeworkspace.Binding{RunIdentity: "run:quota-many", TaskIdentity: "task:quota-many"})
	tooManyWorkspace := openWorkspace(t, &tooManyBroker)["workspace_id"].(string)
	tooMany := tooManyBroker.call(t, fakeworkspace.WorkspaceReadManyToolID, map[string]any{"workspace_id": tooManyWorkspace, "paths": []string{"README.md", "src/main.py"}})
	if tooMany.Error == nil || tooMany.Error.Code != "quota_exceeded" {
		t.Fatalf("too many=%+v", tooMany)
	}
	tooLargeBroker := buildBroker(t, store, fakeworkspace.Binding{RunIdentity: "run:quota-large", TaskIdentity: "task:quota-large"})
	tooLargeWorkspace := openWorkspace(t, &tooLargeBroker)["workspace_id"].(string)
	tooLarge := tooLargeBroker.call(t, fakeworkspace.WorkspaceReadManyToolID, map[string]any{"workspace_id": tooLargeWorkspace, "paths": []string{"src/main.py"}})
	if tooLarge.Error == nil || tooLarge.Error.Code != "quota_exceeded" {
		t.Fatalf("too large=%+v", tooLarge)
	}
	searchBroker := buildBroker(t, store, fakeworkspace.Binding{RunIdentity: "run:quota-search", TaskIdentity: "task:quota-search"})
	searchWorkspace := openWorkspace(t, &searchBroker)["workspace_id"].(string)
	search := searchBroker.call(t, fakeworkspace.WorkspaceSearchToolID, map[string]any{"workspace_id": searchWorkspace, "query": "needle", "max_results": 1})
	var result struct {
		Matches   []json.RawMessage `json:"matches"`
		Truncated bool              `json:"truncated"`
	}
	if search.Status != "ok" || json.Unmarshal(search.Result, &result) != nil || len(result.Matches) != 1 || !result.Truncated {
		t.Fatalf("search=%+v result=%s", search, search.Result)
	}
}

func TestFakeWorkspaceRejectsUnsafeFixtureAtConstruction(t *testing.T) {
	limits := fakeworkspace.DefaultLimits()
	cases := []fakeworkspace.Fixture{
		{Alias: "demo", Revision: revision, Files: map[string][]byte{"../escape": []byte("x")}},
		{Alias: "demo/other", Revision: revision, Files: map[string][]byte{"ok": []byte("x")}},
		{Alias: "demo", Revision: "main", Files: map[string][]byte{"ok": []byte("x")}},
		{Alias: "demo", Revision: revision, Files: map[string][]byte{"binary": {0xff}}},
	}
	for index, fixture := range cases {
		if _, err := fakeworkspace.NewStore([]fakeworkspace.Fixture{fixture}, limits); err == nil {
			t.Fatalf("case %d accepted", index)
		}
	}
}

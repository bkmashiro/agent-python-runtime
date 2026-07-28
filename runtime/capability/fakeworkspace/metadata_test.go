package fakeworkspace_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability/fakeworkspace"
)

func TestFakeWorkspaceManifestListGlobAndStatAreBoundedAndDeterministic(t *testing.T) {
	now := time.Unix(700, 0).UTC()
	store, err := fakeworkspace.NewStoreWithClock([]fakeworkspace.Fixture{{Alias: "demo", Revision: revision, Files: map[string][]byte{
		"README.md": []byte("readme\n"), "docs/guide.md": []byte("guide\n"), "src/main.py": []byte("main\n"), "src/other.py": []byte("other\n"),
	}}}, fakeworkspace.DefaultLimits(), func() time.Time { return now }, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	broker := buildBroker(t, store, fakeworkspace.Binding{RunIdentity: "run:metadata", TaskIdentity: "task:metadata"})
	opened := openWorkspace(t, &broker)
	workspaceID := opened["workspace_id"].(string)
	if opened["expires_at"] == "" {
		t.Fatalf("open omitted expiry: %+v", opened)
	}

	manifest := broker.call(t, fakeworkspace.RepoManifestToolID, map[string]any{"workspace_id": workspaceID, "cursor": 0, "limit": 2})
	var manifestResult struct {
		Alias          string `json:"alias"`
		Revision       string `json:"revision"`
		ManifestDigest string `json:"manifest_digest"`
		Files          []struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
			Bytes  uint64 `json:"bytes"`
		} `json:"files"`
		NextCursor *uint32 `json:"next_cursor"`
	}
	if manifest.Status != "ok" || json.Unmarshal(manifest.Result, &manifestResult) != nil || manifestResult.Alias != "demo" || manifestResult.Revision != revision || len(manifestResult.Files) != 2 || manifestResult.Files[0].Path != "README.md" || manifestResult.Files[1].Path != "docs/guide.md" || manifestResult.Files[0].SHA256 == "" || manifestResult.NextCursor == nil || *manifestResult.NextCursor != 2 {
		t.Fatalf("manifest=%+v result=%s", manifest, manifest.Result)
	}

	listed := broker.call(t, fakeworkspace.WorkspaceListToolID, map[string]any{"workspace_id": workspaceID, "prefix": "src/", "cursor": 0, "limit": 10})
	var listResult struct {
		Paths      []string `json:"paths"`
		NextCursor *uint32  `json:"next_cursor"`
	}
	if listed.Status != "ok" || json.Unmarshal(listed.Result, &listResult) != nil || len(listResult.Paths) != 2 || listResult.Paths[0] != "src/main.py" || listResult.Paths[1] != "src/other.py" || listResult.NextCursor != nil {
		t.Fatalf("list=%+v result=%s", listed, listed.Result)
	}

	globbed := broker.call(t, fakeworkspace.WorkspaceGlobToolID, map[string]any{"workspace_id": workspaceID, "pattern": "src/*.py", "max_results": 10})
	var globResult struct {
		Paths     []string `json:"paths"`
		Truncated bool     `json:"truncated"`
	}
	if globbed.Status != "ok" || json.Unmarshal(globbed.Result, &globResult) != nil || len(globResult.Paths) != 2 || globResult.Paths[0] != "src/main.py" || globResult.Truncated {
		t.Fatalf("glob=%+v result=%s", globbed, globbed.Result)
	}

	stat := broker.call(t, fakeworkspace.WorkspaceStatManyToolID, map[string]any{"workspace_id": workspaceID, "paths": []string{"src/other.py", "README.md"}})
	var statResult struct {
		Items []struct {
			Path   string `json:"path"`
			Bytes  uint64 `json:"bytes"`
			SHA256 string `json:"sha256"`
		} `json:"items"`
	}
	if stat.Status != "ok" || json.Unmarshal(stat.Result, &statResult) != nil || len(statResult.Items) != 2 || statResult.Items[0].Path != "src/other.py" || statResult.Items[0].Bytes != 6 || statResult.Items[0].SHA256 == "" {
		t.Fatalf("stat=%+v result=%s", stat, stat.Result)
	}
}

func TestFakeWorkspaceExpiryFailsClosedAndReopenRefreshesLease(t *testing.T) {
	now := time.Unix(800, 0).UTC()
	store, err := fakeworkspace.NewStoreWithClock([]fakeworkspace.Fixture{{Alias: "demo", Revision: revision, Files: map[string][]byte{"README.md": []byte("readme\n")}}}, fakeworkspace.DefaultLimits(), func() time.Time { return now }, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	broker := buildBroker(t, store, fakeworkspace.Binding{RunIdentity: "run:expiry", TaskIdentity: "task:expiry"})
	workspaceID := openWorkspace(t, &broker)["workspace_id"].(string)
	now = now.Add(time.Minute)
	expired := broker.call(t, fakeworkspace.WorkspaceStatManyToolID, map[string]any{"workspace_id": workspaceID, "paths": []string{"README.md"}})
	if expired.Error == nil || expired.Error.Code != "workspace_expired" {
		t.Fatalf("expired=%+v", expired)
	}
	broker = buildBroker(t, store, fakeworkspace.Binding{RunIdentity: "run:expiry", TaskIdentity: "task:expiry"})
	reopened := openWorkspace(t, &broker)
	if reopened["workspace_id"] != workspaceID {
		t.Fatalf("reopen changed immutable identity: old=%s new=%v", workspaceID, reopened["workspace_id"])
	}
	stat := broker.call(t, fakeworkspace.WorkspaceStatManyToolID, map[string]any{"workspace_id": workspaceID, "paths": []string{"README.md"}})
	if stat.Status != "ok" {
		t.Fatalf("stat after reopen=%+v", stat)
	}
}

func TestFakeWorkspaceMetadataRejectsUnsafeGlobAndPagination(t *testing.T) {
	store, _ := newStore(t, fakeworkspace.DefaultLimits())
	for index, arguments := range []map[string]any{
		{"workspace_id": "placeholder", "pattern": "../*", "max_results": 10},
		{"workspace_id": "placeholder", "pattern": "[", "max_results": 10},
	} {
		binding := fakeworkspace.Binding{RunIdentity: "run:metadata-denied-" + string(rune('a'+index)), TaskIdentity: "task:metadata-denied"}
		broker := buildBroker(t, store, binding)
		workspaceID := openWorkspace(t, &broker)["workspace_id"].(string)
		arguments["workspace_id"] = workspaceID
		response := broker.call(t, fakeworkspace.WorkspaceGlobToolID, arguments)
		if response.Error == nil || response.Error.Code != "path_denied" {
			t.Fatalf("unsafe glob accepted: %+v", response)
		}
	}
	broker := buildBroker(t, store, fakeworkspace.Binding{RunIdentity: "run:metadata-cursor", TaskIdentity: "task:metadata-cursor"})
	workspaceID := openWorkspace(t, &broker)["workspace_id"].(string)
	manifest := broker.call(t, fakeworkspace.RepoManifestToolID, map[string]any{"workspace_id": workspaceID, "cursor": 999, "limit": 10})
	if manifest.Error == nil || manifest.Error.Code != "quota_exceeded" {
		t.Fatalf("invalid cursor=%+v", manifest)
	}
}

func TestFakeWorkspaceLeaseAdmissionIsBounded(t *testing.T) {
	limits := fakeworkspace.DefaultLimits()
	limits.MaxWorkspaces = 1
	store, _ := newStore(t, limits)
	first := buildBroker(t, store, fakeworkspace.Binding{RunIdentity: "run:lease-a", TaskIdentity: "task:lease"})
	openWorkspace(t, &first)
	second := buildBroker(t, store, fakeworkspace.Binding{RunIdentity: "run:lease-b", TaskIdentity: "task:lease"})
	response := second.call(t, fakeworkspace.RepoOpenToolID, map[string]any{"alias": "demo", "revision": revision})
	if response.Error == nil || response.Error.Code != "quota_exceeded" {
		t.Fatalf("second workspace lease admitted: %+v", response)
	}
}

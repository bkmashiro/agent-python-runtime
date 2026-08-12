package capability_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

func TestGitReadToolsExposeBoundedLocalSemanticsWithoutRepositoryPath(t *testing.T) {
	repositoryPath := t.TempDir()
	repository, err := git.PlainInit(repositoryPath, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repositoryPath, "README.md"), []byte("first\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	worktree, err := repository.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := worktree.Add("README.md"); err != nil {
		t.Fatal(err)
	}
	first, err := worktree.Commit("initial", &git.CommitOptions{Author: &object.Signature{Name: "Test", Email: "test@example.invalid", When: time.Unix(1, 0)}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repositoryPath, "README.md"), []byte("second\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	registry := capability.NewRegistry()
	policy := capability.GitReadPolicy{RepositoryID: "fixture-repo", RepositoryPath: repositoryPath, MaxEntries: 32, MaxPatchBytes: 4096, MaxBlobBytes: 4096}
	if err := capability.RegisterGitReadTools(registry, policy); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 8})
	if err != nil {
		t.Fatal(err)
	}
	if stringValue := plan.PythonPrelude(); !containsAll(stringValue, "git.status", "git.diff", "git.log", "git.show", "git.list_refs", "git.resolve_revision") || containsAll(stringValue, repositoryPath) {
		t.Fatalf("unsafe or incomplete projection: %s", stringValue)
	}
	for _, spec := range plan.Specs() {
		encoded, _ := json.Marshal(spec)
		if containsAll(string(encoded), repositoryPath) {
			t.Fatalf("repository path leaked: %s", encoded)
		}
	}
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "git-read", Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	call := func(id, name, arguments string) map[string]any {
		payload := []byte(`{"call_id":"` + id + `","capability":"` + name + `","arguments":` + arguments + `}`)
		response, callErr := broker.Call(context.Background(), payload)
		if callErr != nil {
			t.Fatal(callErr)
		}
		var envelope struct {
			Status string         `json:"status"`
			Result map[string]any `json:"result"`
			Error  any            `json:"error"`
		}
		if err := json.Unmarshal(response, &envelope); err != nil || envelope.Status != "ok" {
			t.Fatalf("%s: %s err=%v", name, response, err)
		}
		return envelope.Result
	}
	status := call("status", "git.status", `{}`)
	if status["clean"] != false || status["head"] != first.String() || len(status["changes"].([]any)) != 1 {
		t.Fatalf("status=%v", status)
	}
	patch := call("diff", "git.diff", `{}`)
	if !containsAll(patch["patch"].(string), "-first", "+second") {
		t.Fatalf("patch=%v", patch)
	}
	log := call("log", "git.log", `{"limit":10}`)
	if len(log["commits"].([]any)) != 1 {
		t.Fatalf("log=%v", log)
	}
	show := call("show", "git.show", `{"revision":"HEAD","path":"README.md"}`)
	if show["content"] != "first\n" {
		t.Fatalf("show=%v", show)
	}
	refs := call("refs", "git.list_refs", `{}`)
	if len(refs["refs"].([]any)) == 0 {
		t.Fatalf("refs=%v", refs)
	}
	resolved := call("resolve", "git.resolve_revision", `{"revision":"HEAD"}`)
	if resolved["commit"] != first.String() {
		t.Fatalf("resolved=%v", resolved)
	}
}

func TestGitReadToolsRejectWorktreeSymlinks(t *testing.T) {
	repositoryPath := t.TempDir()
	repository, err := git.PlainInit(repositoryPath, false)
	if err != nil {
		t.Fatal(err)
	}
	tracked := filepath.Join(repositoryPath, "tracked.txt")
	if err := os.WriteFile(tracked, []byte("tracked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	worktree, _ := repository.Worktree()
	_, _ = worktree.Add("tracked.txt")
	_, err = worktree.Commit("initial", &git.CommitOptions{Author: &object.Signature{Name: "Test", Email: "test@example.invalid", When: time.Unix(1, 0)}})
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(tracked); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, tracked); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	registry := capability.NewRegistry()
	if err := capability.RegisterGitReadTools(registry, capability.GitReadPolicy{RepositoryID: "fixture", RepositoryPath: repositoryPath, MaxEntries: 8, MaxPatchBytes: 4096, MaxBlobBytes: 4096}); err != nil {
		t.Fatal(err)
	}
	plan, _ := registry.Seal(capability.PlanConfig{MaxCalls: 1})
	broker, _ := capability.NewBroker(capability.Config{RunIdentity: "symlink", Plan: plan})
	response, err := broker.Call(context.Background(), []byte(`{"call_id":"diff","capability":"git.diff","arguments":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(response), `"status":"error"`) || strings.Contains(string(response), outside) {
		t.Fatalf("unsafe symlink response: %s", response)
	}
}

func TestGitReadToolsRejectInvalidPolicyAndBoundExhaustion(t *testing.T) {
	if err := capability.RegisterGitReadTools(capability.NewRegistry(), capability.GitReadPolicy{RepositoryID: "bad", RepositoryPath: "relative"}); err != capability.ErrInvalidTool {
		t.Fatalf("error=%v", err)
	}
}

func containsAll(value string, fragments ...string) bool {
	for _, fragment := range fragments {
		if !strings.Contains(value, fragment) {
			return false
		}
	}
	return true
}

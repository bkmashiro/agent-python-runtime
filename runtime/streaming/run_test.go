package streaming

import (
	"context"
	"errors"
	"os"
	"testing"

	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

type runnerStub struct {
	response []byte
	err      error
}

func (runner runnerStub) Run(context.Context, []byte, string) ([]byte, error) {
	return runner.response, runner.err
}
func (runnerStub) Close(context.Context) error { return nil }
func (runnerStub) Properties() enginecontract.Properties {
	return enginecontract.Properties{Backend: "stub"}
}

func testManager(t *testing.T) *workspace.Manager {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	manager, err := workspace.NewManager(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	return manager
}

func TestExecutePublishesOnlySuccessfulGuestResponse(t *testing.T) {
	for name, response := range map[string]runnerStub{
		"transport failure": {err: errors.New("guest failed")},
		"invalid response":  {response: []byte(`{"status":"error"}`)},
		"malformed":         {response: []byte(`{`)},
	} {
		t.Run(name, func(t *testing.T) {
			manager := testManager(t)
			base, err := manager.Create([]workspace.InitialFile{{Path: "base.txt", Data: []byte("base")}}, workspace.DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			attempt, err := manager.ForkAttempt(base)
			if err != nil {
				t.Fatal(err)
			}
			ref := attempt.Ref()
			if _, err := Execute(context.Background(), response, attempt, []byte(`{}`), ""); err == nil {
				t.Fatal("failed stream published")
			}
			if _, err := manager.Acquire(ref, "discard-check"); !errors.Is(err, workspace.ErrWorkspaceNotFound) {
				t.Fatalf("failed attempt remains: %v", err)
			}
		})
	}
}

func TestExecutePublishesSuccessfulAttemptIdentity(t *testing.T) {
	manager := testManager(t)
	base, _ := manager.Create(nil, workspace.DefaultLimits())
	attempt, _ := manager.ForkAttempt(base)
	result, err := Execute(context.Background(), runnerStub{response: []byte(`{"status":"ok","result":1}`)}, attempt, []byte(`{}`), "")
	if err != nil || result.PublishedWorkspace != attempt.Ref() || result.PublishedWorkspace == base {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

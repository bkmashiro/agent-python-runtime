package capability_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

func TestWorkspaceToolsAreTypedAndPathLocal(t *testing.T) {
	workspace, err := capability.NewWorkspace(map[string]string{"input.txt": "hello"})
	if err != nil {
		t.Fatal(err)
	}
	registry := capability.NewRegistry()
	if err := capability.RegisterWorkspaceTools(registry, workspace); err != nil {
		t.Fatal(err)
	}
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "run", MaxCalls: 3, Registry: registry})
	if err != nil {
		t.Fatal(err)
	}
	call := func(id, tool, arguments string) map[string]any {
		payload := []byte(`{"call_id":"` + id + `","capability":"` + tool + `","arguments":` + arguments + `}`)
		response, callErr := broker.Call(context.Background(), payload)
		if callErr != nil {
			t.Fatal(callErr)
		}
		var decoded map[string]any
		if json.Unmarshal(response, &decoded) != nil {
			t.Fatal("invalid response")
		}
		return decoded
	}
	if response := call("read", "workspace.read_text", `{"path":"input.txt"}`); response["status"] != "ok" {
		t.Fatalf("read=%v", response)
	}
	if response := call("write", "workspace.write_text", `{"path":"output.txt","content":"done"}`); response["status"] != "ok" {
		t.Fatalf("write=%v", response)
	}
	if response := call("escape", "workspace.read_text", `{"path":"../secret"}`); response["status"] != "error" {
		t.Fatalf("escape=%v", response)
	}
	if workspace.Snapshot()["output.txt"] != "done" {
		t.Fatalf("workspace=%v", workspace.Snapshot())
	}
}

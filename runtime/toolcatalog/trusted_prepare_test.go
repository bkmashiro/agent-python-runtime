package toolcatalog

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratedTrustedPrepareInstallsHostToolsAndRoutesStableEnvelope(t *testing.T) {
	snapshot, err := BuildSnapshot([]DiscoveredTool{{
		ToolID:         "demo.echo",
		ServerID:       "fixture",
		Name:           "echo",
		HandlerVersion: "v1",
		InputSchema:    json.RawMessage(`{"type":"object","additionalProperties":false,"required":["text"],"properties":{"text":{"type":"string"}}}`),
		OutputSchema:   json.RawMessage(`{"type":"object"}`),
	}}, map[string]Grant{"demo.echo": {ToolID: "demo.echo", EffectClass: "read_only", Policy: "AUTO_COMMIT", GrantVersion: "g1", MaxCalls: 2}}, BuildOptions{Revision: 1})
	if err != nil {
		t.Fatal(err)
	}
	prepare, err := snapshot.GenerateTrustedPrepare()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prepare, `echo = _host_module.__dict__["echo"]`) {
		t.Fatal("legacy trusted prepare unexpectedly prebound a tool")
	}
	path := filepath.Join(t.TempDir(), "prepare.py")
	if err := os.WriteFile(path, []byte(prepare), 0o600); err != nil {
		t.Fatal(err)
	}
	probe := `import json, runpy, sys, types
seen=[]
host=types.ModuleType("_agent_runtime_host")
def call(payload):
    request=json.loads(payload); seen.append(request)
    return json.dumps({"call_id":request["call_id"],"status":"ok","result":{"text":request["arguments"]["text"]},"error":None})
host.call=call
sys.modules["_agent_runtime_host"]=host
runpy.run_path(sys.argv[1])
from host_tools import echo, CATALOG_DIGEST, current_transaction
transaction = current_transaction()
assert transaction == {"scope":"current","authority":"host-owned","lifecycle":"host-managed","catalog_digest":CATALOG_DIGEST}
assert not any(key.endswith("_id") for key in transaction)
assert echo(text="hello") == {"text":"hello"}
assert seen == [{"call_id":"typed:1","capability":"demo.echo","catalog_digest":CATALOG_DIGEST,"handler_version":"v1","arguments":{"text":"hello"}}]
def denied(payload):
    request=json.loads(payload)
    return json.dumps({"call_id":request["call_id"],"status":"denied","result":None,"error":{"code":"capability_denied","message":"no"}})
host.call=denied
try: echo(text="again")
except Exception as error:
    assert type(error).__name__ == "HostToolError" and error.code == "capability_denied"
else: raise AssertionError("denial did not raise")
def malformed(payload):
    request=json.loads(payload)
    return json.dumps({"call_id":request["call_id"],"status":"error","result":None,"error":{"code":[],"message":{}}})
host.call=malformed
try: echo(text="bad")
except Exception as error:
    assert type(error).__name__ == "HostToolError" and error.code == "protocol_error"
else: raise AssertionError("malformed error did not fail closed")
`
	if output, err := exec.Command("python3", "-c", probe, path).CombinedOutput(); err != nil {
		t.Fatalf("trusted prepare failed: %v\n%s", err, output)
	}
}

func TestGeneratedTrustedPrepareWithToolBindingsSupportsDirectCallsAndImport(t *testing.T) {
	snapshot, err := BuildSnapshot([]DiscoveredTool{{
		ToolID:         "demo.echo",
		ServerID:       "fixture",
		Name:           "echo",
		HandlerVersion: "v1",
		InputSchema:    json.RawMessage(`{"type":"object","additionalProperties":false,"required":["text"],"properties":{"text":{"type":"string"}}}`),
		OutputSchema:   json.RawMessage(`{"type":"object"}`),
	}}, map[string]Grant{"demo.echo": {ToolID: "demo.echo", EffectClass: "read_only", Policy: "AUTO_COMMIT", GrantVersion: "g1", MaxCalls: 2}}, BuildOptions{Revision: 1})
	if err != nil {
		t.Fatal(err)
	}
	prepare, err := snapshot.GenerateTrustedPrepareWithToolBindings()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "prepare.py")
	if err := os.WriteFile(path, []byte(prepare), 0o600); err != nil {
		t.Fatal(err)
	}
	probe := `import json, sys, types
seen = []
host = types.ModuleType("_agent_runtime_host")

def call(payload):
    request = json.loads(payload)
    seen.append(request)
    return json.dumps({
        "call_id": request["call_id"],
        "status": "ok",
        "result": {"text": request["arguments"]["text"]},
        "error": None,
    })

host.call = call
sys.modules["_agent_runtime_host"] = host

exec(compile(open(sys.argv[1]).read(), "<trusted_prepare>", "exec"), globals())

direct = echo(text="hello")
from host_tools import echo as imported_echo
imported = imported_echo(text="hello")

assert direct == {"text":"hello"}
assert imported == {"text":"hello"}
assert len(seen) == 2
assert seen[0]["capability"] == seen[1]["capability"] == "demo.echo"
assert seen[0]["catalog_digest"] == seen[1]["catalog_digest"]
assert seen[0]["handler_version"] == seen[1]["handler_version"] == "v1"
assert seen[0]["arguments"] == seen[1]["arguments"] == {"text":"hello"}
assert seen[0]["call_id"] != seen[1]["call_id"]
assert seen[0]["call_id"].startswith("typed:") and seen[1]["call_id"].startswith("typed:")
print("ok")
`
	if output, err := exec.Command("python3", "-c", probe, path).CombinedOutput(); err != nil {
		t.Fatalf("trusted prepare with tool bindings failed: %v\n%s", err, output)
	}
}

func TestGenerateTrustedPrepareWithToolBindingsRejectsConflictingPythonGlobalNames(t *testing.T) {
	for _, name := range []string{"input", "inputs", "result", "open", "print", "host_tools", "aiter", "ExceptionGroup", "PythonFinalizationError"} {
		t.Run(name, func(t *testing.T) {
			snapshot, err := BuildSnapshot([]DiscoveredTool{{
				ToolID:         "demo.conflict." + name,
				ServerID:       "fixture",
				Name:           name,
				HandlerVersion: "v1",
				InputSchema:    json.RawMessage(`{"type":"object","additionalProperties":false,"required":["text"],"properties":{"text":{"type":"string"}}}`),
				OutputSchema:   json.RawMessage(`{"type":"object"}`),
			}}, map[string]Grant{"demo.conflict." + name: {ToolID: "demo.conflict." + name, EffectClass: "read_only", Policy: "AUTO_COMMIT", GrantVersion: "g1", MaxCalls: 1}}, BuildOptions{Revision: 1})
			if err != nil {
				t.Fatalf("setup snapshot failed for %q: %v", name, err)
			}
			if _, err := snapshot.GenerateTrustedPrepareWithToolBindings(); err == nil {
				t.Fatalf("expected conflict rejection for tool name %q", name)
			}
		})
	}
}

func TestGenerateTrustedPrepareWithToolBindingsSupportsEmptySnapshot(t *testing.T) {
	snapshot, err := BuildSnapshot(nil, nil, BuildOptions{Revision: 2})
	if err != nil {
		t.Fatal(err)
	}
	prepare, err := snapshot.GenerateTrustedPrepareWithToolBindings()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prepare, "import sys as _host_sys") || !strings.Contains(prepare, "_host_sys.modules[\"host_tools\"]") {
		t.Fatalf("unexpected prepare output for empty snapshot")
	}
	if !strings.Contains(prepare, "del _host_module, _host_types, _host_sys") {
		t.Fatalf("prepare output should preserve cleanup contract")
	}
	path := filepath.Join(t.TempDir(), "prepare.py")
	if err := os.WriteFile(path, []byte(prepare), 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("python3", "-m", "py_compile", path).CombinedOutput(); err != nil {
		t.Fatalf("empty snapshot prepare must compile: %v\n%s", err, output)
	}
	probe := `import json, sys, types
host = types.ModuleType("_agent_runtime_host")
host.call = lambda payload: json.dumps({"call_id":"", "status":"ok", "result":{}, "error":None})
sys.modules["_agent_runtime_host"] = host

exec(compile(open(sys.argv[1]).read(), "<trusted_prepare>", "exec"), globals())
print("ok")
`
	if output, err := exec.Command("python3", "-c", probe, path).CombinedOutput(); err != nil {
		t.Fatalf("empty snapshot trusted prepare failed: %v\n%s", err, output)
	}
}

func TestGenerateTrustedPrepareWithToolBindingsSkipsUnsupportedProjection(t *testing.T) {
	snapshot, err := BuildSnapshot([]DiscoveredTool{{
		ToolID:         "demo.echo",
		ServerID:       "fixture",
		Name:           "echo",
		HandlerVersion: "v1",
		InputSchema:    json.RawMessage(`{"type":"object","required":["text"],"properties":{"text":{"type":"string"}}}`),
		OutputSchema:   json.RawMessage(`{"type":"object"}`),
	}, {
		ToolID:         "demo.unsupported",
		ServerID:       "fixture",
		Name:           "unsupported",
		HandlerVersion: "v1",
		InputSchema:    json.RawMessage(`{"type":"object","required":["item"],"properties":{"item":{"type":["string", "number"]}}}`),
		OutputSchema:   json.RawMessage(`{"type":"object"}`),
	}}, map[string]Grant{
		"demo.echo":        {ToolID: "demo.echo", EffectClass: "read_only", Policy: "AUTO_COMMIT", GrantVersion: "g1", MaxCalls: 1},
		"demo.unsupported": {ToolID: "demo.unsupported", EffectClass: "read_only", Policy: "AUTO_COMMIT", GrantVersion: "g2", MaxCalls: 1},
	}, BuildOptions{Revision: 1})
	if err != nil {
		t.Fatal(err)
	}
	prepare, err := snapshot.GenerateTrustedPrepareWithToolBindings()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "prepare.py")
	if err := os.WriteFile(path, []byte(prepare), 0o600); err != nil {
		t.Fatal(err)
	}
	probe := `import json, sys, types
seen = []
host = types.ModuleType("_agent_runtime_host")

def call(payload):
    request = json.loads(payload)
    seen.append(request)
    return json.dumps({
        "call_id": request["call_id"],
        "status": "ok",
        "result": request["arguments"],
        "error": None,
    })

host.call = call
sys.modules["_agent_runtime_host"] = host

exec(compile(open(sys.argv[1]).read(), "<trusted_prepare>", "exec"), globals())

echo(text="hello")
assert "unsupported" not in globals()
print("ok")
`
	if output, err := exec.Command("python3", "-c", probe, path).CombinedOutput(); err != nil {
		t.Fatalf("unsupported projection must be skipped during bindings: %v\n%s", err, output)
	}
}

func TestEmptySnapshotGeneratesDiscoverableEmptySDK(t *testing.T) {
	snapshot, err := BuildSnapshot(nil, nil, BuildOptions{Revision: 2})
	if err != nil {
		t.Fatal(err)
	}
	runtimeSource, stub, err := snapshot.GeneratePython()
	if err != nil {
		t.Fatal(err)
	}
	if runtimeSource == "" || stub == "" {
		t.Fatal("empty catalog SDK missing")
	}
}

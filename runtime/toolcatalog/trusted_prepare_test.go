package toolcatalog

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGeneratedTrustedPrepareInstallsHostToolsAndRoutesStableEnvelope(t *testing.T) {
	snapshot, err := BuildSnapshot([]DiscoveredTool{{
		ToolID: "demo.echo", ServerID: "fixture", Name: "echo", HandlerVersion: "v1",
		InputSchema:  json.RawMessage(`{"type":"object","additionalProperties":false,"required":["text"],"properties":{"text":{"type":"string"}}}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
	}}, map[string]Grant{"demo.echo": {ToolID: "demo.echo", EffectClass: "read_only", Policy: "AUTO_COMMIT", GrantVersion: "g1", MaxCalls: 2}}, BuildOptions{Revision: 1})
	if err != nil {
		t.Fatal(err)
	}
	prepare, err := snapshot.GenerateTrustedPrepare()
	if err != nil {
		t.Fatal(err)
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

package toolcatalog

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildSnapshotIsDeterministicImmutableAndChangesOnlyOnNextRun(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	discovered := []DiscoveredTool{
		{
			ToolID: "notes.search", ServerID: "notes", Name: "search-notes", Description: "Search user's notes safely.", HandlerVersion: "v1",
			InputSchema:  raw(`{"type":"object","additionalProperties":false,"required":["query"],"properties":{"query":{"type":"string"},"limit":{"type":"integer","minimum":1}}}`),
			OutputSchema: raw(`{"type":"object","additionalProperties":false,"required":["items"],"properties":{"items":{"type":"array","items":{"type":"string"}}}}`),
		},
	}
	grants := map[string]Grant{"notes.search": {ToolID: "notes.search", EffectClass: "read_only", Policy: "AUTO_COMMIT", GrantVersion: "grant_v1", MaxCalls: 4}}

	first, err := BuildSnapshot(discovered, grants, BuildOptions{Revision: 1, DiscoveredAt: now})
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildSnapshot(discovered, grants, BuildOptions{Revision: 1, DiscoveredAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest() != second.Digest() || first.SnapshotID() != second.SnapshotID() {
		t.Fatalf("nonauthority timestamp changed snapshot identity: %s vs %s", first.Digest(), second.Digest())
	}
	tools := first.Tools()
	if len(tools) != 1 || tools[0].PythonName != "search_notes" || tools[0].Projection != ProjectionLossy {
		t.Fatalf("unexpected normalized tool: %+v", tools)
	}
	tools[0].InputSchema[0] = 'x'
	if first.Tools()[0].InputSchema[0] == 'x' {
		t.Fatal("snapshot leaked mutable schema bytes")
	}

	discovered = append(discovered, DiscoveredTool{
		ToolID: "notes.get", ServerID: "notes", Name: "get", HandlerVersion: "v1",
		InputSchema:  raw(`{"type":"object","additionalProperties":false,"required":["id"],"properties":{"id":{"type":"string"}}}`),
		OutputSchema: raw(`{"type":"string"}`),
	})
	grants["notes.get"] = Grant{ToolID: "notes.get", EffectClass: "read_only", Policy: "AUTO_COMMIT", GrantVersion: "grant_v1", MaxCalls: 4}
	if len(first.Tools()) != 1 {
		t.Fatal("live snapshot changed after discovery/grant source mutation")
	}
	next, err := BuildSnapshot(discovered, grants, BuildOptions{Revision: 2, DiscoveredAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Tools()) != 2 || next.Digest() == first.Digest() {
		t.Fatalf("next Run did not observe catalog change: first=%s next=%s", first.Digest(), next.Digest())
	}
}

func TestBuildSnapshotRejectsMissingGrantCollisionAndMalformedSchema(t *testing.T) {
	base := DiscoveredTool{ToolID: "server.one", ServerID: "server", Name: "foo-bar", HandlerVersion: "v1", InputSchema: raw(`{"type":"object","properties":{}}`), OutputSchema: raw(`{"type":"object"}`)}
	if _, err := BuildSnapshot([]DiscoveredTool{base}, nil, BuildOptions{Revision: 1}); err == nil {
		t.Fatal("discovered tool without Host grant was exposed")
	}
	other := base
	other.ToolID = "server.two"
	other.Name = "foo_bar"
	grants := map[string]Grant{
		base.ToolID:  {ToolID: base.ToolID, EffectClass: "read_only", Policy: "AUTO_COMMIT", GrantVersion: "g1", MaxCalls: 4},
		other.ToolID: {ToolID: other.ToolID, EffectClass: "read_only", Policy: "AUTO_COMMIT", GrantVersion: "g1", MaxCalls: 4},
	}
	if _, err := BuildSnapshot([]DiscoveredTool{base, other}, grants, BuildOptions{Revision: 1}); err == nil {
		t.Fatal("Python name collision was accepted")
	}
	base.InputSchema = raw(`{"type":"object",`)
	if _, err := BuildSnapshot([]DiscoveredTool{base}, map[string]Grant{base.ToolID: grants[base.ToolID]}, BuildOptions{Revision: 1}); err == nil {
		t.Fatal("malformed schema was accepted")
	}
	base.InputSchema = raw(`{"type":"not-a-json-schema-type"}`)
	if _, err := BuildSnapshot([]DiscoveredTool{base}, map[string]Grant{base.ToolID: grants[base.ToolID]}, BuildOptions{Revision: 1}); err == nil {
		t.Fatal("semantically invalid JSON Schema was accepted")
	}
	base.InputSchema = raw(`{"type":"object","properties":{}}`)
	base.ToolID, base.Name = "fetch_many", "fetch_many"
	if _, err := BuildSnapshot([]DiscoveredTool{base}, map[string]Grant{"fetch_many": {ToolID: "fetch_many", EffectClass: "read_only", Policy: "AUTO_COMMIT", GrantVersion: "g1", MaxCalls: 1}}, BuildOptions{Revision: 1}); err == nil {
		t.Fatal("legacy fetch_many was accepted as a typed catalog tool")
	}
	base.ToolID, base.Name = "server.reserved", "CATALOG_DIGEST"
	if _, err := BuildSnapshot([]DiscoveredTool{base}, map[string]Grant{base.ToolID: {ToolID: base.ToolID, EffectClass: "read_only", Policy: "AUTO_COMMIT", GrantVersion: "g1", MaxCalls: 1}}, BuildOptions{Revision: 1}); err == nil {
		t.Fatal("generated Python reserved name was accepted")
	}
}

func TestProjectionAndGeneratedSurfacesShareOneIR(t *testing.T) {
	discovered := []DiscoveredTool{
		{
			ToolID: "demo.echo", ServerID: "demo", Name: "echo", Description: "Echo a value.\nNo authority.", HandlerVersion: "v7",
			InputSchema:  raw(`{"type":"object","additionalProperties":false,"required":["text","mode"],"properties":{"text":{"type":"string"},"mode":{"type":"string","enum":["short","long"]},"tags":{"type":"array","items":{"type":"string"}}}}`),
			OutputSchema: raw(`{"type":"string"}`),
		},
		{
			ToolID: "demo.ref", ServerID: "demo", Name: "ref-tool", HandlerVersion: "v1",
			InputSchema:  raw(`{"type":"object","properties":{"x":{"$ref":"#/$defs/X"}},"$defs":{"X":{"type":"string"}}}`),
			OutputSchema: raw(`{"type":"object"}`),
		},
	}
	grants := map[string]Grant{
		"demo.echo": {ToolID: "demo.echo", EffectClass: "read_only", Policy: "AUTO_COMMIT", GrantVersion: "g1", MaxCalls: 4},
		"demo.ref":  {ToolID: "demo.ref", EffectClass: "read_only", Policy: "AUTO_COMMIT", GrantVersion: "g1", MaxCalls: 4},
	}
	snapshot, err := BuildSnapshot(discovered, grants, BuildOptions{Revision: 9})
	if err != nil {
		t.Fatal(err)
	}
	tools := snapshot.Tools()
	if tools[0].Projection != ProjectionExact || tools[1].Projection != ProjectionUnsupported {
		t.Fatalf("projection classes = %q, %q", tools[0].Projection, tools[1].Projection)
	}
	runtimeSource, stub, err := snapshot.GeneratePython()
	if err != nil {
		t.Fatal(err)
	}
	for _, surface := range []string{runtimeSource, stub} {
		for _, expected := range []string{"def echo(", "text: str", `Literal["short", "long"]`, "tags: list[str]", "-> str", snapshot.Digest(), "v7"} {
			if !strings.Contains(surface, expected) {
				t.Fatalf("generated surface missing %q:\n%s", expected, surface)
			}
		}
		if strings.Contains(surface, "def ref_tool(") {
			t.Fatalf("unsupported tool was emitted:\n%s", surface)
		}
	}
	if !strings.Contains(runtimeSource, "return _call(") || strings.Contains(stub, "return _call(") {
		t.Fatalf("runtime/stub bodies are inconsistent:\nruntime=%s\nstub=%s", runtimeSource, stub)
	}
	directory := t.TempDir()
	runtimePath := filepath.Join(directory, "generated_tools.py")
	stubPath := filepath.Join(directory, "generated_tools.pyi")
	if err := os.WriteFile(runtimePath, []byte(runtimeSource), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stubPath, []byte(stub), 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("python3", "-m", "py_compile", runtimePath, stubPath).CombinedOutput(); err != nil {
		t.Fatalf("generated Python failed compilation: %v\n%s", err, output)
	}
	probe := `import importlib.util, json, sys
spec = importlib.util.spec_from_file_location("generated_tools", sys.argv[1])
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
seen = []
module._call = lambda *args: seen.append(args) or "ok"
assert module.echo(text="hello", mode="short") == "ok"
tool, digest, version, arguments = seen[0]
assert tool == "demo.echo" and digest == module.CATALOG_DIGEST and version == "v7"
assert arguments == {"text": "hello", "mode": "short"}
assert module.echo(text="hello", mode="short", tags=None) == "ok"
assert seen[1][3] == {"text": "hello", "mode": "short", "tags": None}
print(json.dumps(arguments, sort_keys=True))
`
	if output, err := exec.Command("python3", "-c", probe, runtimePath).CombinedOutput(); err != nil {
		t.Fatalf("generated wrapper execution failed: %v\n%s", err, output)
	}
}

func raw(value string) json.RawMessage { return json.RawMessage(value) }

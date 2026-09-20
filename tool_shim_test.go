package pysolate

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestDynamicToolShimInRealGuest(t *testing.T) {
	wasm, err := readGuestArtifact()
	if err != nil {
		t.Fatal(err)
	}
	name := "mcp.filesystem.read-file"
	manifest := Manifest{name: {
		Description: "Read one approved file",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
		Annotations: ToolAnnotations{ReadOnlyHint: true},
		Call: func(_ context.Context, args json.RawMessage) (any, error) {
			var input map[string]any
			if err := json.Unmarshal(args, &input); err != nil {
				return nil, err
			}
			return input, nil
		},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	runner, err := New(ctx, wasm, manifest)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())
	out, err := runner.Run(ctx, `from pysolate import tools
meta=tools.describe("mcp.filesystem.read-file")
result={
 "names": list(tools.names()),
 "description": meta["description"],
 "readonly": meta["annotations"]["read_only_hint"],
 "value": tools.call("mcp.filesystem.read-file", path="notes.txt"),
}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	var value struct {
		Names       []string       `json:"names"`
		Description string         `json:"description"`
		ReadOnly    bool           `json:"readonly"`
		Value       map[string]any `json:"value"`
	}
	if err := json.Unmarshal(out.Value, &value); err != nil {
		t.Fatal(err)
	}
	if len(value.Names) != 1 || value.Names[0] != name || value.Description != "Read one approved file" || !value.ReadOnly || value.Value["path"] != "notes.txt" {
		t.Fatalf("unexpected value: %#v", value)
	}
}

package mcpadapter

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	pysolate "github.com/bkmashiro/agent-python-runtime"
)

type fakeClient struct {
	pages map[string]ToolPage
	calls []string
}

func (c *fakeClient) ListTools(_ context.Context, cursor string) (ToolPage, error) {
	return c.pages[cursor], nil
}

func (c *fakeClient) CallTool(_ context.Context, name string, args json.RawMessage) (any, error) {
	c.calls = append(c.calls, name+":"+string(args))
	return map[string]any{"name": name}, nil
}

func TestProviderDiscoversPaginatedMCPToolsAndDelegatesCalls(t *testing.T) {
	client := &fakeClient{pages: map[string]ToolPage{
		"": {
			Tools: []Tool{{
				Name: "read_file", Description: "Read one file",
				InputSchema: json.RawMessage(`{"type":"object","required":["path"]}`),
				Annotations: Annotations{ReadOnlyHint: true},
			}},
			NextCursor: "next",
		},
		"next": {Tools: []Tool{{Name: "write_file", Annotations: Annotations{DestructiveHint: true}}}},
	}}
	provider := Provider{
		Client:    client,
		Namespace: "mcp.filesystem",
		AllowEarlyRead: func(tool Tool) bool {
			return tool.Annotations.ReadOnlyHint
		},
	}
	manifest, err := pysolate.ManifestFromProviders(context.Background(), provider)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest) != 2 || !manifest["mcp.filesystem.read_file"].AllowEarlyRead || manifest["mcp.filesystem.write_file"].AllowEarlyRead {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}
	got, err := manifest["mcp.filesystem.read_file"].Call(context.Background(), json.RawMessage(`{"path":"a.txt"}`))
	if err != nil || !reflect.DeepEqual(got, map[string]any{"name": "read_file"}) {
		t.Fatalf("got=%#v err=%v", got, err)
	}
	if !reflect.DeepEqual(client.calls, []string{`read_file:{"path":"a.txt"}`}) {
		t.Fatalf("calls=%#v", client.calls)
	}
}

func TestProviderDoesNotTrustMCPReadOnlyHintByDefault(t *testing.T) {
	client := &fakeClient{pages: map[string]ToolPage{"": {Tools: []Tool{{Name: "read", Annotations: Annotations{ReadOnlyHint: true}}}}}}
	manifest, err := pysolate.ManifestFromProviders(context.Background(), Provider{Client: client, Namespace: "mcp.server"})
	if err != nil {
		t.Fatal(err)
	}
	if manifest["mcp.server.read"].AllowEarlyRead {
		t.Fatal("MCP hints must not grant early execution authority")
	}
}

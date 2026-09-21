package gosdk

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"reflect"
	"testing"
	"time"

	pysolate "github.com/bkmashiro/agent-python-runtime"
	"github.com/bkmashiro/agent-python-runtime/mcpadapter"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type lookupInput struct {
	SKU string `json:"sku"`
}

type lookupOutput struct {
	SKU   string `json:"sku"`
	Price int    `json:"price"`
}

func TestClientAdaptsOfficialSDKSession(t *testing.T) {
	ctx := context.Background()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	server := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "v1"}, nil)
	openWorld := false
	destructive := false
	mcp.AddTool(server, &mcp.Tool{
		Name:        "lookup",
		Description: "Look up one local catalog item",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:    true,
			DestructiveHint: &destructive,
			OpenWorldHint:   &openWorld,
		},
	}, func(_ context.Context, _ *mcp.CallToolRequest, input lookupInput) (*mcp.CallToolResult, lookupOutput, error) {
		return nil, lookupOutput{SKU: input.SKU, Price: 123}, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "default_hints"}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		return nil, map[string]any{"ok": true}, nil
	})
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()

	sdkClient := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v1"}, nil)
	session, err := sdkClient.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	client, err := New(session)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	provider := mcpadapter.Provider{
		Client:             client,
		CanonicalNamespace: "mcp.catalog",
		PythonNamespace:    "catalog",
		AllowEarlyRead: func(tool mcpadapter.Tool) bool {
			return tool.Annotations.ReadOnlyHint && !tool.Annotations.OpenWorldHint
		},
	}
	manifest, err := pysolate.ManifestFromProviders(ctx, provider)
	if err != nil {
		t.Fatal(err)
	}
	spec, ok := manifest["mcp.catalog.lookup"]
	if !ok || spec.PythonPath != "catalog.lookup" || !spec.AllowEarlyRead {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}
	if !spec.Annotations.ReadOnlyHint || spec.Annotations.DestructiveHint || spec.Annotations.OpenWorldHint {
		t.Fatalf("unexpected annotations: %#v", spec.Annotations)
	}
	defaults := manifest["mcp.catalog.default_hints"].Annotations
	if defaults.ReadOnlyHint || !defaults.DestructiveHint || !defaults.OpenWorldHint {
		t.Fatalf("protocol annotation defaults were lost: %#v", defaults)
	}
	var schema map[string]any
	if err := json.Unmarshal(spec.InputSchema, &schema); err != nil || schema["type"] != "object" {
		t.Fatalf("schema=%s err=%v", spec.InputSchema, err)
	}

	result, err := spec.Call(ctx, json.RawMessage(`{"sku":"A-1"}`))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Structured lookupOutput `json:"structuredContent"`
		IsError    bool         `json:"isError"`
	}
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if got.IsError || !reflect.DeepEqual(got.Structured, lookupOutput{SKU: "A-1", Price: 123}) {
		t.Fatalf("result=%s", encoded)
	}
}

func TestClientRejectsNilSession(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("nil session was accepted")
	}
	if _, err := ConnectCommand(context.Background(), (*exec.Cmd)(nil), time.Second); err == nil {
		t.Fatal("nil command was accepted")
	}
}

func TestMCPToolErrorsRemainProtocolResults(t *testing.T) {
	ctx := context.Background()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	server := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "v1"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "fail"}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		return nil, nil, errors.New("fixture failure")
	})
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	clientSession, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v1"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	client, err := New(clientSession)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	result, err := client.CallTool(ctx, "fail", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("tool-level error escaped as protocol error: %v", err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(encoded, &got); err != nil || !got.IsError {
		t.Fatalf("result=%s err=%v", encoded, err)
	}
}

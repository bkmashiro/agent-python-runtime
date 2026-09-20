package pysolate

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type providerFunc func(context.Context) ([]ToolDefinition, error)

func (f providerFunc) Tools(ctx context.Context) ([]ToolDefinition, error) { return f(ctx) }

func TestManifestFromProvidersSeparatesCanonicalIdentityAndPythonPath(t *testing.T) {
	provider := providerFunc(func(context.Context) ([]ToolDefinition, error) {
		return []ToolDefinition{{
			Name: "mcp.market/get-price",
			Spec: ToolSpec{
				PythonPath:  "stock.getprice",
				Description: "Read one approved price",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"symbol":{"type":"string"}},"required":["symbol"]}`),
				Annotations: ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
				Call:        func(context.Context, json.RawMessage) (any, error) { return "ok", nil },
			},
		}}, nil
	})
	manifest, err := ManifestFromProviders(context.Background(), provider)
	if err != nil {
		t.Fatal(err)
	}
	spec, ok := manifest["mcp.market/get-price"]
	if !ok || spec.PythonPath != "stock.getprice" || spec.Description != "Read one approved price" || !spec.Annotations.ReadOnlyHint {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}
}

func TestManifestFromProvidersRejectsIdentityPathAndSchemaConflicts(t *testing.T) {
	call := func(context.Context, json.RawMessage) (any, error) { return nil, nil }
	for _, tc := range []struct {
		name      string
		providers []ToolProvider
	}{
		{
			name: "duplicate canonical identity",
			providers: []ToolProvider{
				providerFunc(func(context.Context) ([]ToolDefinition, error) {
					return []ToolDefinition{{Name: "same", Spec: ToolSpec{Call: call}}}, nil
				}),
				providerFunc(func(context.Context) ([]ToolDefinition, error) {
					return []ToolDefinition{{Name: "same", Spec: ToolSpec{Call: call}}}, nil
				}),
			},
		},
		{
			name: "duplicate Python path",
			providers: []ToolProvider{providerFunc(func(context.Context) ([]ToolDefinition, error) {
				return []ToolDefinition{
					{Name: "first", Spec: ToolSpec{PythonPath: "stock.price", Call: call}},
					{Name: "second", Spec: ToolSpec{PythonPath: "stock.price", Call: call}},
				}, nil
			})},
		},
		{
			name: "tool namespace collision",
			providers: []ToolProvider{providerFunc(func(context.Context) ([]ToolDefinition, error) {
				return []ToolDefinition{
					{Name: "root", Spec: ToolSpec{PythonPath: "stock", Call: call}},
					{Name: "child", Spec: ToolSpec{PythonPath: "stock.price", Call: call}},
				}, nil
			})},
		},
		{
			name: "canonical name needs explicit Python path",
			providers: []ToolProvider{providerFunc(func(context.Context) ([]ToolDefinition, error) {
				return []ToolDefinition{{Name: "mcp.market/get-price", Spec: ToolSpec{Call: call}}}, nil
			})},
		},
		{
			name: "invalid schema",
			providers: []ToolProvider{providerFunc(func(context.Context) ([]ToolDefinition, error) {
				return []ToolDefinition{{Name: "bad", Spec: ToolSpec{Call: call, InputSchema: json.RawMessage(`[]`)}}}, nil
			})},
		},
		{
			name: "provider failure",
			providers: []ToolProvider{providerFunc(func(context.Context) ([]ToolDefinition, error) {
				return nil, errors.New("discovery failed")
			})},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ManifestFromProviders(context.Background(), tc.providers...); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestGuestManifestContainsOnlyDispatchFields(t *testing.T) {
	call := func(context.Context, json.RawMessage) (any, error) { return nil, nil }
	manifest, guest, err := normalizeManifest(Manifest{
		"lookup": {Call: call, Description: "Host-only metadata"},
		"mcp.market/get-price": {
			PythonPath:     "stock.getprice",
			Call:           call,
			Description:    "Host-only MCP metadata",
			AllowEarlyRead: true,
		},
	})
	if err != nil || len(manifest) != 2 || len(guest) != 2 {
		t.Fatalf("manifest=%#v guest=%#v err=%v", manifest, guest, err)
	}
	paths := map[string]string{}
	for _, spec := range guest {
		paths[spec.Name] = spec.PythonPath
	}
	if paths["lookup"] != "lookup" || paths["mcp.market/get-price"] != "stock.getprice" {
		t.Fatalf("unexpected guest paths: %#v", paths)
	}
}

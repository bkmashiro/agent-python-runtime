package pysolate

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type providerFunc func(context.Context) ([]ToolDefinition, error)

func (f providerFunc) Tools(ctx context.Context) ([]ToolDefinition, error) { return f(ctx) }

func TestManifestFromProvidersPreservesCanonicalNamesAndMetadata(t *testing.T) {
	provider := providerFunc(func(context.Context) ([]ToolDefinition, error) {
		return []ToolDefinition{
			{
				Name: "mcp.filesystem.read-file",
				Spec: ToolSpec{
					Description: "Read one approved file",
					InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
					Annotations: ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
					Call:        func(context.Context, json.RawMessage) (any, error) { return "ok", nil },
				},
			},
		}, nil
	})
	manifest, err := ManifestFromProviders(context.Background(), provider)
	if err != nil {
		t.Fatal(err)
	}
	spec, ok := manifest["mcp.filesystem.read-file"]
	if !ok || spec.Description != "Read one approved file" || !spec.Annotations.ReadOnlyHint {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}
	spec.InputSchema[0] = '['
	if manifest["mcp.filesystem.read-file"].InputSchema[0] != '[' {
		t.Fatal("returned manifest should own provider metadata")
	}
}

func TestManifestFromProvidersRejectsDuplicateAndInvalidDefinitions(t *testing.T) {
	call := func(context.Context, json.RawMessage) (any, error) { return nil, nil }
	for _, tc := range []struct {
		name      string
		providers []ToolProvider
	}{
		{
			name: "duplicate",
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

func TestGuestManifestOnlyInjectsSafePythonIdentifiers(t *testing.T) {
	call := func(context.Context, json.RawMessage) (any, error) { return nil, nil }
	manifest, guest, err := normalizeManifest(Manifest{
		"lookup":                   {Call: call},
		"mcp.filesystem.read-file": {Call: call, Description: "MCP read"},
	})
	if err != nil || len(manifest) != 2 || len(guest) != 2 {
		t.Fatalf("manifest=%#v guest=%#v err=%v", manifest, guest, err)
	}
	injected := map[string]bool{}
	for _, spec := range guest {
		injected[spec.Name] = spec.InjectGlobal
	}
	if !injected["lookup"] || injected["mcp.filesystem.read-file"] {
		t.Fatalf("unexpected injection flags: %#v", injected)
	}
}

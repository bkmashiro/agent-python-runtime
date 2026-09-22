package pysolate

import (
	"context"
	"encoding/json"
	"testing"
)

func TestDiscoverCapabilitiesReturnsNormalizedStableCatalog(t *testing.T) {
	provider := providerFunc(func(context.Context) ([]ToolDefinition, error) {
		return []ToolDefinition{
			{Name: "zeta", Spec: ToolSpec{Call: func(context.Context, json.RawMessage) (any, error) { return nil, nil }}},
			{Name: "host/catalog", Spec: ToolSpec{
				PythonPath:  "catalog.lookup",
				Description: "Look up one catalog record",
				InputSchema: json.RawMessage(`{"type":"object","required":["key"]}`),
				Call:        func(context.Context, json.RawMessage) (any, error) { return nil, nil },
			}},
		}, nil
	})
	capabilities, err := DiscoverCapabilities(context.Background(), provider)
	if err != nil {
		t.Fatal(err)
	}
	if len(capabilities) != 2 || capabilities[0].Name != "host/catalog" || capabilities[0].Spec.PythonPath != "catalog.lookup" || capabilities[1].Name != "zeta" {
		t.Fatalf("capabilities=%#v", capabilities)
	}
	manifest, err := ManifestFromCapabilities(capabilities)
	if err != nil || manifest["host/catalog"].Description != "Look up one catalog record" {
		t.Fatalf("manifest=%#v err=%v", manifest, err)
	}
}

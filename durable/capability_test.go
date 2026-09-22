package durable

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	pysolate "github.com/bkmashiro/agent-python-runtime"
)

type capabilityProvider func(context.Context) ([]pysolate.ToolDefinition, error)

func (provider capabilityProvider) Tools(ctx context.Context) ([]pysolate.ToolDefinition, error) {
	return provider(ctx)
}

func TestToolsFromProvidersRequiresHostPolicyAndPreservesCapability(t *testing.T) {
	provider := capabilityProvider(func(context.Context) ([]pysolate.ToolDefinition, error) {
		return []pysolate.ToolDefinition{{
			Name: "mcp.catalog/lookup",
			Spec: pysolate.ToolSpec{
				PythonPath:  "catalog.lookup",
				Description: "Read one record",
				InputSchema: json.RawMessage(`{"type":"object","required":["key"]}`),
				Annotations: pysolate.ToolAnnotations{ReadOnlyHint: true},
				Call:        func(context.Context, json.RawMessage) (any, error) { return map[string]string{"value": "ok"}, nil },
			},
		}}, nil
	})
	tools, err := ToolsFromProviders(context.Background(), func(_ context.Context, capability pysolate.Capability) (CapabilityPolicy, error) {
		if capability.Name != "mcp.catalog/lookup" || !capability.Spec.Annotations.ReadOnlyHint {
			t.Fatalf("capability=%#v", capability)
		}
		return CapabilityPolicy{Version: "catalog-v1", Recovery: RetrySafe, Scheduling: ExternalIO}, nil
	}, provider)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "mcp.catalog/lookup" || tools[0].Definition.Spec.PythonPath != "catalog.lookup" || tools[0].Version != "catalog-v1" || tools[0].Recovery != RetrySafe || tools[0].Scheduling != ExternalIO {
		t.Fatalf("tools=%#v", tools)
	}
}

func TestToolsFromProvidersRejectsMissingOrFailedPolicy(t *testing.T) {
	provider := capabilityProvider(func(context.Context) ([]pysolate.ToolDefinition, error) {
		return []pysolate.ToolDefinition{{Name: "catalog.lookup", Spec: pysolate.ToolSpec{Call: func(context.Context, json.RawMessage) (any, error) { return nil, nil }}}}, nil
	})
	if _, err := ToolsFromProviders(context.Background(), nil, provider); err == nil {
		t.Fatal("expected missing policy error")
	}
	want := errors.New("not approved")
	_, err := ToolsFromProviders(context.Background(), func(context.Context, pysolate.Capability) (CapabilityPolicy, error) {
		return CapabilityPolicy{}, want
	}, provider)
	if !errors.Is(err, want) {
		t.Fatalf("error=%v", err)
	}
	if _, err = ToolsFromProviders(context.Background(), func(context.Context, pysolate.Capability) (CapabilityPolicy, error) {
		return CapabilityPolicy{Recovery: RetrySafe}, nil
	}, provider); err == nil {
		t.Fatal("expected missing version error")
	}
	if _, err = ToolsFromProviders(context.Background(), func(context.Context, pysolate.Capability) (CapabilityPolicy, error) {
		return CapabilityPolicy{Version: "v1"}, nil
	}, provider); err == nil {
		t.Fatal("expected missing recovery error")
	}
}

func TestBoundCapabilityExecutesAtDeclaredPythonPath(t *testing.T) {
	store, _ := openTestStore(t)
	capability := pysolate.Capability{
		Name: "host/catalog-lookup",
		Spec: pysolate.ToolSpec{
			PythonPath: "catalog.lookup",
			Call: func(context.Context, json.RawMessage) (any, error) {
				return map[string]string{"value": "record-a"}, nil
			},
		},
	}
	runner := realRunner(t, store, []Tool{BindCapability(capability, CapabilityPolicy{Version: "v1", Recovery: RetrySafe})})
	run, err := runner.Create(context.Background(), Definition{
		ID: "bound-capability", Code: `result = catalog.lookup(key="a")`, Seed: "seed",
		ArtifactSHA256: runner.ArtifactID(), EnvironmentVersion: "env-1", Inputs: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Advance(context.Background(), run.Definition.ID)
	var value map[string]string
	decodeErr := json.Unmarshal(result.Output.Value, &value)
	if err != nil || decodeErr != nil || result.State != StateCompleted || value["value"] != "record-a" {
		t.Fatalf("result=%+v err=%v decode=%v", result, err, decodeErr)
	}
}

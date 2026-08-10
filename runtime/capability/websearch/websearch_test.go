package websearch_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability/websearch"
)

type providerFunc func(context.Context, websearch.ProviderRequest) (websearch.ProviderPage, error)

func (function providerFunc) Search(ctx context.Context, request websearch.ProviderRequest) (websearch.ProviderPage, error) {
	return function(ctx, request)
}

func TestAdapterRejectsProviderAuthorityAndProvenanceViolations(t *testing.T) {
	cases := map[string]websearch.ProviderPage{
		"plaintext URL": {ObservedAt: time.Unix(1, 0), Results: []websearch.ProviderResult{{Title: "x", URL: "http://example.invalid", Snippet: "x", Source: "docs"}}},
		"userinfo URL":  {ObservedAt: time.Unix(1, 0), Results: []websearch.ProviderResult{{Title: "x", URL: "https://u:p@example.invalid", Snippet: "x", Source: "docs"}}},
		"wrong source":  {ObservedAt: time.Unix(1, 0), Results: []websearch.ProviderResult{{Title: "x", URL: "https://example.invalid", Snippet: "x", Source: "other"}}},
		"zero observed": {Results: []websearch.ProviderResult{{Title: "x", URL: "https://example.invalid", Snippet: "x", Source: "docs"}}},
	}
	for name, page := range cases {
		t.Run(name, func(t *testing.T) {
			adapter, err := websearch.NewAdapter(websearch.Config{Provider: providerFunc(func(context.Context, websearch.ProviderRequest) (websearch.ProviderPage, error) { return page, nil }), ProviderID: "provider", AllowedSources: []string{"docs"}, MaxQueryBytes: 64, MaxResults: 2})
			if err != nil {
				t.Fatal(err)
			}
			_, err = adapter.Handle(context.Background(), capability.HostCall{ToolID: websearch.SearchToolID, Arguments: json.RawMessage(`{"query":"x","max_results":1}`)})
			if err == nil || !errors.Is(err, websearch.ErrSearchResultInvalid) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestAdapterPassesOnlyFrozenSourceScopeToProvider(t *testing.T) {
	allowed := []string{"docs"}
	var received websearch.ProviderRequest
	adapter, err := websearch.NewAdapter(websearch.Config{Provider: providerFunc(func(_ context.Context, request websearch.ProviderRequest) (websearch.ProviderPage, error) {
		received = request
		return websearch.ProviderPage{ObservedAt: time.Unix(1, 0)}, nil
	}), ProviderID: "provider", AllowedSources: allowed, MaxQueryBytes: 64, MaxResults: 2})
	if err != nil {
		t.Fatal(err)
	}
	allowed[0] = "mutated"
	result, err := adapter.Handle(context.Background(), capability.HostCall{ToolID: websearch.SearchToolID, Arguments: json.RawMessage(`{"query":" x ","max_results":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	var page websearch.SearchPage
	if json.Unmarshal(result, &page) != nil || received.Query != "x" || len(received.AllowedSources) != 1 || received.AllowedSources[0] != "docs" || page.Provider != "provider" {
		t.Fatalf("request=%+v page=%+v", received, page)
	}
	received.AllowedSources[0] = "provider-mutated"
	_, err = adapter.Handle(context.Background(), capability.HostCall{ToolID: websearch.SearchToolID, Arguments: json.RawMessage(`{"query":"y","max_results":1}`)})
	if err != nil || received.AllowedSources[0] != "docs" {
		t.Fatalf("provider mutated adapter authority: request=%+v err=%v", received, err)
	}
}

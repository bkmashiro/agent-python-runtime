package fakeweb_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability/fakeweb"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability/websearch"
	"github.com/bkmashiro/agent-python-runtime/runtime/transaction"
)

const catalogDigest = "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"

type ids struct{ next int }

func (value *ids) New(prefix string) (string, error) {
	value.next++
	return fmt.Sprintf("%s_%d", prefix, value.next), nil
}

type response struct {
	Status string          `json:"status"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code string `json:"code"`
	} `json:"error"`
}

func newBroker(t testing.TB) (*capability.Broker, *fakeweb.Provider) {
	t.Helper()
	provider, err := fakeweb.NewProvider([]fakeweb.Document{
		{Title: "Pysolate lifecycle", URL: "https://docs.example.invalid/pysolate", Snippet: "Prepared Python with one untrusted Run", Source: "docs", PublishedAt: "2026-08-10T00:00:00Z"},
		{Title: "Unrelated result", URL: "https://blog.example.invalid/other", Snippet: "No matching term", Source: "blog", PublishedAt: "2026-08-09T00:00:00Z"},
	}, time.Unix(1234, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := websearch.NewAdapter(websearch.Config{Provider: provider, ProviderID: "fixture-search", AllowedSources: []string{"docs"}, MaxQueryBytes: 128, MaxResults: 5})
	if err != nil {
		t.Fatal(err)
	}
	registry := capability.NewRegistry()
	specs, err := websearch.HandlerSpecs(adapter)
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range specs {
		if err := registry.Register(spec); err != nil {
			t.Fatal(err)
		}
	}
	grants, err := websearch.ToolGrants("web:v1", 3)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1234, 0).UTC()
	coordinator := transaction.NewCoordinator(transaction.NewMemoryLedger(), &ids{}, func() time.Time { return now }, nil)
	tx, err := coordinator.Begin(transaction.BeginRequest{RunID: "run:web", CatalogDigest: catalogDigest, Mode: transaction.TransactionModeWorkflow})
	if err != nil {
		t.Fatal(err)
	}
	binder, err := capability.NewCoordinatorBinder(coordinator, tx.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "run:web", CatalogDigest: catalogDigest, Registry: registry, Binder: binder, ToolGrants: grants, MaxTransactionCalls: 8}, capability.FetcherFunc(func(context.Context, capability.ResolvedRequest, uint32) (capability.FetchOutput, error) {
		return capability.FetchOutput{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	return broker, provider
}

func call(t testing.TB, broker *capability.Broker, callID string, arguments any) response {
	t.Helper()
	args, _ := json.Marshal(arguments)
	payload, _ := json.Marshal(map[string]any{"call_id": callID, "capability": websearch.SearchToolID, "catalog_digest": catalogDigest, "handler_version": websearch.HandlerVersion, "arguments": json.RawMessage(args)})
	raw, err := broker.Call(context.Background(), payload)
	if err != nil {
		t.Fatal(err)
	}
	var value response
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestWebSearchIsTypedBoundedAndProvenanceBearing(t *testing.T) {
	broker, provider := newBroker(t)
	value := call(t, broker, "call:1", map[string]any{"query": "prepared python", "max_results": 5})
	var page websearch.SearchPage
	if value.Status != "ok" || json.Unmarshal(value.Result, &page) != nil {
		t.Fatalf("response=%+v result=%s", value, value.Result)
	}
	if page.Provider != "fixture-search" || page.Query != "prepared python" || len(page.Results) != 1 {
		t.Fatalf("page=%+v", page)
	}
	result := page.Results[0]
	if result.Rank != 1 || result.Source != "docs" || result.URL != "https://docs.example.invalid/pysolate" || result.ObservedAt != "1970-01-01T00:20:34Z" {
		t.Fatalf("result=%+v", result)
	}
	if provider.SearchCount() != 1 || len(broker.Receipts()) != 1 {
		t.Fatalf("searches=%d receipts=%d", provider.SearchCount(), len(broker.Receipts()))
	}
}

func TestWebSearchExactReplayDoesNotRedispatchProvider(t *testing.T) {
	broker, provider := newBroker(t)
	first := call(t, broker, "call:replay", map[string]any{"query": "prepared", "max_results": 1})
	second := call(t, broker, "call:replay", map[string]any{"query": "prepared", "max_results": 1})
	if first.Status != "ok" || second.Status != "ok" || string(first.Result) != string(second.Result) || provider.SearchCount() != 1 || len(broker.Receipts()) != 1 {
		t.Fatalf("first=%+v second=%+v searches=%d receipts=%d", first, second, provider.SearchCount(), len(broker.Receipts()))
	}
}

func TestWebSearchProviderFailureIsBoundedAndDoesNotLeakCause(t *testing.T) {
	broker, provider := newBroker(t)
	provider.FailNext()
	value := call(t, broker, "call:failure", map[string]any{"query": "prepared", "max_results": 1})
	if value.Status != "error" || value.Error == nil || value.Error.Code != "search_provider_failed" || strings.Contains(string(value.Result), "fake") || provider.SearchCount() != 1 {
		t.Fatalf("response=%+v result=%s searches=%d", value, value.Result, provider.SearchCount())
	}
}

func TestWebSearchRejectsAuthorityAndBudgetConfusion(t *testing.T) {
	broker, provider := newBroker(t)
	for index, arguments := range []map[string]any{
		{"query": "prepared", "max_results": 1, "url": "https://evil.invalid"},
		{"query": "prepared", "max_results": 6},
		{"query": strings.Repeat("x", 129), "max_results": 1},
		{"query": "", "max_results": 1},
	} {
		value := call(t, broker, fmt.Sprintf("call:%d", index+1), arguments)
		if value.Status == "ok" || value.Error == nil {
			t.Fatalf("arguments=%v response=%+v", arguments, value)
		}
	}
	if provider.SearchCount() != 0 {
		t.Fatalf("provider dispatched %d searches", provider.SearchCount())
	}
}

func TestFakeWebProviderRejectsRealDestinationsAndUnboundedConfig(t *testing.T) {
	if _, err := fakeweb.NewProvider([]fakeweb.Document{{Title: "real", URL: "https://example.com", Snippet: "x", Source: "docs"}}, time.Unix(1, 0)); err == nil {
		t.Fatal("real destination accepted by fake provider")
	}
	provider, err := fakeweb.NewProvider([]fakeweb.Document{{Title: "fixture", URL: "https://example.invalid", Snippet: "x", Source: "docs"}}, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	for _, config := range []websearch.Config{
		{Provider: provider, ProviderID: "fixture", AllowedSources: []string{"docs"}, MaxQueryBytes: 0, MaxResults: 1},
		{Provider: provider, ProviderID: "fixture", AllowedSources: nil, MaxQueryBytes: 10, MaxResults: 1},
		{Provider: provider, ProviderID: "bad provider", AllowedSources: []string{"docs"}, MaxQueryBytes: 10, MaxResults: 1},
	} {
		if _, err := websearch.NewAdapter(config); err == nil {
			t.Fatalf("accepted config=%+v", config)
		}
	}
}

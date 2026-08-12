package capability_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

func TestDemoCatalogIsTypedExactEndpointSource(t *testing.T) {
	var calls atomic.Uint32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.Method != http.MethodGet || request.URL.Path != "/catalog" || request.URL.RawQuery != "fixed=1" || len(request.Header.Values("Authorization")) != 0 {
			t.Errorf("unexpected request method=%s url=%s headers=%v", request.Method, request.URL.String(), request.Header)
		}
		writer.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = writer.Write([]byte(`{"items":[{"id":"alpha","title":"Alpha","score":7}]}`))
	}))
	defer server.Close()

	policy := capability.DemoCatalogPolicy{Endpoint: server.URL + "/catalog?fixed=1", Timeout: time.Second, MaxResponseBytes: 4096}
	registry := capability.NewRegistry()
	if err := capability.RegisterDemoCatalog(registry, policy); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 1})
	if err != nil {
		t.Fatal(err)
	}
	if specs := plan.Specs(); len(specs) != 1 || specs[0].Name != "sources.demo_catalog" || specs[0].EffectClass != capability.EffectExternalRead || specs[0].Playback != capability.PlaybackCaptured || specs[0].Python.Module != "sources" || specs[0].Python.Method != "demo_catalog" || specs[0].Python.GlobalAlias != "" {
		t.Fatalf("specs=%#v", specs)
	}
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "source-run", Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	response, err := broker.Call(context.Background(), []byte(`{"call_id":"one","capability":"sources.demo_catalog","arguments":{}}`))
	if err != nil || !strings.Contains(string(response), `"status":"ok"`) || !strings.Contains(string(response), `"title":"Alpha"`) {
		t.Fatalf("response=%s err=%v", response, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("source calls=%d", calls.Load())
	}
}

func TestDemoCatalogGrantBindsExactPolicy(t *testing.T) {
	base := capability.DemoCatalogPolicy{Endpoint: "https://source.test/catalog", Timeout: time.Second, MaxResponseBytes: 4096}
	firstSpec, firstGrant, err := capability.DemoCatalogDefinition(base)
	if err != nil {
		t.Fatal(err)
	}
	changed := base
	changed.Endpoint = "https://other.test/catalog"
	_, changedGrant, err := capability.DemoCatalogDefinition(changed)
	if err != nil {
		t.Fatal(err)
	}
	if firstGrant.Identity() == changedGrant.Identity() {
		t.Fatal("endpoint change did not change grant")
	}
	if firstSpec.HandlerIdentity == "" || strings.Contains(string(mustJSON(t, firstSpec)), base.Endpoint) {
		t.Fatalf("invalid or endpoint-bearing spec=%#v", firstSpec)
	}
	for name, mutate := range map[string]func(*capability.DemoCatalogPolicy){
		"timeout": func(policy *capability.DemoCatalogPolicy) { policy.Timeout = 2 * time.Second },
		"bytes":   func(policy *capability.DemoCatalogPolicy) { policy.MaxResponseBytes++ },
	} {
		t.Run(name, func(t *testing.T) {
			policy := base
			mutate(&policy)
			_, grant, err := capability.DemoCatalogDefinition(policy)
			if err != nil || grant.Identity() == firstGrant.Identity() {
				t.Fatalf("grant=%q err=%v", grant.Identity(), err)
			}
		})
	}
}

func TestDemoCatalogFailsClosedOnTransportAndPayload(t *testing.T) {
	tests := map[string]http.HandlerFunc{
		"redirect": func(writer http.ResponseWriter, request *http.Request) {
			http.Redirect(writer, request, "/other", http.StatusFound)
		},
		"status": func(writer http.ResponseWriter, request *http.Request) { writer.WriteHeader(http.StatusNoContent) },
		"content type": func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "text/plain")
			_, _ = writer.Write([]byte(`{"items":[]}`))
		},
		"non UTF-8 charset": func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "application/json; charset=iso-8859-1")
			_, _ = writer.Write([]byte(`{"items":[]}`))
		},
		"oversize": func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"items":[{"id":"` + strings.Repeat("x", 512) + `","title":"x","score":1}]}`))
		},
		"schema": func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"items":[{"id":"alpha","title":"Alpha","score":"bad"}]}`))
		},
	}
	for name, handler := range tests {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(handler)
			defer server.Close()
			registry := capability.NewRegistry()
			if err := capability.RegisterDemoCatalog(registry, capability.DemoCatalogPolicy{Endpoint: server.URL, Timeout: time.Second, MaxResponseBytes: 128}); err != nil {
				t.Fatal(err)
			}
			plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 1})
			if err != nil {
				t.Fatal(err)
			}
			broker, err := capability.NewBroker(capability.Config{RunIdentity: "source-run", Plan: plan})
			if err != nil {
				t.Fatal(err)
			}
			response, err := broker.Call(context.Background(), []byte(`{"call_id":"one","capability":"sources.demo_catalog","arguments":{}}`))
			if err != nil || strings.Contains(string(response), `"status":"ok"`) {
				t.Fatalf("response=%s err=%v", response, err)
			}
		})
	}
}

func TestDemoCatalogRejectsInvalidPolicy(t *testing.T) {
	for name, policy := range map[string]capability.DemoCatalogPolicy{
		"relative":    {Endpoint: "/catalog", Timeout: time.Second, MaxResponseBytes: 100},
		"credentials": {Endpoint: "https://user:pass@source.test/catalog", Timeout: time.Second, MaxResponseBytes: 100},
		"fragment":    {Endpoint: "https://source.test/catalog#secret", Timeout: time.Second, MaxResponseBytes: 100},
		"scheme":      {Endpoint: "file:///tmp/catalog", Timeout: time.Second, MaxResponseBytes: 100},
		"timeout":     {Endpoint: "https://source.test/catalog", Timeout: 0, MaxResponseBytes: 100},
		"bytes":       {Endpoint: "https://source.test/catalog", Timeout: time.Second, MaxResponseBytes: 0},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := capability.DemoCatalogDefinition(policy); err == nil {
				t.Fatal("invalid policy accepted")
			}
		})
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

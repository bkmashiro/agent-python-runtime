package capability_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

type fakeFetcher struct {
	calls []capability.ResolvedRequest
}

func (fetcher *fakeFetcher) Fetch(_ context.Context, request capability.ResolvedRequest, _ uint32) (capability.FetchOutput, error) {
	fetcher.calls = append(fetcher.calls, request)
	if request.URL == "https://api.example.test/fail" {
		return capability.FetchOutput{}, errors.New("fixture failure")
	}
	if request.URL == "https://api.example.test/large" {
		return capability.FetchOutput{StatusCode: 200, Body: []byte("0123456789abcdef")}, nil
	}
	return capability.FetchOutput{
		StatusCode:  200,
		Body:        []byte(`{"value":42}`),
		ContentType: "application/json",
	}, nil
}

func testGrant() capability.Grant {
	return capability.Grant{
		Name:               capability.FetchManyCapability,
		MaxCalls:           2,
		MaxRequestsPerCall: 3,
		MaxTotalRequests:   4,
		MaxResponseBytes:   4096,
		PerRequestTimeout:  time.Second,
		Targets: map[string]capability.TargetGrant{
			"fixture": {
				BaseURL: "https://api.example.test",
				Headers: map[string]string{"Authorization": "Host secret"},
			},
		},
	}
}

func newBroker(t *testing.T, grants map[string]capability.Grant, fetcher capability.Fetcher) *capability.Broker {
	t.Helper()
	broker, err := capability.NewBroker(capability.Config{
		RunIdentity: "host-run-001",
		Grants:      grants,
	}, fetcher)
	if err != nil {
		t.Fatal(err)
	}
	return broker
}

func call(t *testing.T, broker *capability.Broker, callID, name string, requests []map[string]string) capability.ToolResponse {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"call_id":    callID,
		"capability": name,
		"arguments":  map[string]any{"requests": requests},
	})
	if err != nil {
		t.Fatal(err)
	}
	responseBytes, err := broker.Call(context.Background(), payload)
	if err != nil {
		t.Fatal(err)
	}
	var response capability.ToolResponse
	if err := json.Unmarshal(responseBytes, &response); err != nil {
		t.Fatalf("decode response: %v: %s", err, responseBytes)
	}
	return response
}

func TestNoGrantAndWrongCapabilityAreDenied(t *testing.T) {
	fetcher := &fakeFetcher{}
	broker := newBroker(t, nil, fetcher)
	requests := []map[string]string{{"request_id": "r1", "target": "fixture", "path": "/ok"}}
	for _, name := range []string{capability.FetchManyCapability, "arbitrary_tool"} {
		response := call(t, broker, "call-denied", name, requests)
		if response.Status != capability.StatusDenied || response.Error == nil {
			t.Fatalf("expected denial for %q: %#v", name, response)
		}
	}
	if len(fetcher.calls) != 0 {
		t.Fatalf("denied calls reached fetcher: %d", len(fetcher.calls))
	}
}

func TestMatchingGrantResolvesHostOwnedTargetAndHeaders(t *testing.T) {
	fetcher := &fakeFetcher{}
	grant := testGrant()
	broker := newBroker(t, map[string]capability.Grant{grant.Name: grant}, fetcher)
	response := call(t, broker, "call-ok", capability.FetchManyCapability, []map[string]string{
		{"request_id": "r1", "target": "fixture", "path": "/one?x=1"},
		{"request_id": "r2", "target": "fixture", "path": "/two"},
	})
	if response.Status != capability.StatusOK || response.Error != nil {
		t.Fatalf("unexpected response: %#v", response)
	}
	if len(fetcher.calls) != 2 {
		t.Fatalf("fetch calls=%d", len(fetcher.calls))
	}
	if fetcher.calls[0].URL != "https://api.example.test/one?x=1" {
		t.Fatalf("unexpected resolved URL %q", fetcher.calls[0].URL)
	}
	if fetcher.calls[0].Headers["Authorization"] != "Host secret" {
		t.Fatalf("Host header missing: %#v", fetcher.calls[0].Headers)
	}
	if len(broker.Receipts()) != 2 || broker.Receipts()[0].ReceiptID == "" {
		t.Fatalf("missing receipts: %#v", broker.Receipts())
	}
}

func TestArbitraryDestinationAndAuthorityBearingPathsAreDenied(t *testing.T) {
	fetcher := &fakeFetcher{}
	grant := testGrant()
	broker := newBroker(t, map[string]capability.Grant{grant.Name: grant}, fetcher)
	requests := []map[string]string{
		{"request_id": "unknown", "target": "not-granted", "path": "/ok"},
		{"request_id": "absolute", "target": "fixture", "path": "https://evil.example/x"},
		{"request_id": "network-path", "target": "fixture", "path": "//evil.example/x"},
	}
	response := call(t, broker, "call-targets", capability.FetchManyCapability, requests)
	if response.Status != capability.StatusOK {
		t.Fatalf("batch-level call should return structured partial results: %#v", response)
	}
	items := response.Result.Items
	if len(items) != len(requests) {
		t.Fatalf("items=%d", len(items))
	}
	for _, item := range items {
		if item.Status != capability.StatusDenied {
			t.Fatalf("unexpected item: %#v", item)
		}
	}
	if len(fetcher.calls) != 0 {
		t.Fatalf("denied destinations reached fetcher")
	}
}

func TestCallAndRequestBudgetsFailClosed(t *testing.T) {
	fetcher := &fakeFetcher{}
	grant := testGrant()
	grant.MaxCalls = 1
	grant.MaxRequestsPerCall = 1
	broker := newBroker(t, map[string]capability.Grant{grant.Name: grant}, fetcher)

	tooMany := call(t, broker, "call-many", capability.FetchManyCapability, []map[string]string{
		{"request_id": "r1", "target": "fixture", "path": "/one"},
		{"request_id": "r2", "target": "fixture", "path": "/two"},
	})
	if tooMany.Status != capability.StatusDenied {
		t.Fatalf("expected per-call budget denial: %#v", tooMany)
	}
	ok := call(t, broker, "call-one", capability.FetchManyCapability, []map[string]string{
		{"request_id": "r3", "target": "fixture", "path": "/one"},
	})
	if ok.Status != capability.StatusOK {
		t.Fatalf("valid call failed: %#v", ok)
	}
	exhausted := call(t, broker, "call-two", capability.FetchManyCapability, []map[string]string{
		{"request_id": "r4", "target": "fixture", "path": "/two"},
	})
	if exhausted.Status != capability.StatusDenied {
		t.Fatalf("expected call budget denial: %#v", exhausted)
	}
}

func TestResponseByteBudgetReturnsStablePartialError(t *testing.T) {
	fetcher := &fakeFetcher{}
	grant := testGrant()
	grant.MaxResponseBytes = 8
	broker := newBroker(t, map[string]capability.Grant{grant.Name: grant}, fetcher)
	response := call(t, broker, "call-large", capability.FetchManyCapability, []map[string]string{
		{"request_id": "large", "target": "fixture", "path": "/large"},
	})
	if response.Status != capability.StatusOK || len(response.Result.Items) != 1 {
		t.Fatalf("unexpected batch response: %#v", response)
	}
	item := response.Result.Items[0]
	if item.Status != capability.StatusError || item.Error == nil || item.Error.Code != "response_too_large" {
		t.Fatalf("unexpected oversized response: %#v", item)
	}
	if broker.Receipts()[0].ResponseSHA256 != "" {
		t.Fatalf("oversized bytes must not be admitted: %#v", broker.Receipts()[0])
	}
}

func TestPartialFailureAndReceiptIdentityAreStable(t *testing.T) {
	grant := testGrant()
	requests := []map[string]string{
		{"request_id": "ok", "target": "fixture", "path": "/ok"},
		{"request_id": "fail", "target": "fixture", "path": "/fail"},
	}
	var firstIDs []string
	for iteration := 0; iteration < 2; iteration++ {
		fetcher := &fakeFetcher{}
		broker := newBroker(t, map[string]capability.Grant{grant.Name: grant}, fetcher)
		response := call(t, broker, "stable-call", capability.FetchManyCapability, requests)
		if response.Status != capability.StatusOK || len(response.Result.Items) != 2 {
			t.Fatalf("bad partial response: %#v", response)
		}
		if response.Result.Items[0].Status != capability.StatusOK || response.Result.Items[1].Status != capability.StatusError {
			t.Fatalf("partial statuses not preserved: %#v", response.Result.Items)
		}
		receipts := broker.Receipts()
		ids := []string{receipts[0].ReceiptID, receipts[1].ReceiptID}
		if iteration == 0 {
			firstIDs = ids
		} else if ids[0] != firstIDs[0] || ids[1] != firstIDs[1] {
			t.Fatalf("receipt IDs are not deterministic: %v != %v", ids, firstIDs)
		}
	}
}

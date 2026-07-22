package capability_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

type concurrencyFetcher struct {
	mu               sync.Mutex
	active           int
	maxActive        int
	calls            int
	waitFor          int
	started          chan struct{}
	startedOnce      sync.Once
	delays           map[string]time.Duration
	bodies           map[string]string
	blockUntilCancel bool
}

func (fetcher *concurrencyFetcher) Fetch(ctx context.Context, request capability.ResolvedRequest, _ uint32) (capability.FetchOutput, error) {
	fetcher.mu.Lock()
	fetcher.calls++
	fetcher.active++
	if fetcher.active > fetcher.maxActive {
		fetcher.maxActive = fetcher.active
	}
	if fetcher.active >= fetcher.waitFor && fetcher.started != nil {
		fetcher.startedOnce.Do(func() { close(fetcher.started) })
	}
	fetcher.mu.Unlock()
	defer func() {
		fetcher.mu.Lock()
		fetcher.active--
		fetcher.mu.Unlock()
	}()

	delay := fetcher.delays[request.URL]
	if fetcher.started != nil && fetcher.waitFor > 1 {
		select {
		case <-ctx.Done():
			return capability.FetchOutput{}, ctx.Err()
		case <-fetcher.started:
		}
	}
	if fetcher.blockUntilCancel {
		<-ctx.Done()
		return capability.FetchOutput{}, ctx.Err()
	}
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return capability.FetchOutput{}, ctx.Err()
		case <-timer.C:
		}
	}
	body := fetcher.bodies[request.URL]
	if body == "" {
		body = request.URL
	}
	return capability.FetchOutput{StatusCode: 200, Body: []byte(body), ContentType: "text/plain"}, nil
}

func (fetcher *concurrencyFetcher) maximum() int {
	fetcher.mu.Lock()
	defer fetcher.mu.Unlock()
	return fetcher.maxActive
}

func (fetcher *concurrencyFetcher) count() int {
	fetcher.mu.Lock()
	defer fetcher.mu.Unlock()
	return fetcher.calls
}

func TestFetchManyExecutesBoundedConcurrentWaveInInputOrder(t *testing.T) {
	fetcher := &concurrencyFetcher{waitFor: 3, started: make(chan struct{})}
	grant := testGrant()
	grant.MaxRequestsPerCall = 3
	grant.MaxTotalRequests = 3
	grant.MaxConcurrency = 3
	broker := newBroker(t, map[string]capability.Grant{grant.Name: grant}, fetcher)
	response := call(t, broker, "parallel-order", capability.FetchManyCapability, []map[string]string{
		{"request_id": "zero", "target": "fixture", "path": "/zero"},
		{"request_id": "one", "target": "fixture", "path": "/one"},
		{"request_id": "two", "target": "fixture", "path": "/two"},
	})
	if fetcher.maximum() != 3 {
		t.Fatalf("maximum concurrency=%d, want 3", fetcher.maximum())
	}
	for index, item := range response.Result.Items {
		want := []string{"zero", "one", "two"}[index]
		if item.RequestID != want || item.Status != capability.StatusOK {
			t.Fatalf("result order drifted at %d: %#v", index, item)
		}
	}
	for index, receipt := range broker.Receipts() {
		if receipt.OperationIndex != uint32(index) {
			t.Fatalf("receipt order drifted: %#v", broker.Receipts())
		}
	}
}

func TestConcurrentByteAdmissionIsIndependentOfCompletionOrder(t *testing.T) {
	for name, delays := range map[string]map[string]time.Duration{
		"second finishes first": {
			"https://api.example.test/first":  20 * time.Millisecond,
			"https://api.example.test/second": time.Millisecond,
		},
		"first finishes first": {
			"https://api.example.test/first":  time.Millisecond,
			"https://api.example.test/second": 20 * time.Millisecond,
		},
	} {
		t.Run(name, func(t *testing.T) {
			fetcher := &concurrencyFetcher{
				waitFor: 2,
				started: make(chan struct{}),
				delays:  delays,
				bodies: map[string]string{
					"https://api.example.test/first":  "123456",
					"https://api.example.test/second": "abcdef",
				},
			}
			grant := testGrant()
			grant.MaxConcurrency = 2
			grant.MaxResponseBytes = 8
			broker := newBroker(t, map[string]capability.Grant{grant.Name: grant}, fetcher)
			response := call(t, broker, "byte-order", capability.FetchManyCapability, []map[string]string{
				{"request_id": "first", "target": "fixture", "path": "/first"},
				{"request_id": "second", "target": "fixture", "path": "/second"},
			})
			items := response.Result.Items
			if items[0].Status != capability.StatusOK || items[0].Body != "123456" {
				t.Fatalf("first input was not admitted: %#v", items)
			}
			if items[1].Status != capability.StatusError || items[1].Error == nil || items[1].Error.Code != "response_too_large" {
				t.Fatalf("second input should deterministically exceed aggregate budget: %#v", items)
			}
			receipts := broker.Receipts()
			if receipts[0].ResponseSHA256 == "" || receipts[1].ResponseSHA256 != "" {
				t.Fatalf("receipt admission drifted: %#v", receipts)
			}
		})
	}
}

func TestConcurrentFetchCancellationJoinsWorkers(t *testing.T) {
	fetcher := &concurrencyFetcher{waitFor: 2, started: make(chan struct{}), blockUntilCancel: true}
	grant := testGrant()
	grant.MaxRequestsPerCall = 4
	grant.MaxTotalRequests = 4
	grant.MaxConcurrency = 2
	broker := newBroker(t, map[string]capability.Grant{grant.Name: grant}, fetcher)
	payload, err := json.Marshal(map[string]any{
		"call_id":    "cancel-call",
		"capability": capability.FetchManyCapability,
		"arguments": map[string]any{"requests": []map[string]string{
			{"request_id": "first", "target": "fixture", "path": "/first"},
			{"request_id": "second", "target": "fixture", "path": "/second"},
			{"request_id": "third", "target": "fixture", "path": "/third"},
			{"request_id": "fourth", "target": "fixture", "path": "/fourth"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, callErr := broker.Call(ctx, payload)
		done <- callErr
	}()
	select {
	case <-fetcher.started:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("workers did not start concurrently")
	}
	select {
	case callErr := <-done:
		if callErr != nil {
			t.Fatalf("structured cancellation call failed: %v", callErr)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled workers were not joined")
	}
	if fetcher.maximum() != 2 {
		t.Fatalf("maximum concurrency=%d, want 2", fetcher.maximum())
	}
	if fetcher.count() != 2 {
		t.Fatalf("provider calls after cancellation=%d, want first wave only", fetcher.count())
	}
}

func TestConcurrentFetchPreservesPartialTimeoutInInputOrder(t *testing.T) {
	fetcher := &concurrencyFetcher{
		waitFor: 2,
		started: make(chan struct{}),
		delays: map[string]time.Duration{
			"https://api.example.test/slow": 50 * time.Millisecond,
			"https://api.example.test/fast": time.Millisecond,
		},
	}
	grant := testGrant()
	grant.MaxConcurrency = 2
	grant.PerRequestTimeout = 10 * time.Millisecond
	broker := newBroker(t, map[string]capability.Grant{grant.Name: grant}, fetcher)
	response := call(t, broker, "partial-timeout", capability.FetchManyCapability, []map[string]string{
		{"request_id": "slow", "target": "fixture", "path": "/slow"},
		{"request_id": "fast", "target": "fixture", "path": "/fast"},
	})
	items := response.Result.Items
	if items[0].Status != capability.StatusTimeout || items[0].Error == nil || items[0].Error.Code != "fetch_timeout" {
		t.Fatalf("slow operation did not time out: %#v", items)
	}
	if items[1].Status != capability.StatusOK || items[1].RequestID != "fast" {
		t.Fatalf("fast operation was not preserved: %#v", items)
	}
	if broker.Receipts()[0].OperationIndex != 0 || broker.Receipts()[1].OperationIndex != 1 {
		t.Fatalf("receipt order drifted: %#v", broker.Receipts())
	}
}

func TestConcurrencyGrantIsHardBounded(t *testing.T) {
	for _, value := range []uint32{0, 17} {
		grant := testGrant()
		grant.MaxConcurrency = value
		if _, err := capability.NewBroker(capability.Config{
			RunIdentity: "host-run",
			Grants:      map[string]capability.Grant{grant.Name: grant},
		}, &fakeFetcher{}); err == nil {
			t.Fatalf("max concurrency %d should be rejected", value)
		}
	}
}

func TestFetchManyNeverExceedsHostConcurrencyGrant(t *testing.T) {
	fetcher := &concurrencyFetcher{
		waitFor: 1,
		delays: map[string]time.Duration{
			"https://api.example.test/0": 5 * time.Millisecond,
			"https://api.example.test/1": 5 * time.Millisecond,
			"https://api.example.test/2": 5 * time.Millisecond,
			"https://api.example.test/3": 5 * time.Millisecond,
			"https://api.example.test/4": 5 * time.Millisecond,
		},
	}
	grant := testGrant()
	grant.MaxRequestsPerCall = 5
	grant.MaxTotalRequests = 5
	grant.MaxConcurrency = 2
	broker := newBroker(t, map[string]capability.Grant{grant.Name: grant}, fetcher)
	response := call(t, broker, "bounded-wave", capability.FetchManyCapability, []map[string]string{
		{"request_id": "0", "target": "fixture", "path": "/0"},
		{"request_id": "1", "target": "fixture", "path": "/1"},
		{"request_id": "2", "target": "fixture", "path": "/2"},
		{"request_id": "3", "target": "fixture", "path": "/3"},
		{"request_id": "4", "target": "fixture", "path": "/4"},
	})
	if response.Status != capability.StatusOK || len(response.Result.Items) != 5 {
		t.Fatalf("unexpected response: %#v", response)
	}
	if fetcher.maximum() != 2 {
		t.Fatalf("maximum concurrency=%d, want exactly 2", fetcher.maximum())
	}
}

package capability_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

const canonicalProviderDelay = 2 * time.Millisecond

var canonicalOperationCounts = []int{1, 5, 20}

type delayedFetcher struct {
	delay time.Duration
}

func (fetcher delayedFetcher) Fetch(ctx context.Context, _ capability.ResolvedRequest, _ uint32) (capability.FetchOutput, error) {
	timer := time.NewTimer(fetcher.delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return capability.FetchOutput{}, ctx.Err()
	case <-timer.C:
		return capability.FetchOutput{StatusCode: 200, Body: []byte(`{"value":42}`), ContentType: "application/json"}, nil
	}
}

func TestCanonicalSequentialBenchmarkFixtures(t *testing.T) {
	if fmt.Sprint(canonicalOperationCounts) != "[1 5 20]" {
		t.Fatalf("canonical operation counts drifted: %v", canonicalOperationCounts)
	}
	if canonicalProviderDelay != 2*time.Millisecond {
		t.Fatalf("canonical provider delay drifted: %s", canonicalProviderDelay)
	}
	for _, operations := range canonicalOperationCounts {
		payload := canonicalFetchPayload(t, operations)
		broker := canonicalBroker(t, operations)
		responseBytes, err := broker.Call(context.Background(), payload)
		if err != nil {
			t.Fatal(err)
		}
		var response capability.ToolResponse
		if err := json.Unmarshal(responseBytes, &response); err != nil {
			t.Fatal(err)
		}
		if response.Status != capability.StatusOK || len(response.Result.Items) != operations || len(broker.Receipts()) != operations {
			t.Fatalf("operations=%d response=%#v receipts=%d", operations, response, len(broker.Receipts()))
		}
	}
}

func BenchmarkFetchManySequential(b *testing.B) {
	for _, operations := range canonicalOperationCounts {
		b.Run(fmt.Sprintf("operations=%d", operations), func(b *testing.B) {
			payload := canonicalFetchPayload(b, operations)
			b.ResetTimer()
			for iteration := 0; iteration < b.N; iteration++ {
				broker := canonicalBroker(b, operations)
				if _, err := broker.Call(context.Background(), payload); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(operations), "operations/batch")
			b.ReportMetric(float64(canonicalProviderDelay.Nanoseconds()), "provider-delay-ns/operation")
		})
	}
}

func canonicalFetchPayload(tb testing.TB, operations int) []byte {
	tb.Helper()
	requests := make([]map[string]string, operations)
	for index := range requests {
		requests[index] = map[string]string{
			"request_id": fmt.Sprintf("request-%02d", index),
			"target":     "fixture",
			"path":       fmt.Sprintf("/item/%02d", index),
		}
	}
	payload, err := json.Marshal(map[string]any{
		"call_id":    "benchmark-call",
		"capability": capability.FetchManyCapability,
		"arguments":  map[string]any{"requests": requests},
	})
	if err != nil {
		tb.Fatal(err)
	}
	return payload
}

func canonicalBroker(tb testing.TB, operations int) *capability.Broker {
	tb.Helper()
	grant := capability.Grant{
		Name:               capability.FetchManyCapability,
		MaxCalls:           1,
		MaxRequestsPerCall: uint32(operations),
		MaxTotalRequests:   uint32(operations),
		MaxResponseBytes:   1024 * 1024,
		PerRequestTimeout:  time.Second,
		Targets: map[string]capability.TargetGrant{
			"fixture": {BaseURL: "https://benchmark.invalid"},
		},
	}
	broker, err := capability.NewBroker(capability.Config{
		RunIdentity: "benchmark-host-run",
		Grants:      map[string]capability.Grant{capability.FetchManyCapability: grant},
	}, delayedFetcher{delay: canonicalProviderDelay})
	if err != nil {
		tb.Fatal(err)
	}
	return broker
}

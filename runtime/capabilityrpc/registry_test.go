package capabilityrpc

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

func testBroker(t *testing.T, handler capability.Handler) (*capability.Broker, *capability.Plan) {
	t.Helper()
	registry := capability.NewRegistry()
	grant, err := capability.NewGrant(json.RawMessage(`{"scope":"test"}`))
	if err != nil {
		t.Fatal(err)
	}
	err = registry.Register(capability.Spec{
		Name: "math.double", Version: "v1", Description: "double a value", EffectClass: capability.EffectPure,
		Playback: capability.PlaybackLiveOnly, HandlerIdentity: "test.double.v1",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"value":{"type":"integer"}},"required":["value"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"integer"}},"required":["value"],"additionalProperties":false}`),
		Python:       &capability.PythonProjection{Module: "math_tools", Method: "double", Arguments: []string{"value"}, ResultField: "value"},
	}, grant, handler)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 4})
	if err != nil {
		t.Fatal(err)
	}
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "execution-1", Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	return broker, plan
}

func request(plan *capability.Plan, callID string, value int) []byte {
	encoded, _ := json.Marshal(Request{
		SchemaVersion: SchemaVersion, ChannelID: "channel-1", InvocationID: "invocation-1", ExecutionID: "execution-1",
		PlanSHA256: plan.Identity(), Call: json.RawMessage(mustJSON(map[string]any{"call_id": callID, "capability": "math.double", "arguments": map[string]any{"value": value}})),
	})
	return encoded
}

func mustJSON(value any) []byte { encoded, _ := json.Marshal(value); return encoded }

func TestRegistryDispatchesAndReplaysCompletedExactCall(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	broker, plan := testBroker(t, capability.HandlerFunc(func(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		var input struct {
			Value int `json:"value"`
		}
		_ = json.Unmarshal(raw, &input)
		return json.RawMessage(mustJSON(map[string]int{"value": input.Value * 2})), nil
	}))
	registry := NewRegistry()
	if err := registry.Open(ChannelConfig{ID: "channel-1", Credential: "secret-credential", InvocationID: "invocation-1", ExecutionID: "execution-1", Transport: TransportUnixHTTP, ExpiresAt: time.Now().Add(time.Minute), MaxRequestBytes: 4096, Broker: broker}); err != nil {
		t.Fatal(err)
	}

	first, err := registry.Dispatch(context.Background(), "secret-credential", request(plan, "call-1", 3))
	if err != nil {
		t.Fatal(err)
	}
	second, err := registry.Dispatch(context.Background(), "secret-credential", request(plan, "call-1", 3))
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != StatusCompleted || first.Replayed || second.Status != StatusCompleted || !second.Replayed {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	if string(first.BrokerResponse) != string(second.BrokerResponse) {
		t.Fatalf("responses differ: %s / %s", first.BrokerResponse, second.BrokerResponse)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 || broker.CallCount() != 1 {
		t.Fatalf("handler=%d broker=%d", calls, broker.CallCount())
	}
}

func TestRegistryRejectsIdentityDriftExpiryAndRevocation(t *testing.T) {
	broker, plan := testBroker(t, capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"value":2}`), nil
	}))
	registry := NewRegistry()
	config := ChannelConfig{ID: "channel-1", Credential: "secret-credential", InvocationID: "invocation-1", ExecutionID: "execution-1", Transport: TransportUnixHTTP, ExpiresAt: time.Now().Add(time.Minute), MaxRequestBytes: 4096, Broker: broker}
	if err := registry.Open(config); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Dispatch(context.Background(), "wrong", request(plan, "call-1", 1)); err != ErrChannelDenied {
		t.Fatalf("credential err=%v", err)
	}
	if _, err := registry.Dispatch(context.Background(), "secret-credential", request(plan, "call-1", 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Dispatch(context.Background(), "secret-credential", request(plan, "call-1", 2)); err != ErrCallIdentityMismatch {
		t.Fatalf("drift err=%v", err)
	}
	registry.Revoke("channel-1")
	if _, err := registry.Dispatch(context.Background(), "secret-credential", request(plan, "call-2", 1)); err != ErrChannelDenied {
		t.Fatalf("revoked err=%v", err)
	}

	expired := NewRegistry()
	config.ID = "channel-2"
	config.ExpiresAt = time.Now().Add(-time.Second)
	if err := expired.Open(config); err != ErrInvalidChannel {
		t.Fatalf("expired open err=%v", err)
	}
}

func TestRegistryReturnsAmbiguousForConcurrentDuplicate(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	broker, plan := testBroker(t, capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		close(started)
		<-release
		return json.RawMessage(`{"value":2}`), nil
	}))
	registry := NewRegistry()
	if err := registry.Open(ChannelConfig{ID: "channel-1", Credential: "secret-credential", InvocationID: "invocation-1", ExecutionID: "execution-1", Transport: TransportUnixHTTP, ExpiresAt: time.Now().Add(time.Minute), MaxRequestBytes: 4096, Broker: broker}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := registry.Dispatch(context.Background(), "secret-credential", request(plan, "call-1", 1))
		done <- err
	}()
	<-started
	response, err := registry.Dispatch(context.Background(), "secret-credential", request(plan, "call-1", 1))
	if err != nil || response.Status != StatusAmbiguous || response.ErrorCode != "call_in_flight" {
		t.Fatalf("response=%+v err=%v", response, err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestDecodeRequestRejectsUnknownDuplicateAndOversized(t *testing.T) {
	for _, raw := range [][]byte{
		[]byte(`{"schema_version":"pysolate.capability-rpc.v1","channel_id":"c","channel_id":"d"}`),
		[]byte(`{"schema_version":"pysolate.capability-rpc.v1","channel_id":"c","invocation_id":"i","execution_id":"e","plan_sha256":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","call":{},"extra":1}`),
	} {
		if _, err := DecodeRequest(raw, 4096); err != ErrInvalidRequest {
			t.Fatalf("raw=%s err=%v", raw, err)
		}
	}
	if _, err := DecodeRequest(make([]byte, 32), 16); err != ErrInvalidRequest {
		t.Fatalf("oversize err=%v", err)
	}
}

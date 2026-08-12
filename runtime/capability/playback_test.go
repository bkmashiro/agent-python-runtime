package capability_test

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/playback"
)

type countingEvidenceHandler struct{ calls atomic.Uint32 }

func (handler *countingEvidenceHandler) Call(ctx context.Context, arguments json.RawMessage) (json.RawMessage, error) {
	result, _, err := handler.CallWithEvidence(ctx, arguments)
	return result, err
}

func (handler *countingEvidenceHandler) CallWithEvidence(context.Context, json.RawMessage) (json.RawMessage, capability.TransportEvidence, error) {
	handler.calls.Add(1)
	return json.RawMessage(`{"items":[]}`), capability.TransportEvidence{
		Kind: "http", Status: 200, MediaType: "application/json", BodyBytes: 12,
		BodySHA256: "sha256:" + repeatHex('a'),
	}, nil
}

func TestBrokerPlaybackReturnsRecordedResultWithoutLiveHandler(t *testing.T) {
	handler := &countingEvidenceHandler{}
	plan := capturedPlan(t, handler)
	arguments := json.RawMessage(`{}`)
	result := json.RawMessage(`{"items":[{"id":"a","score":1,"title":"recorded"}]}`)
	entry := playbackEntry(arguments, result)
	broker, err := capability.NewBroker(capability.Config{
		RunIdentity: "run-playback", Plan: plan,
		Playback: &capability.PlaybackConfig{Entries: []capability.TranscriptEntry{entry}},
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := broker.Call(context.Background(), []byte(`{"call_id":"call-1","capability":"sources.demo_catalog","arguments":{}}`))
	if err != nil || !json.Valid(response) || handler.calls.Load() != 0 {
		t.Fatalf("response=%s err=%v live calls=%d", response, err, handler.calls.Load())
	}
	var envelope map[string]json.RawMessage
	_ = json.Unmarshal(response, &envelope)
	if string(envelope["status"]) != `"ok"` || string(envelope["result"]) != string(result) {
		t.Fatalf("response=%s", response)
	}
	if err := broker.Finalize(true); err != nil {
		t.Fatal(err)
	}
	if len(broker.Receipts()) != 1 || broker.Receipts()[0].Outcome != "ok" {
		t.Fatalf("receipts=%+v", broker.Receipts())
	}
}

func TestBrokerPlaybackRejectsMismatchAndUnusedRecords(t *testing.T) {
	cases := map[string]string{
		"capability": `{"call_id":"call-1","capability":"workspace.read_text","arguments":{}}`,
		"arguments":  `{"call_id":"call-1","capability":"sources.demo_catalog","arguments":{"extra":1}}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			handler := &countingEvidenceHandler{}
			plan := capturedPlan(t, handler)
			entry := playbackEntry(json.RawMessage(`{}`), json.RawMessage(`{"items":[]}`))
			broker, err := capability.NewBroker(capability.Config{RunIdentity: "run-playback", Plan: plan, Playback: &capability.PlaybackConfig{Entries: []capability.TranscriptEntry{entry}}})
			if err != nil {
				t.Fatal(err)
			}
			response, err := broker.Call(context.Background(), []byte(raw))
			if err != nil || handler.calls.Load() != 0 || (!containsCode(response, "playback_mismatch") && !containsCode(response, "capability_denied") && !containsCode(response, "invalid_arguments")) {
				t.Fatalf("response=%s err=%v calls=%d", response, err, handler.calls.Load())
			}
			if err := broker.Finalize(false); err == nil {
				t.Fatal("unused playback record accepted")
			}
		})
	}
}

func TestBrokerPlaybackRejectsExtraCallAfterTranscript(t *testing.T) {
	handler := &countingEvidenceHandler{}
	plan := capturedPlan(t, handler)
	entry := playbackEntry(json.RawMessage(`{}`), json.RawMessage(`{"items":[]}`))
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "run-playback", Plan: plan, Playback: &capability.PlaybackConfig{Entries: []capability.TranscriptEntry{entry}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, callID := range []string{"call-1", "call-2"} {
		response, err := broker.Call(context.Background(), []byte(`{"call_id":"`+callID+`","capability":"sources.demo_catalog","arguments":{}}`))
		if err != nil {
			t.Fatal(err)
		}
		if callID == "call-2" && !containsCode(response, "playback_mismatch") {
			t.Fatalf("extra response=%s", response)
		}
	}
	if err := broker.Finalize(true); err == nil || handler.calls.Load() != 0 {
		t.Fatalf("finalize=%v live calls=%d", err, handler.calls.Load())
	}
}

func TestBrokerPlaybackConcurrentCallsRemainRaceFree(t *testing.T) {
	handler := &countingEvidenceHandler{}
	plan := capturedPlan(t, handler)
	first := playbackEntry(json.RawMessage(`{}`), json.RawMessage(`{"items":[]}`))
	second := first
	second.OperationIndex = 1
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "run-playback", Plan: plan, Playback: &capability.PlaybackConfig{Entries: []capability.TranscriptEntry{first, second}}})
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	errorsSeen := make(chan error, 2)
	for _, callID := range []string{"call-a", "call-b"} {
		wait.Add(1)
		go func(callID string) {
			defer wait.Done()
			response, callErr := broker.Call(context.Background(), []byte(`{"call_id":"`+callID+`","capability":"sources.demo_catalog","arguments":{}}`))
			if callErr != nil {
				errorsSeen <- callErr
				return
			}
			var envelope struct {
				Status string `json:"status"`
			}
			if json.Unmarshal(response, &envelope) != nil || envelope.Status != "ok" {
				errorsSeen <- capability.ErrPlaybackMismatch
			}
		}(callID)
	}
	wait.Wait()
	close(errorsSeen)
	for callErr := range errorsSeen {
		t.Fatal(callErr)
	}
	if err := broker.Finalize(true); err != nil || handler.calls.Load() != 0 {
		t.Fatalf("finalize=%v live calls=%d", err, handler.calls.Load())
	}
}

func TestBrokerPlaybackRevalidatesRecordedResultSchemaAndDigest(t *testing.T) {
	handler := &countingEvidenceHandler{}
	plan := capturedPlan(t, handler)
	entry := playbackEntry(json.RawMessage(`{}`), json.RawMessage(`{"items":[]}`))
	entry.Result = json.RawMessage(`{"wrong":true}`)
	entry.ResultSHA256 = playback.SHA256(entry.Result)
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "run-playback", Plan: plan, Playback: &capability.PlaybackConfig{Entries: []capability.TranscriptEntry{entry}}})
	if err != nil {
		t.Fatal(err)
	}
	response, err := broker.Call(context.Background(), []byte(`{"call_id":"call-1","capability":"sources.demo_catalog","arguments":{}}`))
	if err != nil || !containsCode(response, "invalid_result") || handler.calls.Load() != 0 {
		t.Fatalf("response=%s err=%v calls=%d", response, err, handler.calls.Load())
	}
}

func capturedPlan(t *testing.T, handler capability.Handler) *capability.Plan {
	t.Helper()
	policy := capability.DemoCatalogPolicy{Endpoint: "http://127.0.0.1:1/catalog", Timeout: 1000000000, MaxResponseBytes: 4096}
	spec, grant, err := capability.DemoCatalogDefinition(policy)
	if err != nil {
		t.Fatal(err)
	}
	registry := capability.NewRegistry()
	if err := registry.Register(spec, grant, handler); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 2})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func playbackEntry(arguments, result json.RawMessage) capability.TranscriptEntry {
	return capability.TranscriptEntry{
		OperationIndex: 0, Capability: "sources.demo_catalog",
		Arguments: arguments, ArgumentsSHA256: playback.SHA256(arguments),
		Result: result, ResultSHA256: playback.SHA256(result),
		Evidence: capability.TransportEvidence{Kind: "http", Status: 200, MediaType: "application/json", BodyBytes: uint32(len(result)), BodySHA256: "sha256:" + repeatHex('b')},
	}
}

func repeatHex(value byte) string {
	result := make([]byte, 64)
	for index := range result {
		result[index] = value
	}
	return string(result)
}

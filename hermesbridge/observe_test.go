package hermesbridge

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/agenttrace"
)

func validObserveRequest() ObserveRequest {
	return ObserveRequest{
		Version: ProtocolVersion, Operation: OperationObserve, RequestID: "observe-1",
		AgentRunID: "hermes-run-1", EventType: agenttrace.EventDirectToolCompleted,
		Payload: json.RawMessage(`{"tool_name":"read_file","status":"ok","duration_ms":4,"args_digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","args_bytes":12,"result_digest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","result_bytes":30}`),
	}
}

func TestDecodeObserveRequestAcceptsMetadataOnlyPayload(t *testing.T) {
	payload, err := json.Marshal(validObserveRequest())
	if err != nil {
		t.Fatal(err)
	}
	request, err := DecodeObserveRequest(payload)
	if err != nil {
		t.Fatal(err)
	}
	if request.EventType != agenttrace.EventDirectToolCompleted {
		t.Fatalf("unexpected event type %q", request.EventType)
	}
}

func TestDecodeObserveRequestRejectsRawOrUnknownPayloadFields(t *testing.T) {
	for _, payload := range []string{
		`{"tool_name":"read_file","status":"ok","content":"raw secret"}`,
		`{"tool_name":"read_file","status":"ok","result_digest":"not-a-digest"}`,
		`{"tool_name":"read_file","status":"ok","unknown":"value"}`,
	} {
		request := validObserveRequest()
		request.Payload = json.RawMessage(payload)
		encoded, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeObserveRequest(encoded); err == nil {
			t.Fatalf("expected metadata payload rejection for %s", payload)
		}
	}
}

func TestTraceManagerObservePersistsAgentMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.sqlite")
	store, err := agenttrace.OpenSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewTraceManager(store)
	if err != nil {
		t.Fatal(err)
	}
	request := validObserveRequest()
	event, err := manager.Observe(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if event.Sequence != 1 || event.EventType != agenttrace.EventDirectToolCompleted {
		t.Fatalf("unexpected event: %+v", event)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	readonly, err := agenttrace.OpenSQLiteStoreReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer readonly.Close()
	playback, err := readonly.LoadPlayback(context.Background(), request.AgentRunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(playback.Events) != 1 {
		t.Fatalf("unexpected playback event count: %+v", playback)
	}
	var want, got map[string]any
	if err := json.Unmarshal(request.Payload, &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(playback.Events[0].Payload, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) || got["tool_name"] != want["tool_name"] || got["result_digest"] != want["result_digest"] {
		t.Fatalf("unexpected playback payload: %v", got)
	}
}

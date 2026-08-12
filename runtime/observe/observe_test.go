package observe_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/observe"
)

func TestSessionAppendsCanonicalCausalEventsWithoutGaps(t *testing.T) {
	recorder := &memoryRecorder{}
	session, err := observe.NewSession(observe.Required, recorder, "exec-1")
	if err != nil {
		t.Fatal(err)
	}
	first, err := session.Append(context.Background(), "execution.started", nil, startedPayload())
	if err != nil {
		t.Fatal(err)
	}
	parent := first.Sequence
	second, err := session.Append(context.Background(), "execution.completed", &parent, completedPayload(true))
	if err != nil {
		t.Fatal(err)
	}
	if first.Sequence != 1 || second.Sequence != 2 || second.ParentSequence == nil || *second.ParentSequence != 1 {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	if len(recorder.events) != 2 || recorder.events[0].SchemaVersion != observe.SchemaVersion {
		t.Fatalf("events=%+v", recorder.events)
	}
	if session.ExecutionID() != "exec-1" || session.Mode() != observe.Required || session.Incomplete() {
		t.Fatalf("session identity or state changed")
	}
}

func TestRequiredAppendFailureDoesNotAdvanceSequence(t *testing.T) {
	sinkErr := errors.New("append failed")
	recorder := &memoryRecorder{failNext: sinkErr}
	session, err := observe.NewSession(observe.Required, recorder, "exec-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Append(context.Background(), "execution.started", nil, startedPayload()); !errors.Is(err, sinkErr) {
		t.Fatalf("required failure=%v", err)
	}
	event, err := session.Append(context.Background(), "execution.started", nil, startedPayload())
	if err != nil || event.Sequence != 1 {
		t.Fatalf("event=%+v err=%v", event, err)
	}
	if session.Incomplete() {
		t.Fatal("required sink rejection was incorrectly reported as accepted incomplete evidence")
	}
}

func TestBestEffortFailureMarksIncompleteAndKeepsNoSequenceGap(t *testing.T) {
	recorder := &memoryRecorder{failNext: errors.New("append failed")}
	session, err := observe.NewSession(observe.BestEffort, recorder, "exec-1")
	if err != nil {
		t.Fatal(err)
	}
	if event, err := session.Append(context.Background(), "execution.started", nil, startedPayload()); err != nil || event.Sequence != 0 {
		t.Fatalf("event=%+v err=%v", event, err)
	}
	if !session.Incomplete() {
		t.Fatal("best-effort loss not marked incomplete")
	}
	if _, err := session.Append(context.Background(), "execution.completed", nil, completedPayload(true)); !errors.Is(err, observe.ErrInvalidEvent) {
		t.Fatalf("incomplete session claimed complete evidence: %v", err)
	}
	event, err := session.Append(context.Background(), "execution.completed", nil, completedPayload(false))
	if err != nil || event.Sequence != 1 {
		t.Fatalf("event=%+v err=%v", event, err)
	}
}

func TestAppendCopiesPayloadParentAndRecorderValue(t *testing.T) {
	recorder := &retainingRecorder{mutateDuringAppend: true}
	session, err := observe.NewSession(observe.Required, recorder, "exec-1")
	if err != nil {
		t.Fatal(err)
	}
	first, err := session.Append(context.Background(), "execution.started", nil, startedPayload())
	if err != nil {
		t.Fatal(err)
	}
	payload := completedPayload(true)
	parent := first.Sequence
	event, err := session.Append(context.Background(), "execution.completed", &parent, payload)
	if err != nil {
		t.Fatal(err)
	}
	payload[0] = '['
	parent = 99
	if event.ParentSequence == nil || *event.ParentSequence != 1 || !bytes.Equal(event.Payload, completedPayload(true)) {
		t.Fatalf("returned event aliased append or Recorder input: %+v payload=%s", event, event.Payload)
	}

	stableRecorder := &retainingRecorder{}
	stableSession, err := observe.NewSession(observe.Required, stableRecorder, "exec-2")
	if err != nil {
		t.Fatal(err)
	}
	returned, err := stableSession.Append(context.Background(), "execution.started", nil, startedPayload())
	if err != nil {
		t.Fatal(err)
	}
	returned.Payload[0] = '['
	if len(stableRecorder.events) != 1 || stableRecorder.events[0].Payload[0] != '{' {
		t.Fatal("caller mutation reached Recorder-owned event")
	}
}

func TestAppendRejectsInvalidPayloadContract(t *testing.T) {
	recorder := &memoryRecorder{}
	session, err := observe.NewSession(observe.Required, recorder, "exec-1")
	if err != nil {
		t.Fatal(err)
	}
	invalidUTF8 := append([]byte(nil), completedPayload(true)...)
	invalidUTF8[len(invalidUTF8)-2] = 0xff
	oversized := append(json.RawMessage(`{"executed_code_sha256":"`+sha('a')+`","x":"`), bytes.Repeat([]byte{'x'}, observe.MaxPayloadBytes)...)
	oversized = append(oversized, []byte(`"}`)...)
	for name, bad := range map[string]json.RawMessage{
		"duplicate":                json.RawMessage(`{"evidence_complete":true,"evidence_complete":false,"result_sha256":"` + sha('a') + `","status":"ok"}`),
		"folded alias":             json.RawMessage(`{"Evidence_Complete":true,"result_sha256":"` + sha('a') + `","status":"ok"}`),
		"unknown":                  json.RawMessage(`{"evidence_complete":true,"result_sha256":"` + sha('a') + `","status":"ok","unknown":true}`),
		"missing":                  json.RawMessage(`{"evidence_complete":true,"status":"ok"}`),
		"explicit null":            json.RawMessage(`{"evidence_complete":null,"result_sha256":"` + sha('a') + `","status":"ok"}`),
		"trailing":                 append(completedPayload(true), []byte(`{}`)...),
		"invalid UTF-8":            invalidUTF8,
		"non-canonical whitespace": json.RawMessage(`{"evidence_complete": true,"result_sha256":"` + sha('a') + `","status":"ok"}`),
		"non-canonical key order":  json.RawMessage(`{"status":"ok","result_sha256":"` + sha('a') + `","evidence_complete":true}`),
		"wrong status":             json.RawMessage(`{"evidence_complete":true,"result_sha256":"` + sha('a') + `","status":"error"}`),
		"uppercase digest":         json.RawMessage(`{"evidence_complete":true,"result_sha256":"sha256:` + strings.Repeat("A", 64) + `","status":"ok"}`),
		"non-object":               json.RawMessage(`[]`),
		"oversized":                oversized,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := session.Append(context.Background(), "execution.completed", nil, bad); !errors.Is(err, observe.ErrInvalidEvent) {
				t.Fatalf("invalid payload accepted or wrong error: %q err=%v", bad, err)
			}
		})
	}
	if len(recorder.events) != 0 {
		t.Fatalf("invalid payloads reached Recorder: %+v", recorder.events)
	}
	parent := uint32(99)
	if _, err := session.Append(context.Background(), "execution.started", &parent, startedPayload()); !errors.Is(err, observe.ErrInvalidCausalParent) {
		t.Fatalf("nonexistent parent error=%v", err)
	}
	zero := uint32(0)
	if _, err := session.Append(context.Background(), "execution.started", &zero, startedPayload()); !errors.Is(err, observe.ErrInvalidCausalParent) {
		t.Fatalf("zero parent error=%v", err)
	}
	if _, err := session.Append(nil, "execution.started", nil, startedPayload()); !errors.Is(err, observe.ErrInvalidEvent) {
		t.Fatalf("nil context error=%v", err)
	}
}

func TestAppendRejectsNonCanonicalAndNestedDuplicatePayload(t *testing.T) {
	recorder := &memoryRecorder{}
	session, err := observe.NewSession(observe.Required, recorder, "exec-1")
	if err != nil {
		t.Fatal(err)
	}
	for name, payload := range map[string]json.RawMessage{
		"non-canonical whitespace": json.RawMessage(`{"status": "ok"}`),
		"nested duplicate":         json.RawMessage(`{"status":{"name":"ok","name":"error"}}`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := session.Append(context.Background(), "execution.completed", nil, payload); err == nil {
				t.Fatalf("invalid payload accepted: %s", payload)
			}
		})
	}
}

func TestTypedPayloadContracts(t *testing.T) {
	valid := []struct {
		kind    string
		payload json.RawMessage
	}{
		{"execution.started", mustJSON(t, map[string]any{
			"artifact_sha256": sha('a'), "capability_plan_sha256": sha('b'), "deterministic_profile_sha256": sha('c'),
			"executed_code_sha256": sha('d'), "execution_profile_sha256": sha('e'),
		})},
		{"execution.completed", completedPayload(false)},
		{"execution.failed", mustJSON(t, map[string]any{"error_class": "guest.error", "evidence_complete": true, "status": "error"})},
		{"execution.failed", mustJSON(t, map[string]any{"error_class": "guest.error", "evidence_complete": true, "result_sha256": sha('a'), "status": "error"})},
		{"capability.plan_bound", mustJSON(t, map[string]any{"capability_plan_sha256": sha('a')})},
		{"capability.call", mustJSON(t, map[string]any{
			"arguments_sha256": sha('a'), "capability": "sources.demo_catalog", "operation_index": uint32(0), "outcome": "ok", "result_sha256": sha('b'),
		})},
		{"capability.call", mustJSON(t, map[string]any{
			"arguments_sha256": sha('a'), "capability": "sources.demo_catalog", "operation_index": uint32(1), "outcome": "denied",
		})},
		{"workspace.finalized", mustJSON(t, map[string]any{
			"changes": []any{}, "changes_truncated": false,
			"entry_count": uint32(0), "final_tree_sha256": sha('a'), "final_workspace_sha256": sha('b'),
			"initial_workspace_sha256": sha('c'), "syscall_order_available": false, "total_bytes": uint64(0),
		})},
	}
	recorder := &memoryRecorder{}
	session, err := observe.NewSession(observe.Required, recorder, "exec-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range valid {
		if _, err := session.Append(context.Background(), item.kind, nil, item.payload); err != nil {
			t.Fatalf("kind=%s payload=%s err=%v", item.kind, item.payload, err)
		}
	}
	if len(recorder.events) != len(valid) {
		t.Fatalf("events=%d want=%d", len(recorder.events), len(valid))
	}

	invalid := []struct {
		kind    string
		payload json.RawMessage
	}{
		{"execution.started", mustJSON(t, map[string]any{})},
		{"execution.started", mustJSON(t, map[string]any{"artifact_sha256": "", "executed_code_sha256": sha('a')})},
		{"execution.failed", mustJSON(t, map[string]any{"error_class": "bad class", "evidence_complete": true, "status": "error"})},
		{"execution.failed", mustJSON(t, map[string]any{"error_class": "guest.error", "evidence_complete": true, "status": "ok"})},
		{"capability.plan_bound", mustJSON(t, map[string]any{"capability_plan_sha256": ""})},
		{"capability.call", mustJSON(t, map[string]any{
			"arguments_sha256": sha('a'), "capability": "Sources.Demo", "operation_index": uint32(0), "outcome": "ok",
		})},
		{"capability.call", mustJSON(t, map[string]any{
			"arguments_sha256": sha('a'), "capability": "sources." + strings.Repeat("a", 121), "operation_index": uint32(0), "outcome": "ok",
		})},
		{"capability.call", mustJSON(t, map[string]any{
			"arguments_sha256": sha('a'), "capability": "sources.demo", "operation_index": uint32(0), "outcome": "error", "result_sha256": sha('b'),
		})},
		{"capability.call", json.RawMessage(`{"arguments_sha256":"` + sha('a') + `","capability":"sources.demo","operation_index":4294967296,"outcome":"ok"}`)},
		{"workspace.finalized", mustJSON(t, map[string]any{
			"entry_count": uint32(0), "final_tree_sha256": sha('a'), "final_workspace_sha256": sha('b'), "total_bytes": uint64(0),
		})},
	}
	for _, item := range invalid {
		if _, err := session.Append(context.Background(), item.kind, nil, item.payload); !errors.Is(err, observe.ErrInvalidEvent) {
			t.Fatalf("invalid kind=%s payload=%s err=%v", item.kind, item.payload, err)
		}
	}
}

func TestEventCanonicalEncodeDecodeAndTamperRejection(t *testing.T) {
	parent := uint32(1)
	event := observe.Event{
		SchemaVersion: observe.SchemaVersion, ExecutionID: "exec-1", Sequence: 2, ParentSequence: &parent,
		Type: "execution.completed", Payload: completedPayload(true),
	}
	encoded, err := observe.Encode(event)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"schema_version":"pysolate.runtime-observation.v1","execution_id":"exec-1","sequence":2,"parent_sequence":1,"type":"execution.completed","payload":` + string(completedPayload(true)) + `}`
	if string(encoded) != want {
		t.Fatalf("encoded=%s\nwant=%s", encoded, want)
	}
	decoded, err := observe.Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	encoded[0] = '['
	parent = 99
	if decoded.ParentSequence == nil || *decoded.ParentSequence != 1 || decoded.Payload[0] != '{' {
		t.Fatalf("decoded aliases input: %+v", decoded)
	}
	reencoded, err := observe.Encode(decoded)
	if err != nil || string(reencoded) != want {
		t.Fatalf("reencoded=%s err=%v", reencoded, err)
	}

	valid := []byte(want)
	invalidUTF8 := append([]byte(nil), valid...)
	invalidUTF8[len(invalidUTF8)-2] = 0xff
	duplicate := bytes.Replace(valid, []byte(`{"schema_version":`), []byte(`{"schema_version":"pysolate.runtime-observation.v1","schema_version":`), 1)
	folded := bytes.Replace(valid, []byte(`"schema_version"`), []byte(`"Schema_Version"`), 1)
	unknown := bytes.Replace(valid, []byte(`,"payload":`), []byte(`,"unknown":true,"payload":`), 1)
	nullParent := bytes.Replace(valid, []byte(`"parent_sequence":1`), []byte(`"parent_sequence":null`), 1)
	nestedDuplicate := bytes.Replace(valid, completedPayload(true), []byte(`{"evidence_complete":true,"result_sha256":{"x":1,"x":2},"status":"ok"}`), 1)
	for name, raw := range map[string][]byte{
		"invalid UTF-8":      invalidUTF8,
		"duplicate":          duplicate,
		"folded alias":       folded,
		"unknown":            unknown,
		"explicit null":      nullParent,
		"nested duplicate":   nestedDuplicate,
		"trailing":           append(append([]byte(nil), valid...), '\n'),
		"leading whitespace": append([]byte{' '}, valid...),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := observe.Decode(raw); !errors.Is(err, observe.ErrInvalidEvent) {
				t.Fatalf("tampered envelope accepted: %q err=%v", raw, err)
			}
		})
	}

	badParent := event
	badParent.Sequence = 1
	if _, err := observe.Encode(badParent); !errors.Is(err, observe.ErrInvalidCausalParent) {
		t.Fatalf("forward/self parent error=%v", err)
	}
}

func TestSessionConcurrentAppendIsGapFree(t *testing.T) {
	const count = 128
	recorder := &memoryRecorder{}
	session, err := observe.NewSession(observe.Required, recorder, "exec-concurrent")
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	errorsSeen := make(chan error, count)
	sequences := make(chan uint32, count)
	for index := 0; index < count; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			event, err := session.Append(context.Background(), "execution.started", nil, startedPayload())
			if err != nil {
				errorsSeen <- err
				return
			}
			sequences <- event.Sequence
		}()
	}
	wait.Wait()
	close(errorsSeen)
	close(sequences)
	for err := range errorsSeen {
		t.Fatal(err)
	}
	got := make([]int, 0, count)
	for sequence := range sequences {
		got = append(got, int(sequence))
	}
	sort.Ints(got)
	if len(got) != count || len(recorder.events) != count {
		t.Fatalf("sequences=%d events=%d", len(got), len(recorder.events))
	}
	for index, sequence := range got {
		if sequence != index+1 || recorder.events[index].Sequence != uint32(index+1) {
			t.Fatalf("index=%d returned=%d recorded=%d", index, sequence, recorder.events[index].Sequence)
		}
	}
}

func TestSessionEventBound(t *testing.T) {
	session, err := observe.NewSession(observe.Required, discardRecorder{}, "exec-bounded")
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < observe.MaxEventsPerExecution; index++ {
		event, err := session.Append(context.Background(), "execution.started", nil, startedPayload())
		if err != nil || event.Sequence != uint32(index+1) {
			t.Fatalf("index=%d event=%+v err=%v", index, event, err)
		}
	}
	if _, err := session.Append(context.Background(), "execution.started", nil, startedPayload()); !errors.Is(err, observe.ErrEventLimitExceeded) {
		t.Fatalf("event limit error=%v", err)
	}
}

func TestOffModeRequiresNoRecorderWork(t *testing.T) {
	recorder := &memoryRecorder{failNext: errors.New("must not be called")}
	session, err := observe.NewSession(observe.Off, recorder, "exec-1")
	if err != nil {
		t.Fatal(err)
	}
	event, err := session.Append(nil, "forged.kind", nil, json.RawMessage(`not-json`))
	if err != nil || event.Sequence != 0 || session.Incomplete() {
		t.Fatalf("event=%+v err=%v incomplete=%v", event, err, session.Incomplete())
	}
	if recorder.calls != 0 {
		t.Fatalf("off mode called Recorder %d times", recorder.calls)
	}
	if _, err := observe.NewSession(observe.Required, nil, "exec-1"); !errors.Is(err, observe.ErrInvalidSession) {
		t.Fatalf("required mode without Recorder err=%v", err)
	}
	if _, err := observe.NewSession(observe.Mode("REQUIRED"), recorder, "exec-1"); !errors.Is(err, observe.ErrInvalidSession) {
		t.Fatalf("folded mode accepted: %v", err)
	}
	if _, err := observe.NewSession(observe.Off, nil, "bad execution id"); !errors.Is(err, observe.ErrInvalidSession) {
		t.Fatalf("invalid execution identity accepted: %v", err)
	}
}

func startedPayload() json.RawMessage {
	return json.RawMessage(`{"executed_code_sha256":"` + sha('a') + `"}`)
}

func completedPayload(complete bool) json.RawMessage {
	return json.RawMessage(`{"evidence_complete":` + boolString(complete) + `,"result_sha256":"` + sha('b') + `","status":"ok"}`)
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func sha(character byte) string {
	return "sha256:" + strings.Repeat(string(character), 64)
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

type memoryRecorder struct {
	mu       sync.Mutex
	events   []observe.Event
	failNext error
	calls    int
}

func (recorder *memoryRecorder) Append(_ context.Context, event observe.Event) error {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.calls++
	if recorder.failNext != nil {
		err := recorder.failNext
		recorder.failNext = nil
		return err
	}
	event.Payload = append(json.RawMessage(nil), event.Payload...)
	if event.ParentSequence != nil {
		parent := *event.ParentSequence
		event.ParentSequence = &parent
	}
	recorder.events = append(recorder.events, event)
	return nil
}

type retainingRecorder struct {
	events             []observe.Event
	mutateDuringAppend bool
}

func (recorder *retainingRecorder) Append(_ context.Context, event observe.Event) error {
	recorder.events = append(recorder.events, event)
	if recorder.mutateDuringAppend {
		event.Payload[0] = '['
		if event.ParentSequence != nil {
			*event.ParentSequence = 99
		}
	}
	return nil
}

type discardRecorder struct{}

func (discardRecorder) Append(context.Context, observe.Event) error { return nil }

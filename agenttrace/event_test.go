package agenttrace_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/agenttrace"
)

type failingSink struct{}

func (failingSink) Append(context.Context, agenttrace.Event) error {
	return errors.New("store unavailable")
}

type failsOnceSink struct {
	failed bool
	store  agenttrace.MemorySink
}

func (sink *failsOnceSink) Append(ctx context.Context, event agenttrace.Event) error {
	if !sink.failed {
		sink.failed = true
		return errors.New("transient")
	}
	return sink.store.Append(ctx, event)
}

func TestRecorderProducesBoundedMetadataOnlyEvents(t *testing.T) {
	sink := agenttrace.NewMemorySink()
	plugin := agenttrace.Plugin{Mode: agenttrace.ModeRequired, Sink: sink}
	recorder, err := plugin.Begin("agent-run-1", func() time.Time { return time.Unix(123, 0).UTC() })
	if err != nil {
		t.Fatal(err)
	}
	event, err := recorder.Record(context.Background(), agenttrace.EventLLMResponseReceived, "", json.RawMessage(`{"request_digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","status_code":200}`), "")
	if err != nil {
		t.Fatal(err)
	}
	if event.AgentRunID != "agent-run-1" || event.Sequence != 1 || event.EventID == "" || event.PayloadDigest == "" || !event.ObservedAt.Equal(time.Unix(123, 0).UTC()) {
		t.Fatalf("event=%+v", event)
	}
	if err := event.Validate(); err != nil {
		t.Fatal(err)
	}
	event.Payload[1] = 'X'
	stored := sink.Events()
	if len(stored) != 1 || stored[0].Validate() != nil {
		t.Fatalf("stored event mutated through caller alias: %+v", stored)
	}
	if _, err := recorder.Record(context.Background(), agenttrace.EventLLMResponseReceived, "", json.RawMessage(`{"prompt":"secret"}`), ""); !errors.Is(err, agenttrace.ErrSensitivePayload) {
		t.Fatalf("err=%v", err)
	}
	if _, err := recorder.Record(context.Background(), agenttrace.EventLLMResponseReceived, "", json.RawMessage(`{"Prompt":"secret"}`), ""); !errors.Is(err, agenttrace.ErrSensitivePayload) {
		t.Fatalf("folded sensitive key err=%v", err)
	}
}

func TestPluginModesBoundTraceFailureWithoutChangingBestEffortExecution(t *testing.T) {
	bestEffort, err := (agenttrace.Plugin{Mode: agenttrace.ModeBestEffort, Sink: failingSink{}}).Begin("agent-run-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bestEffort.Record(context.Background(), agenttrace.EventRunStarted, "", json.RawMessage(`{"task_digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`), ""); err != nil || bestEffort.Dropped() != 1 {
		t.Fatalf("err=%v dropped=%d", err, bestEffort.Dropped())
	}
	required, err := (agenttrace.Plugin{Mode: agenttrace.ModeRequired, Sink: failingSink{}}).Begin("agent-run-2", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := required.Record(context.Background(), agenttrace.EventRunStarted, "", json.RawMessage(`{}`), ""); !errors.Is(err, agenttrace.ErrAppend) {
		t.Fatalf("err=%v", err)
	}
}

func TestBestEffortDropDoesNotCreateSequenceOrParentGap(t *testing.T) {
	sink := &failsOnceSink{}
	recorder, err := (agenttrace.Plugin{Mode: agenttrace.ModeBestEffort, Sink: sink}).Begin("agent-run-1", func() time.Time { return time.Unix(1, 0) })
	if err != nil {
		t.Fatal(err)
	}
	dropped, err := recorder.Record(context.Background(), agenttrace.EventRunStarted, "", json.RawMessage(`{}`), "")
	if err != nil || dropped.EventID != "" {
		t.Fatalf("dropped=%+v err=%v", dropped, err)
	}
	stored, err := recorder.Record(context.Background(), agenttrace.EventRunStarted, "", json.RawMessage(`{}`), "")
	if err != nil || stored.Sequence != 1 || len(sink.store.Events()) != 1 || recorder.Dropped() != 1 {
		t.Fatalf("stored=%+v err=%v events=%d dropped=%d", stored, err, len(sink.store.Events()), recorder.Dropped())
	}
}

func TestTracePluginContextIsExplicitAndOptional(t *testing.T) {
	if _, ok := agenttrace.PluginFromContext(context.Background()); ok {
		t.Fatal("unexpected ambient trace plugin")
	}
	plugin := agenttrace.Plugin{Mode: agenttrace.ModeOff}
	ctx, err := agenttrace.WithPlugin(context.Background(), plugin)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := agenttrace.PluginFromContext(ctx)
	if !ok || got.Mode != agenttrace.ModeOff {
		t.Fatalf("got=%+v ok=%v", got, ok)
	}
}

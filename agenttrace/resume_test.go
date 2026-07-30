package agenttrace

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestPluginResumeContinuesVerifiedPlaybackSequence(t *testing.T) {
	sink := &MemorySink{}
	plugin := Plugin{Mode: ModeRequired, Sink: sink}
	clock := func() time.Time { return time.Unix(100, 0).UTC() }
	recorder, err := plugin.Begin("agent-run-resume", clock)
	if err != nil {
		t.Fatal(err)
	}
	first, err := recorder.Record(context.Background(), EventRuntimeStarted, "", json.RawMessage(`{"status":"started"}`), "")
	if err != nil {
		t.Fatal(err)
	}
	playback := Playback{AgentRunID: "agent-run-resume", Events: []Event{first}}

	resumed, err := plugin.Resume(playback, clock)
	if err != nil {
		t.Fatal(err)
	}
	second, err := resumed.Record(context.Background(), EventRuntimeCompleted, first.EventID, json.RawMessage(`{"status":"ok"}`), "")
	if err != nil {
		t.Fatal(err)
	}
	if second.Sequence != 2 || second.ParentEventID != first.EventID {
		t.Fatalf("unexpected resumed event: %#v", second)
	}
}

func TestPluginResumeRejectsTamperedPlayback(t *testing.T) {
	plugin := Plugin{Mode: ModeRequired, Sink: &MemorySink{}}
	playback := Playback{AgentRunID: "agent-run-resume", Events: []Event{{AgentRunID: "agent-run-resume", Sequence: 1}}}
	if _, err := plugin.Resume(playback, nil); err == nil {
		t.Fatal("tampered playback was accepted")
	}
}

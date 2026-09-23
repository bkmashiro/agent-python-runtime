package main

import (
	"context"
	"os"
	"testing"
)

func TestPrivateCodeRecordingReexecutesOfflineWithoutFixtureDispatch(t *testing.T) {
	task := allTasks()["lookup"]
	host := &episodeHost{}
	runner, setup, err := prepareRunner(context.Background(), mustTestGuest(t), task, host)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(context.Background())

	recorded := &privateEpisodeRecording{}
	liveProvider := &recordingProvider{inner: &fakeProvider{arm: "code", task: "lookup"}, episode: recorded}
	live := runEpisode(context.Background(), liveProvider, &requestBudget{limit: 8}, task, 1, 1, "code", runner, setup, host, "fake-requested", 4, 0)
	recorded.Row = live
	if live.CompletionStatus != "completed" || len(recorded.ProviderCalls) != 2 || len(recorded.Executions) != 1 || len(recorded.Executions[0].ToolCalls) != 1 {
		t.Fatalf("incomplete recording: status=%s providers=%d executions=%d", live.CompletionStatus, len(recorded.ProviderCalls), len(recorded.Executions))
	}

	playback := &playbackProvider{episode: recorded, model: "fake-requested"}
	offline := runEpisode(context.Background(), playback, &requestBudget{limit: 8}, task, 1, 1, "code", runner, setup, host, "fake-requested", 4, 0)
	if err := comparePrivateEpisodeRows(live, offline); err != nil {
		t.Fatal(err)
	}
	if playback.index != len(recorded.ProviderCalls) {
		t.Fatalf("provider calls consumed=%d want=%d", playback.index, len(recorded.ProviderCalls))
	}
}

func mustTestGuest(t *testing.T) []byte {
	t.Helper()
	path := testGuestPath(t)
	wasm, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return wasm
}

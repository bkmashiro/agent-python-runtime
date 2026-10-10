package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestPartialEffectsRecordedGuestReplay(t *testing.T) {
	for _, good := range []bool{true, false} {
		t.Run(fmt.Sprint(good), func(t *testing.T) {
			ctx := context.Background()
			wasm := mustTestGuest(t)
			task := partialEffectsTask("docs")
			host := &episodeHost{}
			runner, setup, err := prepareRunner(ctx, wasm, task, host)
			if err != nil {
				t.Fatal(err)
			}
			defer runner.Close(ctx)
			source := "r=credits.apply(accounts=['A','B','C'], amount_cents=100)\n"
			if good {
				source += "pending=[i['account'] for i in r['items'] if i['status']=='not_applied']\ncredits.apply(accounts=pending, amount_cents=100)\n"
			} else {
				source += "credits.apply(accounts=['A','B','C'], amount_cents=100)\n"
			}
			source += "result=credits.balances()"
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				message := toolReply("submit", "submit_answer", map[string]any{"done": true})
				if n == 1 {
					message = toolReply("exec", "execute_python", map[string]any{"source": source})
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"model": "fixture-model", "choices": []any{map[string]any{"message": message}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}})
			}))
			defer server.Close()
			path := filepath.Join(t.TempDir(), "capture.jsonl")
			header := privateRecordingHeader{Kind: "header", Version: privateRecordingVersion, PrivacyWarning: privateRecordingWarning, FixtureVersion: "agent-eval-v2", RequestedModel: "fixture-model", MaxTurns: 4, MaxRequests: 8, GuestSHA256: fmt.Sprintf("sha256:%x", sha256.Sum256(wasm))}
			writer, err := newPrivateRecordingWriter(path, header)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			ep := &privateEpisodeRecording{writer: writer}
			provider := &recordingProvider{inner: &OpenAIProvider{Client: server.Client(), BaseURL: server.URL, Model: "fixture-model"}, episode: ep}
			row := runEpisode(ctx, provider, &requestBudget{limit: 8}, task, 1, 1, "code", runner, setup, host, "fixture-model", 4, 0)
			if row.Correctness == nil || *row.Correctness != good {
				t.Fatalf("wrong grading: %+v", row)
			}
			ep.Row = row
			if err := writer.Append(*ep); err != nil {
				t.Fatal(err)
			}
			if err := writer.Finish(1); err != nil {
				t.Fatal(err)
			}
			writer.Close()
			server.Close()
			if _, err := readPrivateRecording(path); err != nil {
				t.Fatal(err)
			}
			if err := replayPrivateCampaign(ctx, path, filepath.Join(t.TempDir(), "replay.jsonl"), wasm, []taskCase{task}, 4, 8); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 2 {
				t.Fatal("replay contacted provider")
			}
		})
	}
}

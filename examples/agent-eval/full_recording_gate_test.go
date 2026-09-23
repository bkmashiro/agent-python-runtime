package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func toolReply(id, name string, args any) Message {
	data, _ := json.MarshalIndent(args, "", " ")
	return Message{Role: "assistant", ToolCalls: []ToolCall{{ID: id, Type: "function", Function: ToolFunction{Name: name, Arguments: string(data)}}}}
}

func TestFullHTTPReplay(t *testing.T) {
	ctx := context.Background()
	wasm := mustTestGuest(t)
	task := lookupTask()
	host := &episodeHost{}
	runner, setup, err := prepareRunner(ctx, wasm, task, host)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close(ctx)
	path := filepath.Join(t.TempDir(), "capture.jsonl")
	secret := "fixture-only-auth-secret"
	header := privateRecordingHeader{Kind: "header", Version: privateRecordingVersion, PrivacyWarning: privateRecordingWarning, FixtureVersion: "agent-eval-v2", RequestedModel: "fixture-model", MaxTurns: 4, MaxRequests: 8, GuestSHA256: fmt.Sprintf("sha256:%x", sha256.Sum256(wasm))}
	writer, err := newPrivateRecordingWriter(path, header, secret)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			prefix, _ := readPrivateRecording(path)
			if len(prefix.Episodes) != 1 || len(prefix.Episodes[0].ProviderCalls) != 1 || prefix.Episodes[0].ProviderCalls[0].Completed {
				t.Error("request not checkpointed before HTTP")
			}
		}
		var message Message
		if n == 1 {
			message = toolReply("exec", "execute_python", map[string]any{"source": "import random,time,os\nquote=catalog.price(sku='SKU-3')\nos.write(1,b'\\xff')\nos.write(2,b'\\xfe')\nresult={'quote':quote,'nonce':random.getrandbits(64),'clock':time.time()}"})
		} else {
			message = toolReply("answer", "submit_answer", map[string]any{"price_cents": 3199})
		}
		message.ReasoningContent = "retained reasoning " + secret
		_ = json.NewEncoder(w).Encode(map[string]any{"model": "fixture-model", "choices": []any{map[string]any{"message": message}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}})
	}))
	defer server.Close()
	ep := &privateEpisodeRecording{writer: writer}
	provider := &recordingProvider{inner: &OpenAIProvider{Client: server.Client(), BaseURL: server.URL, APIKey: secret, Model: "fixture-model"}, episode: ep}
	row := runEpisode(ctx, provider, &requestBudget{limit: 8}, task, 1, 1, "code", runner, setup, host, "fixture-model", 4, 0)
	if row.CompletionStatus != "completed" {
		t.Fatalf("capture status %s", row.CompletionStatus)
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
	rec, err := readPrivateRecording(path)
	if err != nil {
		t.Fatal(err)
	}
	ex := rec.Episodes[0].Executions[0]
	if !bytes.Equal(ex.Stdout, []byte{255}) || !bytes.Equal(ex.Stderr, []byte{254}) {
		t.Fatal("raw IO lost")
	}
	if len(rec.Episodes[0].ProviderCalls[0].Response.Message.ReasoningContent) == 0 {
		t.Fatal("reasoning lost")
	}
	raw, _ := os.ReadFile(path)
	if bytes.Contains(raw, []byte(secret)) {
		t.Fatal("credential leaked")
	}
	output := filepath.Join(t.TempDir(), "replay.jsonl")
	if err := replayPrivateCampaign(ctx, path, output, wasm, []taskCase{task}, 4, 8); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("offline provider dispatch")
	}
	fmt.Println("full recording verified: raw IO and seeded execution; no provider reuse")
}

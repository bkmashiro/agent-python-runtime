package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateRecordingPreservesProviderBodiesByteForByte(t *testing.T) {
	raw := []byte(" {\"choices\": [\"<not-json>\"]}\n\x00 ")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write(raw)
	}))
	defer server.Close()
	ep := &privateEpisodeRecording{}
	secret := "must-not-be-recorded"
	provider := &recordingProvider{inner: &OpenAIProvider{Client: server.Client(), BaseURL: server.URL, APIKey: secret, Model: "test"}, episode: ep}
	_, err := provider.Complete(context.Background(), []Message{{Role: "user", Content: "<input>"}}, nil, 17)
	if err == nil || len(ep.ProviderCalls) != 1 {
		t.Fatalf("provider error/call = %v/%d", err, len(ep.ProviderCalls))
	}
	if got := ep.ProviderCalls[0].ResponseBody; string(got) != string(raw) {
		t.Fatalf("response body changed: %q", got)
	}
	if got := ep.ProviderCalls[0].ErrorBody; string(got) != string(raw) {
		t.Fatalf("error body changed: %q", got)
	}
	if string(ep.ProviderCalls[0].RequestBody) == "" {
		t.Fatal("request body was not recorded")
	}

	path := filepath.Join(t.TempDir(), "recording.jsonl")
	writer, err := newPrivateRecordingWriter(path, privateRecordingHeader{Kind: "header", Version: privateRecordingVersion, PrivacyWarning: privateRecordingWarning}, secret)
	if err != nil {
		t.Fatal(err)
	}
	ep.Kind = "episode"
	ep.Task, ep.Arm, ep.Seed = "lookup", "direct", "seed"
	if err := writer.Append(*ep); err != nil {
		t.Fatal(err)
	}
	if err := writer.Finish(1); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	decoded, err := readPrivateRecording(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded.Episodes[0].ProviderCalls[0].ResponseBody) != string(raw) {
		t.Fatalf("decoded response body changed: %q", decoded.Episodes[0].ProviderCalls[0].ResponseBody)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var lines []map[string]any
	for _, line := range splitJSONLines(data) {
		var value map[string]any
		if err := json.Unmarshal(line, &value); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, value)
	}
	if len(lines) != 3 {
		t.Fatalf("JSONL lines=%d", len(lines))
	}
	if _, ok := lines[1]["provider_calls"]; !ok {
		t.Fatal("episode line missing provider_calls")
	}
	if bytes.Contains(data, []byte("must-not-be-recorded")) {
		t.Fatal("API key was persisted")
	}
}

func splitJSONLines(data []byte) [][]byte {
	var lines [][]byte
	for len(data) > 0 {
		idx := 0
		for idx < len(data) && data[idx] != '\n' {
			idx++
		}
		if idx > 0 {
			lines = append(lines, append([]byte(nil), data[:idx]...))
		}
		if idx == len(data) {
			break
		}
		data = data[idx+1:]
	}
	return lines
}

func TestPrivateRecordingProgressSurvivesCrashPrefixAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recording.jsonl")
	header := privateRecordingHeader{Kind: "header", Version: privateRecordingVersion, PrivacyWarning: privateRecordingWarning}
	writer, err := newPrivateRecordingWriter(path, header)
	if err != nil {
		t.Fatal(err)
	}
	ep := privateEpisodeRecording{Kind: "episode", Task: "lookup", Arm: "code", Seed: "seed", writer: writer}
	ep.ProviderCalls = []privateProviderExchange{{Transport: "test", RequestBody: []byte("wire request"), Messages: []Message{{Role: "user", Content: "prompt"}}}}
	if err := ep.checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	decoded, err := readPrivateRecording(path)
	if err == nil || len(decoded.Episodes) != 1 || !bytes.Equal(decoded.Episodes[0].ProviderCalls[0].RequestBody, []byte("wire request")) {
		t.Fatalf("crash prefix=%+v err=%v", decoded, err)
	}

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newPrivateRecordingWriter(path, header); !os.IsExist(err) {
		t.Fatalf("existing crash prefix must not be appended: %v", err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("crash prefix changed")
	}
	path = filepath.Join(t.TempDir(), "completed.jsonl")
	writer, err = newPrivateRecordingWriter(path, header)
	if err != nil {
		t.Fatal(err)
	}
	ep.writer = writer
	ep.ProviderCalls[0].Completed = true
	if err := writer.Append(ep); err != nil {
		t.Fatal(err)
	}
	if err := writer.Finish(1); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	decoded, err = readPrivateRecording(path)
	if err != nil || len(decoded.Episodes) != 1 || !bytes.Equal(decoded.Episodes[0].ProviderCalls[0].RequestBody, []byte("wire request")) {
		t.Fatalf("reopened recording=%+v err=%v", decoded, err)
	}
}

func TestPrivateRecordingRedactsSecretsWithoutChangingOtherRawBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recording.jsonl")
	secret := "credential-123"
	writer, err := newPrivateRecordingWriter(path, privateRecordingHeader{Kind: "header", Version: privateRecordingVersion, PrivacyWarning: privateRecordingWarning}, secret)
	if err != nil {
		t.Fatal(err)
	}
	untouched := []byte{' ', 0, '{', 'n', 'o', 't', '-', 'j', 's', 'o', 'n', '}'}
	ep := privateEpisodeRecording{Kind: "episode", Task: "lookup", Arm: "direct", Seed: "seed", ProviderCalls: []privateProviderExchange{{Completed: true, Transport: "http", RequestBody: []byte("prefix-" + secret + "-suffix"), ResponseBody: untouched}}}
	if err := writer.Append(ep); err != nil {
		t.Fatal(err)
	}
	if err := writer.Finish(1); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(secret)) {
		t.Fatal("configured secret was persisted")
	}
	decoded, err := readPrivateRecording(path)
	if err != nil {
		t.Fatal(err)
	}
	call := decoded.Episodes[0].ProviderCalls[0]
	if bytes.Contains(call.RequestBody, []byte(secret)) || !bytes.Equal(call.ResponseBody, untouched) {
		t.Fatalf("redaction changed bytes incorrectly: request=%q response=%v", call.RequestBody, call.ResponseBody)
	}
}

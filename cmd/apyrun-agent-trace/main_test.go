package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/agenttrace"
)

func traceFixture(t *testing.T) (string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "trace.sqlite")
	store, err := agenttrace.OpenSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	recorder, err := (agenttrace.Plugin{Mode: agenttrace.ModeRequired, Sink: store}).Begin("agent-run-1", func() time.Time { return time.Unix(1, 0) })
	if err != nil {
		t.Fatal(err)
	}
	first, err := recorder.Record(context.Background(), agenttrace.EventRunStarted, "", json.RawMessage(`{"spec_digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`), "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = recorder.Record(context.Background(), agenttrace.EventCheckpointCreated, first.EventID, json.RawMessage(`{"state_digest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}`), "sha256:"+strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	return path, "agent-run-1"
}

func TestTraceCLIQueriesStatsExportsAndPlansFork(t *testing.T) {
	path, runID := traceFixture(t)
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"runs", []string{"--db", path, "--op", "runs"}, runID},
		{"events", []string{"--db", path, "--op", "events", "--run", runID}, `"event_type":"agent.run.started"`},
		{"stats", []string{"--db", path, "--op", "stats", "--run", runID}, `"integrity_digest":"sha256:`},
		{"fork", []string{"--db", path, "--op", "fork", "--run", runID, "--sequence", "2", "--new-run", "agent-run-child"}, `"agent_run_id":"agent-run-child"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout bytes.Buffer
			if err := run(context.Background(), tc.args, &stdout); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(stdout.String(), tc.want) {
				t.Fatalf("output=%s", stdout.String())
			}
		})
	}

	out := filepath.Join(t.TempDir(), "events.jsonl")
	var stdout bytes.Buffer
	if err := run(context.Background(), []string{"--db", path, "--op", "export", "--run", runID, "--out", out}, &stdout); err != nil {
		t.Fatal(err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout=%s", stdout.String())
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(strings.TrimSpace(string(body)), "\n") + 1; lines != 2 {
		t.Fatalf("lines=%d body=%s", lines, body)
	}
	info, err := os.Stat(out)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("info=%v err=%v", info, err)
	}
}

func TestTraceCLIRejectsMutationAndUnsafeOutput(t *testing.T) {
	path, runID := traceFixture(t)
	if err := run(context.Background(), []string{"--db", path, "--op", "delete", "--run", runID}, &bytes.Buffer{}); err == nil {
		t.Fatal("mutation op accepted")
	}
	out := filepath.Join(t.TempDir(), "existing.jsonl")
	if os.WriteFile(out, []byte("keep"), 0o600) != nil {
		t.Fatal("write output fixture")
	}
	if err := run(context.Background(), []string{"--db", path, "--op", "export", "--run", runID, "--out", out}, &bytes.Buffer{}); err == nil {
		t.Fatal("existing export overwritten")
	}
}

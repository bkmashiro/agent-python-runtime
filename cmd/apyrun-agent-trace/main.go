package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/bkmashiro/agent-python-runtime/agenttrace"
)

const outputVersion = "agent-trace-query/v1"

type runsOutput struct {
	Version string                  `json:"version"`
	Runs    []agenttrace.RunSummary `json:"runs"`
}

type eventsOutput struct {
	Version string             `json:"version"`
	Events  []agenttrace.Event `json:"events"`
}

type runStats struct {
	Version         string            `json:"version"`
	AgentRunID      string            `json:"agent_run_id"`
	EventCount      uint64            `json:"event_count"`
	FirstObservedAt time.Time         `json:"first_observed_at"`
	LastObservedAt  time.Time         `json:"last_observed_at"`
	DurationNS      int64             `json:"duration_ns"`
	PayloadBytes    uint64            `json:"payload_bytes"`
	EventTypeCounts map[string]uint64 `json:"event_type_counts"`
	IntegrityDigest string            `json:"integrity_digest"`
}

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout io.Writer) (runErr error) {
	flags := flag.NewFlagSet("apyrun-agent-trace", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dbPath := flags.String("db", "", "absolute SQLite trace path")
	op := flags.String("op", "", "runs, events, stats, export, or fork")
	runID := flags.String("run", "", "agent run identifier")
	after := flags.Uint64("after", 0, "first sequence is after this value")
	limit := flags.Uint("limit", 100, "bounded result limit")
	outPath := flags.String("out", "", "exclusive JSONL output path")
	sequence := flags.Uint64("sequence", 0, "checkpoint sequence")
	newRunID := flags.String("new-run", "", "new fork run identifier")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *dbPath == "" || *op == "" || *limit == 0 || *limit > 1000 {
		return errors.New("invalid arguments")
	}
	store, err := agenttrace.OpenSQLiteStoreReadOnly(*dbPath)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, store.Close()) }()
	encode := func(value any) error {
		encoder := json.NewEncoder(stdout)
		encoder.SetEscapeHTML(false)
		return encoder.Encode(value)
	}
	switch *op {
	case "runs":
		if *runID != "" || *after != 0 || *outPath != "" || *sequence != 0 || *newRunID != "" {
			return errors.New("invalid runs arguments")
		}
		runs, err := store.Runs(ctx, uint32(*limit))
		if err != nil {
			return err
		}
		return encode(runsOutput{Version: outputVersion, Runs: runs})
	case "events":
		if *runID == "" || *outPath != "" || *sequence != 0 || *newRunID != "" {
			return errors.New("invalid events arguments")
		}
		events, err := store.Events(ctx, *runID, *after, uint32(*limit))
		if err != nil {
			return err
		}
		if events == nil {
			events = make([]agenttrace.Event, 0)
		}
		return encode(eventsOutput{Version: outputVersion, Events: events})
	case "stats":
		if *runID == "" || *after != 0 || *outPath != "" || *sequence != 0 || *newRunID != "" {
			return errors.New("invalid stats arguments")
		}
		playback, err := store.LoadPlayback(ctx, *runID)
		if err != nil {
			return err
		}
		stats, err := summarize(playback)
		if err != nil {
			return err
		}
		return encode(stats)
	case "export":
		if *runID == "" || *after != 0 || *sequence != 0 || *newRunID != "" {
			return errors.New("invalid export arguments")
		}
		playback, err := store.LoadPlayback(ctx, *runID)
		if err != nil {
			return err
		}
		return exportJSONL(playback.Events, *outPath, stdout)
	case "fork":
		if *runID == "" || *sequence == 0 || *newRunID == "" || *after != 0 || *outPath != "" {
			return errors.New("invalid fork arguments")
		}
		playback, err := store.LoadPlayback(ctx, *runID)
		if err != nil {
			return err
		}
		plan, err := playback.ForkAt(*sequence, *newRunID)
		if err != nil {
			return err
		}
		return encode(plan)
	default:
		return errors.New("unsupported operation")
	}
}

func summarize(playback agenttrace.Playback) (runStats, error) {
	digest, err := playback.IntegrityDigest()
	if err != nil {
		return runStats{}, err
	}
	stats := runStats{
		Version: outputVersion, AgentRunID: playback.AgentRunID, EventCount: uint64(len(playback.Events)),
		FirstObservedAt: playback.Events[0].ObservedAt, LastObservedAt: playback.Events[len(playback.Events)-1].ObservedAt,
		EventTypeCounts: make(map[string]uint64), IntegrityDigest: digest,
	}
	stats.DurationNS = stats.LastObservedAt.Sub(stats.FirstObservedAt).Nanoseconds()
	for _, event := range playback.Events {
		stats.EventTypeCounts[string(event.EventType)]++
		stats.PayloadBytes += uint64(len(event.Payload))
	}
	return stats, nil
}

func exportJSONL(events []agenttrace.Event, path string, stdout io.Writer) (returnErr error) {
	writer := stdout
	var file *os.File
	if path != "" {
		opened, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		file = opened
		writer = file
		defer func() { returnErr = errors.Join(returnErr, file.Close()) }()
	}
	buffered := bufio.NewWriter(writer)
	encoder := json.NewEncoder(buffered)
	encoder.SetEscapeHTML(false)
	for _, event := range events {
		if err := encoder.Encode(event); err != nil {
			return err
		}
	}
	return buffered.Flush()
}

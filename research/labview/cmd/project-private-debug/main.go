package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bkmashiro/agent-python-runtime/research/labstore"
	"github.com/bkmashiro/agent-python-runtime/research/trajectory"
)

const schema = "pysolate.lab-provider-debug.v1"

type debugEvent struct {
	Ordinal        uint64          `json:"ordinal"`
	EventID        string          `json:"event_id"`
	Type           string          `json:"type"`
	ActorID        string          `json:"actor_id"`
	ParentEventIDs []string        `json:"parent_event_ids,omitempty"`
	Payload        json.RawMessage `json:"payload"`
	Body           json.RawMessage `json:"body,omitempty"`
}

type debugExport struct {
	SchemaVersion string          `json:"schema_version"`
	TraceID       string          `json:"trace_id"`
	HeaderSHA256  string          `json:"header_sha256"`
	SealSHA256    string          `json:"seal_sha256"`
	Events        []debugEvent    `json:"events"`
	HarnessResult json.RawMessage `json:"harness_result"`
}

func main() {
	tracePath := flag.String("trace", "", "private experiment-full JSON")
	storePath := flag.String("store", "", "private labstore root")
	harnessPath := flag.String("harness-result", "", "real provider harness result")
	outputPath := flag.String("output", "", "debug output JSON")
	flag.Parse()
	if err := run(*tracePath, *storePath, *harnessPath, *outputPath); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(tracePath, storePath, harnessPath, outputPath string) error {
	if tracePath == "" || storePath == "" || harnessPath == "" || outputPath == "" {
		return errors.New("trace, store, harness-result, and output are required")
	}
	traceRaw, err := os.ReadFile(tracePath)
	if err != nil {
		return err
	}
	var export trajectory.Export
	if err := json.Unmarshal(traceRaw, &export); err != nil || export.Profile != trajectory.ProfileExperimentFull || export.Privacy != labstore.PrivacyPrivate {
		return errors.New("invalid private trajectory export")
	}
	store, err := labstore.Open(storePath, labstore.Options{ReadOnly: true})
	if err != nil {
		return err
	}
	defer store.Close()
	debug := debugExport{SchemaVersion: schema, TraceID: export.TraceID, HeaderSHA256: export.HeaderSHA256, SealSHA256: export.SealSHA256, Events: make([]debugEvent, 0, len(export.Events))}
	for _, event := range export.Events {
		item := debugEvent{Ordinal: event.Ordinal, EventID: event.EventID, Type: string(event.Type), ActorID: event.ActorID, ParentEventIDs: event.ParentEventIDs, Payload: append(json.RawMessage(nil), event.Payload...)}
		if event.Body != nil {
			object, err := store.Get(*event.Body)
			if err != nil {
				return err
			}
			if !json.Valid(object.Body) {
				return errors.New("debug body is not JSON")
			}
			item.Body = append(json.RawMessage(nil), object.Body...)
		}
		debug.Events = append(debug.Events, item)
	}
	debug.HarnessResult, err = os.ReadFile(harnessPath)
	if err != nil || !json.Valid(debug.HarnessResult) {
		return errors.New("invalid harness result")
	}
	encoded, err := json.MarshalIndent(debug, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(outputPath, encoded, 0o644)
}

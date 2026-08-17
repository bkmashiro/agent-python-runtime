package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bkmashiro/agent-python-runtime/research/labview"
)

func main() {
	corpusPath := flag.String("corpus", "", "public development corpus")
	reportPath := flag.String("report", "", "composable direct report")
	capturePath := flag.String("capture", "", "experiment-full body capture")
	outputPath := flag.String("output", "", "task snapshot output")
	identityPath := flag.String("identity-output", "", "generated TypeScript identity output")
	recordingRoot := flag.String("recording-root", "", "optional persistent experiment-full recording root")
	flag.Parse()
	if *corpusPath == "" || *reportPath == "" || *capturePath == "" || *outputPath == "" || *identityPath == "" {
		fmt.Fprintln(os.Stderr, "project-task: corpus, report, capture, output and identity-output are required")
		os.Exit(2)
	}
	corpus, err := os.ReadFile(*corpusPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	report, err := os.ReadFile(*reportPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	capture, err := os.ReadFile(*capturePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	snapshot, err := labview.BuildTaskSnapshot(labview.TaskInputs{Corpus: corpus, Report: report, Capture: capture})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var corpusHeader struct {
		SourceCommit string `json:"source_commit"`
	}
	if err = json.Unmarshal(corpus, &corpusHeader); err != nil {
		fmt.Fprintln(os.Stderr, "project-task: invalid corpus header")
		os.Exit(1)
	}
	recordingPath := *recordingRoot
	cleanup := func() {}
	if recordingPath == "" {
		recordingPath, err = os.MkdirTemp("", "pysolate-task-recording-*")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		cleanup = func() { _ = os.RemoveAll(recordingPath) }
	} else {
		recordingPath, err = filepath.Abs(recordingPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	defer cleanup()
	if _, err = labview.RecordTaskExperiment(filepath.Clean(recordingPath), snapshot, corpusHeader.SourceCommit); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	encoded, err := labview.TaskSnapshotJSON(snapshot)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	encoded = append(encoded, '\n')
	identity, err := labview.TaskIdentitySource(snapshot)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(*outputPath, encoded, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(*identityPath, identity, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/bkmashiro/agent-python-runtime/research/labview"
)

func main() {
	corpusPath := flag.String("corpus", "", "public development corpus")
	reportPath := flag.String("report", "", "composable direct report")
	outputPath := flag.String("output", "", "task snapshot output")
	identityPath := flag.String("identity-output", "", "generated TypeScript identity output")
	flag.Parse()
	if *corpusPath == "" || *reportPath == "" || *outputPath == "" || *identityPath == "" {
		fmt.Fprintln(os.Stderr, "project-task: corpus, report, output and identity-output are required")
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
	snapshot, err := labview.BuildTaskSnapshot(labview.TaskInputs{Corpus: corpus, Report: report})
	if err != nil {
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

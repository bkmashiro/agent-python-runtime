package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bkmashiro/agent-python-runtime/research/effectgraph"
)

func main() {
	output := flag.String("output", "docs/evidence/effect-aware-differential-oracle.json", "oracle report output")
	flag.Parse()
	report, err := effectgraph.RunDifferentialOracle(effectgraph.DefaultDifferentialCases())
	if err != nil {
		fatal(err)
	}
	encoded, err := effectgraph.EncodeDifferentialReport(report)
	if err != nil {
		fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil {
		fatal(err)
	}
	temporary := *output + ".tmp"
	if err := os.WriteFile(temporary, encoded, 0o644); err != nil {
		fatal(err)
	}
	if err := os.Rename(temporary, *output); err != nil {
		fatal(err)
	}
	fmt.Printf("cases=%d matched=%d\n", report.Cases, report.Matched)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

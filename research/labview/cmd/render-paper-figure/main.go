package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/bkmashiro/agent-python-runtime/research/labview"
)

func main() {
	input := flag.String("input", "", "latest Lab snapshot JSON")
	output := flag.String("output", "", "paper figure SVG")
	flag.Parse()
	if *input == "" || *output == "" {
		fmt.Fprintln(os.Stderr, "input and output paths are required")
		os.Exit(2)
	}
	raw, err := os.ReadFile(*input)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	snapshot, err := labview.DecodeLatestSnapshot(raw)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	svg, err := labview.PaperFigureSVG(snapshot)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(*output, svg, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

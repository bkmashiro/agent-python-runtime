package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	fanout "github.com/bkmashiro/agent-python-runtime/research/prepareddatasetfanout"
)

func main() {
	input := flag.String("input", "", "fanout report JSON")
	flag.Parse()
	if *input == "" {
		fail(fmt.Errorf("input is required"))
	}
	raw, err := os.ReadFile(*input)
	if err != nil {
		fail(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var report fanout.Report
	if err := decoder.Decode(&report); err != nil {
		fail(err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		fail(fmt.Errorf("trailing JSON"))
	}
	if err := fanout.Validate(report); err != nil {
		fail(err)
	}
	summaries, err := fanout.Summaries(report)
	if err != nil {
		fail(err)
	}
	encoded, _ := json.Marshal(struct {
		SchemaVersion string           `json:"schema_version"`
		Records       int              `json:"records"`
		Summaries     []fanout.Summary `json:"summaries"`
	}{"pysolate.prepared-data-fanout-check.v1", len(report.Records), summaries})
	fmt.Println(string(encoded))
}
func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }

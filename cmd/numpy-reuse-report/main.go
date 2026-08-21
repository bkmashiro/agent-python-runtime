package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/bkmashiro/agent-python-runtime/research/numpyreuse"
)

type controlsFile struct {
	Controls []numpyreuse.AdversarialControl `json:"controls"`
}

func main() {
	darwin := flag.String("darwin", "", "darwin canonical JSONL")
	linux := flag.String("linux", "", "linux canonical JSONL")
	controlsPath := flag.String("controls", "", "body-free adversarial controls JSON")
	output := flag.String("output", "", "sealed body-free report JSON")
	flag.Parse()
	if *darwin == "" || *linux == "" || *controlsPath == "" || *output == "" {
		fail(errors.New("all paths are required"))
	}
	records := []numpyreuse.TrialRecord{}
	for _, path := range []string{*darwin, *linux} {
		raw, err := os.ReadFile(path)
		if err != nil {
			fail(err)
		}
		decoded, err := numpyreuse.DecodeTrialJSONL(raw)
		if err != nil {
			fail(err)
		}
		records = append(records, decoded...)
	}
	var controls controlsFile
	raw, err := os.ReadFile(*controlsPath)
	if err != nil {
		fail(err)
	}
	if err := json.Unmarshal(raw, &controls); err != nil {
		fail(err)
	}
	report, err := numpyreuse.SealCampaignReport(records, controls.Controls)
	if err != nil {
		fail(err)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		fail(err)
	}
	if err := os.WriteFile(*output, append(encoded, '\n'), 0o600); err != nil {
		fail(err)
	}
	fmt.Printf("PASS numpy reuse campaign records=%d cells=%d economics=%d identity=%s\n", len(report.Records), len(report.Cells), len(report.Economics), report.IdentitySHA256)
}

func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }

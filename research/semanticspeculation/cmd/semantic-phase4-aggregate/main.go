package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bkmashiro/agent-python-runtime/research/semanticspeculation"
)

func main() {
	rawRoot := flag.String("raw-root", "", "complete private raw record directory")
	output := flag.String("output", "", "new aggregate report path")
	flag.Parse()
	if flag.NArg() != 0 || *rawRoot == "" || *output == "" {
		fatal("flags required")
	}
	entries, err := os.ReadDir(*rawRoot)
	if err != nil {
		fatal("read raw: %v", err)
	}
	names := []string{}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	records := make([]semanticspeculation.Phase4TrialRecord, 0, len(names))
	for _, name := range names {
		raw, readErr := os.ReadFile(filepath.Join(*rawRoot, name))
		if readErr != nil {
			fatal("read: %v", readErr)
		}
		record, decodeErr := semanticspeculation.DecodePhase4TrialRecord(raw)
		if decodeErr != nil {
			fatal("decode %s: %v", name, decodeErr)
		}
		records = append(records, record)
	}
	report, err := semanticspeculation.AggregatePhase4Campaign(records)
	if err != nil {
		fatal("aggregate: %v", err)
	}
	encoded, _ := json.Marshal(report)
	encoded = append(encoded, '\n')
	file, err := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		fatal("create: %v", err)
	}
	if _, err = file.Write(encoded); err != nil {
		fatal("write: %v", err)
	}
	if err = file.Close(); err != nil {
		fatal("close: %v", err)
	}
	fmt.Printf("mechanism=%t economics=%t\n", report.MechanismPassed, report.EconomicsPassed)
}
func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "semantic-phase4-aggregate: "+format+"\n", args...)
	os.Exit(1)
}

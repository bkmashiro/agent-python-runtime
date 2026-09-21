package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/scheduling"
)

func TestBuiltInWorkloadCoversCommonShapes(t *testing.T) {
	tasks := builtInWorkload()
	if len(tasks) != 12 {
		t.Fatalf("task count=%d", len(tasks))
	}
	seenIO, seenWait, seenComputeAfterIO := false, false, false
	for _, task := range tasks {
		for index, phase := range task.Phases {
			switch phase.Kind {
			case scheduling.ExternalIO:
				seenIO = true
				if index+1 < len(task.Phases) && task.Phases[index+1].Kind == scheduling.CPU {
					seenComputeAfterIO = true
				}
			case scheduling.DurableWait:
				seenWait = true
			}
		}
	}
	if !seenIO || !seenWait || !seenComputeAfterIO {
		t.Fatalf("workload lacks expected shapes: io=%v wait=%v suffix=%v", seenIO, seenWait, seenComputeAfterIO)
	}
}

func TestParsersRejectUnknownValuesAndDedupePolicies(t *testing.T) {
	policies, err := parsePolicies("fifo,ready_first,fifo")
	if err != nil || len(policies) != 2 || policies[0] != scheduling.FIFO || policies[1] != scheduling.ReadyFirst {
		t.Fatalf("policies=%v err=%v", policies, err)
	}
	if _, err := parsePolicies("unknown"); err == nil {
		t.Fatal("unknown policy accepted")
	}
	if modes, err := parseLiveModes("both"); err != nil || len(modes) != 2 || modes[0] || !modes[1] {
		t.Fatalf("modes=%v err=%v", modes, err)
	}
	if _, err := parseLiveModes("sometimes"); err == nil {
		t.Fatal("invalid live mode accepted")
	}
}

func TestLoadWorkloadIsStrict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workload.json")
	if err := os.WriteFile(path, []byte(`[{"id":"x","phases":[{"kind":"cpu","duration_ns":1}],"unknown":true}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadWorkload(path); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestCanonicalGridParsersSortAndValidate(t *testing.T) {
	integers, err := parsePositiveInts("4,2,2", "values")
	if err != nil || len(integers) != 2 || integers[0] != 2 || integers[1] != 4 {
		t.Fatalf("integers=%v err=%v", integers, err)
	}
	floats, err := parsePositiveFloats("10,0.1,1", "values")
	if err != nil || len(floats) != 3 || floats[0] != 0.1 || floats[1] != 1 || floats[2] != 10 {
		t.Fatalf("floats=%v err=%v", floats, err)
	}
	for _, value := range []string{"0", "NaN", "+Inf", "bad"} {
		if _, err := parsePositiveFloats(value, "values"); err == nil {
			t.Fatalf("invalid float %q accepted", value)
		}
	}
	if _, err := parseSinglePositiveInt("1,2", "running"); err == nil {
		t.Fatal("mixed scenario accepted a grid")
	}
}

func TestCanonicalWritersProduceMachineReadableRows(t *testing.T) {
	grid := scheduling.CanonicalSweep{
		TaskCounts: []int{2}, IORatios: []float64{1}, RunningLimits: []int{1},
		ResidentMultipliers: []int{2}, ToolLimits: []int{1}, Policies: []scheduling.Policy{scheduling.FIFO},
		CPUBefore: time.Millisecond, CPUAfter: time.Millisecond, ResidentBytes: 8 << 20,
	}
	points, boundaries, err := scheduling.RunCanonicalSweep(grid)
	if err != nil {
		t.Fatal(err)
	}

	var jsonl bytes.Buffer
	if err := writeSweepJSONL(&jsonl, grid, points, boundaries); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&jsonl)
	for index, expected := range []string{"metadata", "point", "boundary"} {
		var row struct {
			Type string `json:"type"`
		}
		if err := decoder.Decode(&row); err != nil {
			t.Fatalf("decode row %d: %v", index, err)
		}
		if row.Type != expected {
			t.Fatalf("row %d type=%q", index, row.Type)
		}
	}

	var csvOutput bytes.Buffer
	if err := writeSweepCSV(&csvOutput, points, boundaries); err != nil {
		t.Fatal(err)
	}
	records, err := csv.NewReader(&csvOutput).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 || records[1][0] != "point" || records[2][0] != "boundary" {
		t.Fatalf("unexpected CSV records: %v", records)
	}
	for index, record := range records[1:] {
		if len(record) != len(records[0]) {
			t.Fatalf("row %d has %d columns, want %d", index+1, len(record), len(records[0]))
		}
	}
}

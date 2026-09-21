package main

import (
	"os"
	"path/filepath"
	"testing"

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

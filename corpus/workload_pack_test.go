package corpus

import (
	"os"
	"strings"
	"testing"
)

func TestAgentWorkloadPackIsBroadAndFrozen(t *testing.T) {
	file, err := os.Open("../examples/corpus/agent-workloads.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	cases, err := DecodeJSONL(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 20 {
		t.Fatalf("workload cases=%d want=20", len(cases))
	}

	seen := make(map[string]bool, len(cases))
	pure, tools := 0, 0
	for _, item := range cases {
		if item.Origin.Dataset != "pysolate/agent-workloads" || item.Origin.Revision != "v1" {
			t.Fatalf("case %q has unstable origin %+v", item.ID, item.Origin)
		}
		if !strings.HasPrefix(item.ID, "agent-workloads/") {
			t.Fatalf("case %q is outside workload namespace", item.ID)
		}
		seen[item.ID] = true
		if len(item.Tools) == 0 {
			pure++
		} else {
			tools++
		}
	}
	if pure < 12 || tools < 5 {
		t.Fatalf("coverage pure=%d tools=%d", pure, tools)
	}
	for _, required := range []string{
		"agent-workloads/json-normalize",
		"agent-workloads/yaml-config",
		"agent-workloads/numpy-statistics",
		"agent-workloads/code-symbol-index",
		"agent-workloads/tool-dependent-chain",
		"agent-workloads/tool-error-handled",
		"agent-workloads/mcp-like-report",
	} {
		if !seen[required] {
			t.Fatalf("missing representative workload %q", required)
		}
	}
}

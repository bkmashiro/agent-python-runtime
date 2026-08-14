package effectgraph_test

import (
	"encoding/json"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/effectgraph"
)

func TestDefaultDifferentialOracleMatchesEveryAdversarialCase(t *testing.T) {
	report, err := effectgraph.RunDifferentialOracle(effectgraph.DefaultDifferentialCases())
	if err != nil {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	if report.Cases != 17 || report.Matched != report.Cases || len(report.Results) != int(report.Cases) {
		t.Fatalf("report=%+v", report)
	}
	encoded, err := effectgraph.EncodeDifferentialReport(report)
	if err != nil {
		t.Fatal(err)
	}
	var decoded effectgraph.DifferentialReport
	if err := json.Unmarshal(encoded, &decoded); err != nil || decoded.Results[0].ID == "" {
		t.Fatalf("decode=%+v err=%v", decoded, err)
	}
}

package main

import (
	"strings"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/workflowbench"
)

func TestComputeMetricsKeepsRegressionsSigned(t *testing.T) {
	projection := publicProjection{}
	pairs := map[int]map[workflowbench.CampaignTreatment]projectionRun{
		0: {
			workflowbench.CampaignBaseline:  {Treatment: workflowbench.CampaignBaseline, PhysicalExecutions: 17, WallMS: 10, ProcessCPUMS: 10},
			workflowbench.CampaignQualified: {Treatment: workflowbench.CampaignQualified, PhysicalExecutions: 19, WallMS: 12, ProcessCPUMS: 12},
		},
	}
	if err := computeMetrics(&projection, pairs, 1); err != nil {
		t.Fatal(err)
	}
	if projection.Paired.PhysicalReduction.Median != -2 {
		t.Fatalf("physical reduction=%v", projection.Paired.PhysicalReduction.Median)
	}
}

func TestMedianRangeAndSVGAreDeterministic(t *testing.T) {
	metric := summarize([]float64{19, 18, 20, 17, 21})
	if metric.Median != 19 || metric.Min != 17 || metric.Max != 21 {
		t.Fatalf("metric=%+v", metric)
	}
	projection := publicProjection{
		Source: projectionSource{Repetitions: 2},
		Runs: []projectionRun{
			{Repetition: 0, Treatment: "baseline", PhysicalExecutions: 19},
			{Repetition: 0, Treatment: "qualified", PhysicalExecutions: 17},
			{Repetition: 1, Treatment: "qualified", PhysicalExecutions: 17},
			{Repetition: 1, Treatment: "baseline", PhysicalExecutions: 19},
		},
		Programs:          []projectionProgram{{ID: "P20", Disposition: "cancelled"}},
		WalkthroughEvents: []projectionEvent{{Sequence: 1, ProgramID: "P20", Type: "logical.released", AtNS: 1}, {Sequence: 2, ProgramID: "P20", Type: "logical.terminal", AtNS: 10}},
	}
	first := renderSVG(projection)
	second := renderSVG(projection)
	if first != second || !strings.Contains(first, "Physical executions") || strings.Contains(first, "e+") {
		t.Fatalf("unexpected SVG: %s", first)
	}
	flow := renderFlowSVG(projection)
	if !strings.Contains(flow, ">P20<") || !strings.Contains(flow, "linear measured wall time") || strings.Contains(flow, "<title") {
		t.Fatalf("unexpected flow SVG: %s", flow)
	}
}

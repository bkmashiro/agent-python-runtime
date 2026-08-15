package main

import (
	"strings"
	"testing"
)

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
	}
	first := renderSVG(projection)
	second := renderSVG(projection)
	if first != second || !strings.Contains(first, "Physical executions") || strings.Contains(first, "e+") {
		t.Fatalf("unexpected SVG: %s", first)
	}
}

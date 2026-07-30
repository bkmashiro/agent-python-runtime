package main

import (
	"strings"
	"testing"
)

func TestResolveBenchmarkExecuteFixture(t *testing.T) {
	basic, err := resolveBenchmarkExecuteFixture(benchmarkFixtureBasic, 1000, "base")
	if err != nil {
		t.Fatal(err)
	}
	if basic.Name != benchmarkFixtureBasic || basic.Prepare != "prepared = 41" || basic.Inputs["integer_work"] != 1000 {
		t.Fatalf("unexpected basic fixture: %#v", basic)
	}

	numpy, err := resolveBenchmarkExecuteFixture(benchmarkFixtureNumPyImport, 1000, "numpy-core")
	if err != nil {
		t.Fatal(err)
	}
	if numpy.Name != benchmarkFixtureNumPyImport || !strings.Contains(numpy.Prepare, "import numpy as np") || !strings.Contains(numpy.Code, "np.arange") {
		t.Fatalf("unexpected numpy fixture: %#v", numpy)
	}
	ready, err := resolveBenchmarkExecuteFixture(benchmarkFixtureNumPyReady, 1000, "numpy-core")
	if err != nil {
		t.Fatal(err)
	}
	if ready.Prepare != "" || !strings.Contains(ready.Code, "np.arange") {
		t.Fatalf("unexpected NumPy-ready fixture: %#v", ready)
	}
}

func TestResolveBenchmarkExecuteFixtureRejectsProfileAndNameDrift(t *testing.T) {
	if _, err := resolveBenchmarkExecuteFixture(benchmarkFixtureNumPyImport, 1000, "base"); err == nil {
		t.Fatal("numpy fixture accepted a base artifact")
	}
	if _, err := resolveBenchmarkExecuteFixture(benchmarkFixtureNumPyReady, 1000, "base"); err == nil {
		t.Fatal("NumPy-ready fixture accepted a base artifact")
	}
	if _, err := resolveBenchmarkExecuteFixture("unknown", 1000, "numpy-core"); err == nil {
		t.Fatal("unknown fixture accepted")
	}
}

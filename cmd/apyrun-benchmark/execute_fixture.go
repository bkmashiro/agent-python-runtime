package main

import "fmt"

const (
	benchmarkFixtureBasic       = "basic"
	benchmarkFixtureNumPyImport = "numpy-import"
	benchmarkFixtureNumPyReady  = "numpy-ready"
)

type benchmarkExecuteFixture struct {
	Name    string
	Prepare string
	Code    string
	Inputs  map[string]any
}

func resolveBenchmarkExecuteFixture(name string, integerWork int, artifactProfile string) (benchmarkExecuteFixture, error) {
	switch name {
	case "", benchmarkFixtureBasic:
		return benchmarkExecuteFixture{
			Name: benchmarkFixtureBasic, Prepare: "prepared = 41",
			Code:   `result = {"prepared": prepared, "sum": sum(range(inputs["integer_work"]))}`,
			Inputs: map[string]any{"integer_work": integerWork},
		}, nil
	case benchmarkFixtureNumPyImport:
		if artifactProfile != "numpy-core" {
			return benchmarkExecuteFixture{}, fmt.Errorf("numpy-import fixture requires numpy-core artifact, got %q", artifactProfile)
		}
		return benchmarkExecuteFixture{
			Name: benchmarkFixtureNumPyImport, Prepare: "import numpy as np\nprepared = 41",
			Code:   `result = {"prepared": prepared, "numpy_version": np.__version__, "sum": int(np.arange(inputs["integer_work"]).sum())}`,
			Inputs: map[string]any{"integer_work": integerWork},
		}, nil
	case benchmarkFixtureNumPyReady:
		if artifactProfile != "numpy-core" {
			return benchmarkExecuteFixture{}, fmt.Errorf("numpy-ready fixture requires numpy-core artifact, got %q", artifactProfile)
		}
		return benchmarkExecuteFixture{
			Name:   benchmarkFixtureNumPyReady,
			Code:   `result = {"prepared": prepared, "numpy_version": np.__version__, "sum": int(np.arange(inputs["integer_work"]).sum())}`,
			Inputs: map[string]any{"integer_work": integerWork},
		}, nil
	default:
		return benchmarkExecuteFixture{}, fmt.Errorf("benchmark fixture must be %q, %q, or %q", benchmarkFixtureBasic, benchmarkFixtureNumPyImport, benchmarkFixtureNumPyReady)
	}
}

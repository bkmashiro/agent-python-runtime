package main

import (
	"math"
	"testing"
	"time"
)

func TestValidateCOWPressureOptionsRequiresBoundedLinuxCOW(t *testing.T) {
	valid := benchmarkOptions{
		Kind: "cow-pressure", Class: "production-safe", Strategy: "cow-ready-single-use",
		ArtifactPath: "guest.wasm", ManifestPath: "manifest.json", OutputPath: "evidence.json",
		MemoryBudgetBytes: 32 * 1024 * 1024 * 1024, MemoryReserveBytes: 8 * 1024 * 1024 * 1024,
		MaxPressureSlots: 4096, ConsumerCount: 16, PressureDuration: 30 * time.Second,
	}
	if err := validateCOWPressureOptions(valid, "linux"); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*benchmarkOptions){
		"wrong strategy":     func(value *benchmarkOptions) { value.Strategy = "fresh" },
		"missing budget":     func(value *benchmarkOptions) { value.MemoryBudgetBytes = 0 },
		"missing reserve":    func(value *benchmarkOptions) { value.MemoryReserveBytes = 0 },
		"nonmultiple slots":  func(value *benchmarkOptions) { value.MaxPressureSlots = 5 },
		"too many consumers": func(value *benchmarkOptions) { value.ConsumerCount = 257 },
		"short duration":     func(value *benchmarkOptions) { value.PressureDuration = time.Second },
		"overflow total": func(value *benchmarkOptions) {
			value.MemoryBudgetBytes = math.MaxUint64
			value.MemoryReserveBytes = 1024 * 1024 * 1024
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid
			mutate(&candidate)
			if err := validateCOWPressureOptions(candidate, "linux"); err == nil {
				t.Fatal("invalid cow-pressure options accepted")
			}
		})
	}
	if err := validateCOWPressureOptions(valid, "darwin"); err == nil {
		t.Fatal("non-Linux cow-pressure accepted")
	}
}

func TestPressurePercentileUsesNearestRank(t *testing.T) {
	values := []uint64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	if pressurePercentile(values, 50) != 5 || pressurePercentile(values, 95) != 10 || pressurePercentile(values, 99) != 10 {
		t.Fatal("pressure percentile drifted")
	}
	if pressurePercentile(nil, 99) != 0 {
		t.Fatal("empty pressure percentile is not zero")
	}
}

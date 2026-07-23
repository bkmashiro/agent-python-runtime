package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestBoundedErrorPreservesShortError(t *testing.T) {
	if got := boundedError(errors.New("short")); got != "short" {
		t.Fatalf("boundedError=%q, want short", got)
	}
}

func TestBoundedErrorTruncatesLongError(t *testing.T) {
	got := boundedError(errors.New(strings.Repeat("x", 9000)))
	if len(got) != 8192 {
		t.Fatalf("len(boundedError)=%d, want 8192", len(got))
	}
}

func TestBoundedTextPreservesDiagnosticTraceback(t *testing.T) {
	traceback := strings.Repeat("traceback line\n", 400)
	if got := boundedText(traceback); got != traceback {
		t.Fatalf("boundedText truncated %d-byte diagnostic", len(traceback))
	}
}

func TestFeatureProfileReportSchema(t *testing.T) {
	exit := uint64(0)
	encoded, err := json.Marshal(report{
		SchemaVersion:       6,
		FeatureProfile:      "random",
		Outcome:             "freshness_succeeded",
		CompileDurationNS:   11,
		RuntimeDurationNS:   22,
		HostHeapAllocBytes:  33,
		HostHeapSysBytes:    44,
		HostTotalAllocBytes: 55,
		NumericCalled:       true,
		NumericExit:         &exit,
		NumericValidated:    true,
		RandomCalled:        true,
		RandomExit:          &exit,
		RandomValidated:     true,
		EntropySource:       "host_crypto_rand",
		EntropyCalled:       true,
		EntropyExit:         &exit,
		EntropyValidated:    true,
		FreshInstances: []instanceEvidence{
			{Index: 1, FullValidation: true, MemoryBytes: 134217728, FreshnessExit: &exit, EntropyWord: 1},
			{Index: 2, FullValidation: false, MemoryBytes: 134217728, FreshnessExit: &exit, EntropyWord: 2},
		},
		FreshInstancesValidated: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["schema_version"] != float64(6) || decoded["feature_profile"] != "random" || decoded["outcome"] != "freshness_succeeded" {
		t.Fatalf("unexpected schema identity: %s", encoded)
	}
	if decoded["numeric_called"] != true || decoded["numeric_exit"] != float64(0) || decoded["numeric_validated"] != true {
		t.Fatalf("unexpected numeric evidence: %s", encoded)
	}
	if decoded["random_called"] != true || decoded["random_exit"] != float64(0) || decoded["random_validated"] != true {
		t.Fatalf("unexpected random evidence: %s", encoded)
	}
	if decoded["entropy_source"] != "host_crypto_rand" || decoded["entropy_called"] != true || decoded["entropy_exit"] != float64(0) || decoded["entropy_validated"] != true {
		t.Fatalf("unexpected entropy evidence: %s", encoded)
	}
	if decoded["compile_duration_ns"] != float64(11) || decoded["runtime_duration_ns"] != float64(22) || decoded["host_heap_alloc_bytes"] != float64(33) || decoded["fresh_instances_validated"] != true {
		t.Fatalf("unexpected resource/freshness evidence: %s", encoded)
	}
	instances, ok := decoded["fresh_instances"].([]any)
	if !ok || len(instances) != 2 {
		t.Fatalf("unexpected fresh instances: %s", encoded)
	}
}

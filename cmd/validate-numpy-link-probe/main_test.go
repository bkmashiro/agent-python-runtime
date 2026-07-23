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
		SchemaVersion:    4,
		FeatureProfile:   "random",
		Outcome:          "random_succeeded",
		NumericCalled:    true,
		NumericExit:      &exit,
		NumericValidated: true,
		RandomCalled:     true,
		RandomExit:       &exit,
		RandomValidated:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["schema_version"] != float64(4) || decoded["feature_profile"] != "random" || decoded["outcome"] != "random_succeeded" {
		t.Fatalf("unexpected schema identity: %s", encoded)
	}
	if decoded["numeric_called"] != true || decoded["numeric_exit"] != float64(0) || decoded["numeric_validated"] != true {
		t.Fatalf("unexpected numeric evidence: %s", encoded)
	}
	if decoded["random_called"] != true || decoded["random_exit"] != float64(0) || decoded["random_validated"] != true {
		t.Fatalf("unexpected random evidence: %s", encoded)
	}
}

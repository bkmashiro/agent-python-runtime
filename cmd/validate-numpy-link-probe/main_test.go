package main

import (
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

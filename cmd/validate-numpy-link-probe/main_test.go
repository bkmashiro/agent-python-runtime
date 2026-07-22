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
	got := boundedError(errors.New(strings.Repeat("x", 1500)))
	if len(got) != 1000 {
		t.Fatalf("len(boundedError)=%d, want 1000", len(got))
	}
}

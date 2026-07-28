// Package adaptertest provides reusable black-box conformance gates for
// deterministic Host capability adapters. It performs no provider operation.
package adaptertest

import (
	"bytes"
	"encoding/json"
	"testing"
)

type ReplayCase struct {
	First                  func() ([]byte, error)
	Same                   func() ([]byte, error)
	Changed                func() ([]byte, error)
	MutationCount          func() uint64
	ExpectedFirstErrorCode string
	SecretMarkers          [][]byte
}

type responseEnvelope struct {
	Error *struct {
		Code string `json:"code"`
	} `json:"error"`
}

// AssertReplayConformance verifies byte-stable same-request replay, rejection
// of a changed request using the same call ID, and absence of duplicate
// provider mutation or configured secret markers.
func AssertReplayConformance(t testing.TB, test ReplayCase) {
	t.Helper()
	if test.First == nil || test.Same == nil || test.Changed == nil || test.MutationCount == nil {
		t.Fatal("invalid adapter replay conformance case")
	}
	before := test.MutationCount()
	first, err := test.First()
	if err != nil {
		t.Fatalf("first call failed: %v", err)
	}
	assertErrorCode(t, first, test.ExpectedFirstErrorCode)
	afterFirst := test.MutationCount()
	if afterFirst <= before {
		t.Fatalf("first call did not produce the expected provider mutation: before=%d after=%d", before, afterFirst)
	}
	same, err := test.Same()
	if err != nil {
		t.Fatalf("same replay failed: %v", err)
	}
	if !bytes.Equal(first, same) {
		t.Fatalf("same replay drifted: first=%s same=%s", first, same)
	}
	if got := test.MutationCount(); got != afterFirst {
		t.Fatalf("same replay duplicated provider mutation: first=%d replay=%d", afterFirst, got)
	}
	changed, err := test.Changed()
	if err != nil {
		t.Fatalf("changed replay transport failed: %v", err)
	}
	assertErrorCode(t, changed, "duplicate_call_id")
	if got := test.MutationCount(); got != afterFirst {
		t.Fatalf("changed replay mutated provider: first=%d changed=%d", afterFirst, got)
	}
	for _, payload := range [][]byte{first, same, changed} {
		for _, marker := range test.SecretMarkers {
			if len(marker) != 0 && bytes.Contains(payload, marker) {
				t.Fatalf("adapter response leaked configured secret marker")
			}
		}
	}
}

func assertErrorCode(t testing.TB, payload []byte, expected string) {
	t.Helper()
	var response responseEnvelope
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatalf("decode adapter response: %v", err)
	}
	actual := ""
	if response.Error != nil {
		actual = response.Error.Code
	}
	if actual != expected {
		t.Fatalf("adapter response error code=%q want=%q payload=%s", actual, expected, payload)
	}
}

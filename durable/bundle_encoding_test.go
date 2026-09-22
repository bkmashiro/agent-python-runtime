package durable

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

func TestBundleJSONComparisonPreservesValues(t *testing.T) {
	if !sameJSON(json.RawMessage(`{"x":"<&>", "n":9007199254740993}`), json.RawMessage(`{"x":"\u003c\u0026\u003e","n":9007199254740993}`)) {
		t.Fatal("serialization-only change rejected")
	}
	for _, pair := range [][2]string{{`9007199254740992`, `9007199254740993`}, {`1`, `1.0`}, {`"1"`, `1`}, {`{"a":1,"b":2}`, `{"b":2,"a":1}`}} {
		if sameJSON(json.RawMessage(pair[0]), json.RawMessage(pair[1])) {
			t.Fatalf("accepted distinct values %v", pair)
		}
	}
}
func TestBundleWriterBound(t *testing.T) {
	var target bytes.Buffer
	writer := bundleWriter{Writer: &target, remaining: 3}
	if _, err := writer.Write([]byte("ab")); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("cd")); !errors.Is(err, ErrBundleInvalid) {
		t.Fatalf("limit error=%v", err)
	}
	if target.String() != "ab" {
		t.Fatal("wrote beyond limit")
	}
}

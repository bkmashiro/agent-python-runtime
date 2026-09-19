package main

import "testing"

func TestParseDecisionAcceptsBoolAndObject(t *testing.T) {
	got, err := parseDecision("true")
	if err != nil || string(got) != `{"approved":true}` {
		t.Fatalf("bool=%s err=%v", got, err)
	}
	got, err = parseDecision(`{"approved":false}`)
	if err != nil || string(got) != `{"approved":false}` {
		t.Fatalf("object=%s err=%v", got, err)
	}
	if _, err := parseDecision(`{"answer":true}`); err == nil {
		t.Fatal("object without approved unexpectedly accepted")
	}
}

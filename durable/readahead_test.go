package durable

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestStoreReadAheadIsBoundedAndStopsAtPending(t *testing.T) {
	s, _ := openTestStore(t)
	ctx := context.Background()
	if err := s.Create(ctx, storeTestDefinition("window")); err != nil {
		t.Fatal(err)
	}
	for i := uint32(0); i < 71; i++ {
		_, _, err := s.BeginCall(ctx, "window", LoggedCall{Sequence: i, CallID: fmt.Sprintf("call-%d", i), Capability: "read", Arguments: json.RawMessage(`{}`)})
		if err != nil {
			t.Fatal(err)
		}
		if i != 68 {
			if _, err = s.CompleteCall(ctx, "window", i, json.RawMessage(`{"value":7}`)); err != nil {
				t.Fatal(err)
			}
		}
	}
	first, err := s.readCompleted(ctx, "window", 0)
	if err != nil || len(first) != 64 {
		t.Fatalf("first=%d err=%v", len(first), err)
	}
	second, err := s.readCompleted(ctx, "window", 64)
	if err != nil || len(second) != 4 {
		t.Fatalf("second=%d err=%v", len(second), err)
	}
	third, err := s.readCompleted(ctx, "window", 68)
	if err != nil || len(third) != 0 {
		t.Fatalf("pending bypassed: %d %v", len(third), err)
	}
	if _, err = s.CompleteCall(ctx, "window", 68, json.RawMessage(`{"value":8}`)); err != nil {
		t.Fatal(err)
	}
	third, err = s.readCompleted(ctx, "window", 68)
	if err != nil || len(third) != 3 {
		t.Fatalf("new committed outcome invisible: %d %v", len(third), err)
	}
	first[0].Outcome[0] = 'x'
	again, err := s.readCompleted(ctx, "window", 0)
	if err != nil || string(again[0].Outcome) != `{"value":7}` {
		t.Fatal("caller mutated persisted history")
	}
}

func TestStoreReadAheadByteBudget(t *testing.T) {
	s, _ := openTestStore(t)
	ctx := context.Background()
	if err := s.Create(ctx, storeTestDefinition("bytes")); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(strings.Repeat("x", 600<<10))
	for i := uint32(0); i < 3; i++ {
		if _, _, err := s.BeginCall(ctx, "bytes", LoggedCall{Sequence: i, CallID: fmt.Sprint(i), Capability: "read", Arguments: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.CompleteCall(ctx, "bytes", i, body); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.readCompleted(ctx, "bytes", 0)
	if err != nil || len(rows) != 2 {
		t.Fatalf("unbounded window: %d %v", len(rows), err)
	}
}

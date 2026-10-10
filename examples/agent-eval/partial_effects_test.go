package main

import (
	"context"
	"encoding/json"
	"testing"
)

func TestPartialEffectsStateAndReset(t *testing.T) {
	for _, mode := range []string{"docs", "structured", "query"} {
		t.Run(mode, func(t *testing.T) {
			task := partialEffectsTask(mode)
			ep := task.NewEpisode()
			apply, _ := ep.domain("credits_apply")
			if !apply.Writes {
				t.Fatal("write tool labelled readonly")
			}
			ctx := context.Background()
			first, err := apply.Call(ctx, json.RawMessage(`{"accounts":["A","B","C"],"amount_cents":100}`))
			if err != nil {
				t.Fatal(err)
			}
			b, _ := json.Marshal(first)
			if string(b) != `{"items":[{"account":"A","status":"applied"},{"account":"B","status":"not_applied"},{"account":"C","status":"applied"}]}` {
				t.Fatalf("partial response: %s", b)
			}
			answer := json.RawMessage(`{"done":true}`)
			if ok, _ := ep.CheckAnswer(answer); ok {
				t.Fatal("partial completion passed")
			}
			if _, err := apply.Call(ctx, json.RawMessage(`{"accounts":["B"],"amount_cents":100}`)); err != nil {
				t.Fatal(err)
			}
			if ok, err := ep.CheckAnswer(answer); !ok || err != nil {
				t.Fatalf("correct repair rejected: %v", err)
			}
			if _, err := apply.Call(ctx, json.RawMessage(`{"accounts":["A"],"amount_cents":100}`)); err != nil {
				t.Fatal(err)
			}
			if ok, _ := ep.CheckAnswer(answer); ok {
				t.Fatal("duplicate effect passed")
			}
			if ok, _ := task.NewEpisode().CheckAnswer(answer); ok {
				t.Fatal("state leaked to fresh episode")
			}
		})
	}
}

func TestPartialEffectsInvalidArgumentsAreAtomic(t *testing.T) {
	ep := partialEffectsTask("docs").NewEpisode()
	apply, _ := ep.domain("credits_apply")
	for _, raw := range []string{`{"accounts":["A","X"],"amount_cents":100}`, `{"accounts":["A","A"],"amount_cents":100}`, `{"accounts":[],"amount_cents":100}`, `{"accounts":["A"],"amount_cents":-1}`} {
		if _, err := apply.Call(context.Background(), json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	read, _ := ep.domain("credits_balances")
	v, err := read.Call(context.Background(), json.RawMessage(`{}`))
	b, _ := json.Marshal(v)
	if err != nil || string(b) != `{"A":0,"B":0,"C":0}` {
		t.Fatalf("invalid call changed state: %s %v", b, err)
	}
}

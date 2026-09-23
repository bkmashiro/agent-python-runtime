package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestUsageRequiresAllReportedFields(t *testing.T) {
	for _, raw := range []string{`null`, `{}`, `{"prompt_tokens":10}`, `{"prompt_tokens":10,"completion_tokens":5}`} {
		if decodeUsage(json.RawMessage(raw)) != nil {
			t.Fatalf("partial usage accepted: %s", raw)
		}
	}
	if u := decodeUsage(json.RawMessage(`{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}`)); u == nil {
		t.Fatal("reported zero usage should be known")
	}
}
func TestIntegerOracleDoesNotRound(t *testing.T) {
	for _, raw := range []string{`3199.0`, `3.199e3`, `3199`} {
		n, e := intRaw(json.RawMessage(raw))
		if e != nil || n != 3199 {
			t.Fatalf("%s: %d %v", raw, n, e)
		}
	}
	for _, raw := range []string{`3199.5`, `"3199"`, `1e100000000`, `9223372036854775808`} {
		if _, e := intRaw(json.RawMessage(raw)); e == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
func TestWrongCategoryIsWrongAnswerNotProtocolFailure(t *testing.T) {
	task := joinTask().NewEpisode()
	ok, e := task.CheckAnswer(json.RawMessage(`{"category_totals":{"wrong":1,"beta":2,"gamma":3}}`))
	if e != nil || ok {
		t.Fatalf("%v %v", ok, e)
	}
}
func TestTraceSupportsIndependentGradingWithoutReasoning(t *testing.T) {
	task := lookupTask()
	p := &fakeProvider{arm: "direct", task: "lookup"}
	row := runEpisode(context.Background(), p, &requestBudget{limit: 8}, task, 1, 1, "direct", nil, 0, &episodeHost{}, "fake", 4, 0)
	if len(row.Trace) != 3 || len(row.Answer) == 0 || row.TotalNS <= 0 {
		t.Fatalf("incomplete row %+v", row)
	}
	if strings.Contains(p.seen[0][0].Content, "Domain declarations") {
		t.Fatal("direct schemas duplicated in prompt")
	}
}

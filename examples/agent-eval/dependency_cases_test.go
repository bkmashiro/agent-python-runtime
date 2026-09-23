package main

import (
	"context"
	"encoding/json"
	"testing"
)

func TestOpaqueCursorDependsOnEarlierResponse(t *testing.T) {
	ep := cursorSumTask().NewEpisode()
	tool := ep.Domains[0]
	if _, err := tool.Call(context.Background(), json.RawMessage(`{"cursor":"c_Vp92"}`)); err == nil {
		t.Fatal("future cursor accepted before issue")
	}
	if _, err := tool.Call(context.Background(), json.RawMessage(`{"cursor":null}`)); err == nil {
		t.Fatal("null accepted")
	}
	cursor := ""
	pages, count, matches, sum := 0, 0, 0, 0
	for {
		raw, _ := json.Marshal(map[string]string{"cursor": cursor})
		value, err := tool.Call(context.Background(), raw)
		if err != nil {
			t.Fatal(err)
		}
		page := value.(map[string]any)
		pages++
		for _, r := range page["records"].([]map[string]any) {
			count++
			if r["status"] == "open" && r["region"] == "west" {
				matches++
				sum += r["amount_cents"].(int)
			}
		}
		if page["next_cursor"] == nil {
			break
		}
		cursor = page["next_cursor"].(string)
		if pages > 6 {
			t.Fatal("pagination did not terminate")
		}
	}
	if pages != 6 || count != 120 || matches != 20 || sum != 46050 {
		t.Fatalf("pages=%d rows=%d matches=%d sum=%d", pages, count, matches, sum)
	}
	ok, err := ep.CheckAnswer(json.RawMessage(`{"record_count":20,"sum_cents":46050}`))
	if err != nil || !ok {
		t.Fatalf("oracle: %v %v", ok, err)
	}
}

func TestDependentBranchesRequireReferences(t *testing.T) {
	for _, credit := range []bool{false, true} {
		ep := dependentBranchTask(credit).NewEpisode()
		call := func(name, key, value string) (map[string]any, error) {
			d, ok := ep.domain(name)
			if !ok {
				t.Fatal(name)
			}
			raw, _ := json.Marshal(map[string]string{key: value})
			out, err := d.Call(context.Background(), raw)
			if err != nil {
				return nil, err
			}
			return out.(map[string]any), nil
		}
		if _, err := call("policy_get", "policy_code", "policy_T2vM"); err == nil {
			t.Fatal("policy fetched before document")
		}
		alias, kind, refKey, doc, wrong := "north-shop", "payment_due", "invoice_ref", "invoice_get", "credit_get"
		want := int64(5825)
		if credit {
			alias, kind, refKey, doc, wrong = "south-shop", "credit_available", "credit_ref", "credit_get", "invoice_get"
			want = 4375
		}
		account, err := call("account_resolve", "alias", alias)
		if err != nil {
			t.Fatal(err)
		}
		summary, err := call("account_summary", "account_id", account["account_id"].(string))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := call(wrong, "ref", summary[refKey].(string)); err == nil {
			t.Fatal("wrong branch accepted")
		}
		document, err := call(doc, "ref", summary[refKey].(string))
		if err != nil {
			t.Fatal(err)
		}
		policy, err := call("policy_get", "policy_code", document["policy_code"].(string))
		if err != nil {
			t.Fatal(err)
		}
		got := document["amount_cents"].(int64) + policy["adjustment_cents"].(int64)
		if got != want {
			t.Fatalf("got %d want %d", got, want)
		}
		raw, _ := json.Marshal(map[string]any{"kind": kind, "amount_cents": got})
		ok, err := ep.CheckAnswer(raw)
		if err != nil || !ok {
			t.Fatalf("oracle: %v %v", ok, err)
		}
	}
}

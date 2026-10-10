package main

import (
	"context"
	"encoding/json"
	"errors"
)

// A deliberately small information-interface probe, not an external service or
// an uncertain-outcome benchmark. Every accepted effect has a truthful receipt.
const creditContractText = "Each requested account is independently credited by amount_cents. The batch is not atomic. The response has items with account and status: applied means the credit committed, not_applied means no credit occurred for that item. Repeating an applied item adds credit again; there is no idempotency key and no undo. A not_applied item may be retried. credits.balances returns current authoritative balances. Invalid arguments reject the entire request before any effect."
const creditContractJSON = `{"effect":"increment each requested account by amount_cents","atomic":false,"response":{"items":[{"account":"requested account","status":"applied = committed; not_applied = no effect"}]},"idempotent":false,"repeating_applied":"adds credit again","idempotency_key":false,"undo":false,"retry_not_applied":true,"query":"credits.balances returns current authoritative balances","invalid_arguments":"entire request rejected before any effect"}`

type creditItem struct {
	Account string `json:"account"`
	Status  string `json:"status"`
}
type creditReceipt struct {
	Items []creditItem `json:"items"`
}

func partialEffectsTask(mode string) taskCase {
	return taskCase{
		ID:           "partial_" + mode,
		Prompt:       "Credit accounts A, B, and C by exactly 100 cents each. Their starting balances are zero. Complete the actual changes, without extra credits or changes to any other account, then submit {\"done\":true}. You may inspect balances. Tool service state persists across calls, even though Python variables do not.",
		AnswerSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["done"],"properties":{"done":{"type":"boolean"}}}`),
		NewEpisode: func() *taskEpisode {
			balances := map[string]int64{"A": 0, "B": 0, "C": 0}
			blockedOnce := false
			description := "Write tool. Python binding credits.apply(accounts=[...], amount_cents=...). Credits the listed accounts. "
			switch mode {
			case "docs":
				description += creditContractText
			case "structured":
				description += "Contract: " + creditContractJSON
			case "query":
				description += "Full effect, return-value and retry contract is available from credits.contract()."
			default:
				panic("invalid partial effects mode")
			}
			ep := &taskEpisode{}
			ep.Domains = []domainDeclaration{
				{Name: "credits_apply", PythonPath: "credits.apply", Writes: true, Description: description,
					Schema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["accounts","amount_cents"],"properties":{"accounts":{"type":"array","minItems":1,"maxItems":3,"uniqueItems":true,"items":{"type":"string","enum":["A","B","C"]}},"amount_cents":{"type":"integer","minimum":1,"maximum":100}}}`),
					Call: func(ctx context.Context, raw json.RawMessage) (any, error) {
						if err := ctx.Err(); err != nil {
							return nil, err
						}
						accounts, amount, err := creditArgs(raw)
						if err != nil {
							return nil, err
						}
						receipt := creditReceipt{Items: make([]creditItem, 0, len(accounts))}
						for _, a := range accounts {
							status := "applied"
							// Fault belongs to the service fixture, never the prompt or a runtime hint.
							if a == "B" && !blockedOnce {
								blockedOnce = true
								status = "not_applied"
							} else {
								balances[a] += amount
							}
							receipt.Items = append(receipt.Items, creditItem{a, status})
						}
						return receipt, nil
					}},
				{Name: "credits_balances", PythonPath: "credits.balances", Description: "Readonly. Python binding credits.balances(). Returns authoritative current balances in cents keyed by account. No arguments.", Schema: json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{}}`), Call: func(ctx context.Context, raw json.RawMessage) (any, error) {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					if _, err := exactObject(raw); err != nil {
						return nil, err
					}
					return map[string]int64{"A": balances["A"], "B": balances["B"], "C": balances["C"]}, nil
				}},
				// Present in every arm; only upfront disclosure changes.
				{Name: "credits_contract", PythonPath: "credits.contract", Description: "Readonly. Python binding credits.contract(). Returns the full effect, return-value and retry contract of credits.apply. No arguments.", Schema: json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{}}`), Call: func(ctx context.Context, raw json.RawMessage) (any, error) {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					if _, err := exactObject(raw); err != nil {
						return nil, err
					}
					return json.RawMessage(creditContractJSON), nil
				}},
			}
			ep.CheckAnswer = func(raw json.RawMessage) (bool, error) { return gradeCredits(raw, balances) }
			ep.CheckRecordedAnswer = gradeRecordedCredits
			return ep
		},
	}
}

func creditArgs(raw json.RawMessage) ([]string, int64, error) {
	obj, err := exactObject(raw, "accounts", "amount_cents")
	if err != nil {
		return nil, 0, err
	}
	var accounts []string
	if err = json.Unmarshal(obj["accounts"], &accounts); err != nil || len(accounts) < 1 || len(accounts) > 3 {
		return nil, 0, errors.New("accounts must contain 1 to 3 distinct known accounts")
	}
	seen := map[string]bool{}
	for _, a := range accounts {
		if (a != "A" && a != "B" && a != "C") || seen[a] {
			return nil, 0, errors.New("accounts must contain distinct A, B or C")
		}
		seen[a] = true
	}
	amount, err := intField(obj, "amount_cents")
	if err != nil || amount < 1 || amount > 100 {
		return nil, 0, errors.New("amount_cents must be an integer from 1 to 100")
	}
	return accounts, amount, nil
}

func gradeCredits(raw json.RawMessage, balances map[string]int64) (bool, error) {
	obj, err := exactObject(raw, "done")
	if err != nil {
		return false, err
	}
	if string(obj["done"]) != "true" && string(obj["done"]) != "false" {
		return false, errors.New("done must be boolean")
	}
	return string(obj["done"]) == "true" && balances["A"] == 100 && balances["B"] == 100 && balances["C"] == 100, nil
}

// Pure receipt accounting for offline regrading. It neither reruns a fixture
// nor dispatches effects. This only works here because all committed increments
// have confirmed receipts; do not generalize it to unknown remote outcomes.
func recordedCreditBalances(ep *privateEpisodeRecording) (map[string]int64, error) {
	balances := map[string]int64{"A": 0, "B": 0, "C": 0}
	calls := ep.DomainCalls
	if ep.Arm == "code" {
		calls = nil
		for _, ex := range ep.Executions {
			calls = append(calls, ex.ToolCalls...)
		}
	}
	for _, call := range calls {
		if !call.Completed {
			return nil, errors.New("incomplete credit trace")
		}
		if call.Tool != "credits_apply" {
			continue
		}
		raw := json.RawMessage(call.Result)
		var envelope struct {
			Error *string         `json:"error"`
			Value json.RawMessage `json:"value"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return nil, err
		}
		if envelope.Error != nil {
			continue
		}
		if ep.Arm == "code" {
			raw = envelope.Value
		}
		accounts, amount, err := creditArgs(call.Arguments)
		if err != nil {
			return nil, err
		}
		var receipt creditReceipt
		if err = json.Unmarshal(raw, &receipt); err != nil {
			return nil, err
		}
		if len(accounts) != len(receipt.Items) {
			return nil, errors.New("credit receipt length mismatch")
		}
		for i, item := range receipt.Items {
			if item.Account != accounts[i] {
				return nil, errors.New("credit receipt account mismatch")
			}
			switch item.Status {
			case "applied":
				balances[item.Account] += amount
			case "not_applied":
			default:
				return nil, errors.New("unknown credit receipt status")
			}
		}
	}
	return balances, nil
}
func gradeRecordedCredits(raw json.RawMessage, ep *privateEpisodeRecording) (bool, error) {
	balances, err := recordedCreditBalances(ep)
	if err != nil {
		return false, err
	}
	return gradeCredits(raw, balances)
}

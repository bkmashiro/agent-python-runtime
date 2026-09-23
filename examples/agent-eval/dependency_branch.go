package main

import (
	"context"
	"encoding/json"
	"errors"
)

func dependentBranchTask(credit bool) taskCase {
	id, alias, status, kind := "dependent_due", "north-shop", "overdue", "payment_due"
	amount, adjustment := int64(6275), int64(-450)
	if credit {
		id, alias, status, kind = "dependent_credit", "south-shop", "credit", "credit_available"
		amount, adjustment = 4200, 175
	}
	return taskCase{ID: id, Prompt: "Resolve account alias " + alias + ". Read its summary. For status overdue fetch the referenced invoice; for status credit fetch the referenced credit document. Read the policy identified by that document, then add adjustment_cents to amount_cents. Submit kind (payment_due or credit_available) and amount_cents. Obtain all references from previous responses; do not invent them.", AnswerSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["kind","amount_cents"],"properties":{"kind":{"type":"string","enum":["payment_due","credit_available"]},"amount_cents":{"type":"integer"}}}`), NewEpisode: func() *taskEpisode {
		resolved, summaryRead, documentRead := false, false, false
		account, ref, policy := "acct_K7qP", "doc_r4N9", "policy_T2vM"
		resolve := dependencyReadTool("account_resolve", "accounts.resolve", "Resolve an alias. Returns account_id.", "alias", func(v string) (any, error) {
			if v != alias {
				return nil, errors.New("unknown alias")
			}
			resolved = true
			return map[string]any{"account_id": account}, nil
		})
		summary := dependencyReadTool("account_summary", "accounts.summary", "Read an account. Returns status and either invoice_ref or credit_ref.", "account_id", func(v string) (any, error) {
			if !resolved || v != account {
				return nil, errors.New("account reference has not been issued")
			}
			summaryRead = true
			field := "invoice_ref"
			if credit {
				field = "credit_ref"
			}
			return map[string]any{"status": status, field: ref}, nil
		})
		document := func(wantCredit bool) func(string) (any, error) {
			return func(v string) (any, error) {
				if !summaryRead || credit != wantCredit || v != ref {
					return nil, errors.New("document reference not issued for this operation")
				}
				documentRead = true
				return map[string]any{"amount_cents": amount, "policy_code": policy}, nil
			}
		}
		invoice := dependencyReadTool("invoice_get", "invoices.get", "Read the issued invoice reference. Returns amount_cents and policy_code.", "ref", document(false))
		creditTool := dependencyReadTool("credit_get", "credits.get", "Read the issued credit reference. Returns amount_cents and policy_code.", "ref", document(true))
		policyTool := dependencyReadTool("policy_get", "policies.get", "Read the policy from the fetched document. Returns signed adjustment_cents.", "policy_code", func(v string) (any, error) {
			if !documentRead || v != policy {
				return nil, errors.New("policy reference has not been issued")
			}
			return map[string]any{"adjustment_cents": adjustment}, nil
		})
		return &taskEpisode{Domains: []domainDeclaration{resolve, summary, invoice, creditTool, policyTool}, CheckAnswer: func(raw json.RawMessage) (bool, error) {
			obj, err := exactObject(raw, "kind", "amount_cents")
			if err != nil {
				return false, err
			}
			gotKind, err := stringField(obj, "kind")
			if err != nil {
				return false, err
			}
			got, err := intField(obj, "amount_cents")
			return gotKind == kind && got == amount+adjustment, err
		}}
	}}
}

func dependencyReadTool(name, path, description, key string, call func(string) (any, error)) domainDeclaration {
	schema, _ := json.Marshal(map[string]any{"type": "object", "additionalProperties": false, "required": []string{key}, "properties": map[string]any{key: map[string]any{"type": "string"}}})
	return domainDeclaration{Name: name, PythonPath: path, Description: "Readonly. Python binding " + path + "(" + key + "=...) uses keyword arguments. " + description, Schema: schema, Call: func(ctx context.Context, raw json.RawMessage) (any, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		obj, err := exactObject(raw, key)
		if err != nil {
			return nil, err
		}
		value, err := stringField(obj, key)
		if err != nil {
			return nil, err
		}
		return call(value)
	}}
}

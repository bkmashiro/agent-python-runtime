package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

func cursorSumTask() taskCase {
	return taskCase{ID: "cursor_sum", Prompt: "Read the ledger starting with cursor='' and follow every next_cursor until null. Count records whose status is open and region is west, and sum their amount_cents. Submit record_count and sum_cents as integers. Cursors are opaque and must come from responses.", AnswerSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["record_count","sum_cents"],"properties":{"record_count":{"type":"integer"},"sum_cents":{"type":"integer"}}}`), NewEpisode: func() *taskEpisode {
		cursors := []string{"", "c_k7Q4", "c_Vp92", "c_A6mB", "c_Tz38", "c_R0fN"}
		issued := map[string]bool{"": true}
		tool := domainDeclaration{Name: "ledger_cursor_page", PythonPath: "ledger.cursor_page", Description: "Readonly. Python binding ledger.cursor_page(cursor=''). Start with the empty string. Returns records (id, status, region, amount_cents) and next_cursor (opaque string or null). Use only returned cursors.", Schema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["cursor"],"properties":{"cursor":{"type":"string"}}}`), Call: func(ctx context.Context, raw json.RawMessage) (any, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			args, err := exactObject(raw, "cursor")
			if err != nil {
				return nil, err
			}
			var cursor string
			if string(args["cursor"]) == "null" {
				return nil, errors.New("cursor must be a string")
			}
			if err = json.Unmarshal(args["cursor"], &cursor); err != nil {
				return nil, errors.New("cursor must be a string")
			}
			if !issued[cursor] {
				return nil, errors.New("cursor has not been issued")
			}
			page := -1
			for i, c := range cursors {
				if c == cursor {
					page = i
					break
				}
			}
			if page < 0 {
				return nil, errors.New("unknown cursor")
			}
			rows := make([]map[string]any, 0, 20)
			for i := page * 20; i < (page+1)*20; i++ {
				rows = append(rows, map[string]any{"id": fmt.Sprintf("R-%03d", i+1), "status": []string{"open", "closed", "pending", "open"}[i%4], "region": []string{"east", "west", "north"}[i%3], "amount_cents": 101 + 37*i})
			}
			var next any
			if page+1 < len(cursors) {
				next = cursors[page+1]
				issued[cursors[page+1]] = true
			}
			return map[string]any{"records": rows, "next_cursor": next}, nil
		}}
		return &taskEpisode{Domains: []domainDeclaration{tool}, CheckAnswer: func(raw json.RawMessage) (bool, error) {
			obj, err := exactObject(raw, "record_count", "sum_cents")
			if err != nil {
				return false, err
			}
			count, err := intField(obj, "record_count")
			if err != nil {
				return false, err
			}
			sum, err := intField(obj, "sum_cents")
			return count == 20 && sum == 46050, err
		}}
	}}
}

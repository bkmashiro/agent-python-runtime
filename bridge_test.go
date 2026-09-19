package pysolate

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestToolBudgetStopsFurtherDispatch(t *testing.T) {
	calls := 0
	r := &Runner{manifest: Manifest{"call": {Call: func(context.Context, json.RawMessage) (any, error) { calls++; return calls, nil }}}}
	state := newRun(context.Background(), r, false)
	defer state.close()
	request := callRequest{Tool: "call", Args: json.RawMessage(`{}`)}
	var response []byte
	for i := 0; i < maxToolCalls+2; i++ {
		response = r.invoke(state.ctx, request)
	}
	if calls != maxToolCalls || !strings.Contains(string(response), "budget exhausted") {
		t.Fatalf("calls=%d response=%s", calls, response)
	}
}

func TestEarlyReadRequiresHostDeclaration(t *testing.T) {
	calls := 0
	r := &Runner{manifest: Manifest{"write": {Call: func(context.Context, json.RawMessage) (any, error) { calls++; return nil, nil }}}}
	state := newRun(context.Background(), r, true)
	defer state.close()
	if handle := state.prepare(callRequest{Tool: "write", Args: json.RawMessage(`{}`)}); handle != 0 || calls != 0 {
		t.Fatalf("undeclared early read: handle=%d calls=%d", handle, calls)
	}
}

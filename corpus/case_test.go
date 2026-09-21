package corpus

import (
	"strings"
	"testing"
)

func TestDecodeJSONLValidatesCases(t *testing.T) {
	input := strings.NewReader(`{"schema_version":1,"id":"demo/one","source":"result = lookup(key='price')","inputs":{},"tools":[{"name":"lookup","python_path":"lookup","input_schema":{"type":"object"},"allow_early_read":true}],"replay":[{"tool":"lookup","args":{"key":"price"},"value":21}],"expected":21,"origin":{"dataset":"fixture","revision":"abc","case_id":"one"}}
`)
	cases, err := DecodeJSONL(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 1 || cases[0].ID != "demo/one" {
		t.Fatalf("cases=%#v", cases)
	}
}

func TestDecodeJSONLRejectsInvalidContract(t *testing.T) {
	for _, tc := range []struct {
		name string
		line string
		want string
	}{
		{"version", `{"schema_version":2,"id":"x","source":"result=1","inputs":{},"expected":1}`, "schema_version"},
		{"missing source", `{"schema_version":1,"id":"x","inputs":{},"expected":1}`, "source"},
		{"duplicate tool", `{"schema_version":1,"id":"x","source":"result=1","inputs":{},"tools":[{"name":"a","python_path":"a"},{"name":"a","python_path":"b"}],"expected":1,"origin":{"dataset":"fixture","revision":"abc","case_id":"x"}}`, "duplicate tool"},
		{"unknown replay tool", `{"schema_version":1,"id":"x","source":"result=1","inputs":{},"replay":[{"tool":"missing","args":{},"value":1}],"expected":1,"origin":{"dataset":"fixture","revision":"abc","case_id":"x"}}`, "unknown tool"},
		{"response ambiguity", `{"schema_version":1,"id":"x","source":"result=1","inputs":{},"tools":[{"name":"a","python_path":"a"}],"replay":[{"tool":"a","args":{},"value":1,"error":"bad"}],"expected":1,"origin":{"dataset":"fixture","revision":"abc","case_id":"x"}}`, "exactly one"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodeJSONL(strings.NewReader(tc.line + "\n"))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v, want %q", err, tc.want)
			}
		})
	}
}

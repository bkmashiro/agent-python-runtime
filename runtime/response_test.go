package runtime

import (
	"errors"
	"testing"
)

func TestDecodeAndValidateRunResponseEnforcesOutputSchema(t *testing.T) {
	request := RunRequest{RunID: "run", Code: "result = {}", Inputs: []byte(`{}`), OutputSchema: []byte(`{"type":"object","additionalProperties":false,"required":["ok"],"properties":{"ok":{"type":"boolean"}}}`)}
	valid := []byte(`{"status":"ok","result":{"ok":true},"receipts":[],"metrics":{"guest_time_ms":1.5,"capability_calls":0,"result_bytes":11},"error":null}`)
	if _, err := DecodeAndValidateRunResponse(request, valid); err != nil {
		t.Fatal(err)
	}
	for name, payload := range map[string][]byte{
		"schema mismatch":      []byte(`{"status":"ok","result":{"ok":"yes"},"receipts":[],"metrics":{"capability_calls":0,"result_bytes":12},"error":null}`),
		"forged success error": []byte(`{"status":"ok","result":{"ok":true},"receipts":[],"metrics":{"capability_calls":0,"result_bytes":11},"error":{"code":"x","message":"x"}}`),
		"unknown field":        []byte(`{"status":"ok","result":{"ok":true},"receipts":[],"metrics":{"capability_calls":0,"result_bytes":11,"forged":1},"error":null}`),
		"missing metrics":      []byte(`{"status":"ok","result":{"ok":true},"receipts":[],"error":null}`),
		"negative guest time":  []byte(`{"status":"ok","result":{"ok":true},"receipts":[],"metrics":{"guest_time_ms":-1,"capability_calls":0,"result_bytes":11},"error":null}`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeAndValidateRunResponse(request, payload); err == nil {
				t.Fatal("invalid response accepted")
			}
		})
	}
}

func TestDecodeAndValidateRunResponseReturnsPartialMetricsForSchemaMismatch(t *testing.T) {
	request := RunRequest{RunID: "run", Code: "print('missing result')", Inputs: []byte(`{}`), OutputSchema: []byte(`{"type":"object"}`)}
	response, err := DecodeAndValidateRunResponse(request, []byte(`{"status":"ok","result":null,"receipts":[],"metrics":{"capability_calls":4,"result_bytes":4},"error":null}`))
	if !errors.Is(err, ErrRunResultSchemaMismatch) || response.Metrics == nil || response.Metrics.CapabilityCalls != 4 {
		t.Fatalf("response=%+v err=%v", response, err)
	}
}

func TestDecodeAndValidateRunResponseRejectsExternalSchemaReference(t *testing.T) {
	request := RunRequest{RunID: "run", Code: "result = {}", Inputs: []byte(`{}`), OutputSchema: []byte(`{"$ref":"https://example.invalid/schema.json"}`)}
	payload := []byte(`{"status":"ok","result":{},"receipts":[],"metrics":{"capability_calls":0,"result_bytes":2},"error":null}`)
	if _, err := DecodeAndValidateRunResponse(request, payload); err == nil {
		t.Fatal("external output schema reference accepted")
	}
}

func TestDecodeAndValidateRunResponseAcceptsBoundedGuestError(t *testing.T) {
	request := RunRequest{RunID: "run", Code: "raise ValueError()", Inputs: []byte(`{}`)}
	response, err := DecodeAndValidateRunResponse(request, []byte(`{"status":"error","result":null,"receipts":[],"metrics":{"capability_calls":0,"result_bytes":0},"error":{"code":"python_exception","message":"failed"}}`))
	if err != nil || response.Status != RunResponseError {
		t.Fatalf("response=%+v err=%v", response, err)
	}
}

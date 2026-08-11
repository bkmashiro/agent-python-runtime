package runtime

import (
	"bytes"
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

func TestDecodeAndValidateGuestRunResponseRejectsHostEvidenceAndFoldedAliases(t *testing.T) {
	request := RunRequest{RunID: "guest", Code: "result = 1", Inputs: []byte(`{}`)}
	for name, payload := range map[string][]byte{
		"canonical execution ref": []byte(`{"status":"ok","result":1,"receipts":[],"metrics":{"capability_calls":0,"result_bytes":1},"error":null,"execution_ref":{}}`),
		"folded execution ref":    []byte(`{"status":"ok","result":1,"receipts":[],"metrics":{"capability_calls":0,"result_bytes":1},"error":null,"Execution_ref":{}}`),
		"folded status":           []byte(`{"Status":"ok","result":1,"receipts":[],"metrics":{"capability_calls":0,"result_bytes":1},"error":null}`),
		"folded metric":           []byte(`{"status":"ok","result":1,"receipts":[],"metrics":{"Capability_calls":0,"result_bytes":1},"error":null}`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeAndValidateGuestRunResponse(request, payload); err == nil {
				t.Fatal("Guest response alias/Host evidence accepted")
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

func TestRunResponseWorkspaceReceiptIsHostOnlyAndValidated(t *testing.T) {
	request := RunRequest{RunID: "run", Code: "result = 1", Inputs: []byte(`{}`)}
	payload := []byte(`{"status":"ok","result":1,"receipts":[],"metrics":{"capability_calls":0,"result_bytes":1},"error":null,"workspace_receipt":{"schema_version":"pysolate.workspace-disposition.v1","request_sha256":"sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee","policy":"export_on_success","disposition":"exported","initial_workspace_sha256":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","final_workspace_sha256":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","final_tree_sha256":"sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","entry_count":1,"total_bytes":3,"capsule_sha256":"sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"}}`)
	requestSHA, err := RunRequestSHA256(request)
	if err != nil {
		t.Fatal(err)
	}
	payload = bytes.Replace(payload, []byte("sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"), []byte(requestSHA), 1)
	response, err := DecodeAndValidateRunResponse(request, payload)
	if err != nil || response.WorkspaceReceipt == nil || response.WorkspaceReceipt.Disposition != WorkspaceExported {
		t.Fatalf("response=%+v err=%v", response, err)
	}
	if _, err := DecodeAndValidateGuestRunResponse(request, payload); err == nil {
		t.Fatal("Guest-authored workspace receipt was accepted")
	}
	forged := bytes.Replace(payload, []byte(requestSHA), []byte("sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"), 1)
	if _, err := DecodeAndValidateRunResponse(request, forged); err == nil {
		t.Fatal("workspace receipt with mismatched request identity was accepted")
	}
	nullCapsule := bytes.Replace(payload, []byte(`"sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"`), []byte("null"), 1)
	if _, err := DecodeAndValidateRunResponse(request, nullCapsule); err == nil {
		t.Fatal("workspace receipt with explicit null capsule identity was accepted")
	}
	for field, value := range map[string]string{"entry_count": "1", "total_bytes": "3"} {
		nullNumeric := bytes.Replace(payload, []byte(`"`+field+`":`+value), []byte(`"`+field+`":null`), 1)
		if _, err := DecodeAndValidateRunResponse(request, nullNumeric); err == nil {
			t.Fatalf("workspace receipt with explicit null %s was accepted", field)
		}
		missingNumeric := bytes.Replace(payload, []byte(`,"`+field+`":`+value), nil, 1)
		if _, err := DecodeAndValidateRunResponse(request, missingNumeric); err == nil {
			t.Fatalf("workspace receipt missing %s was accepted", field)
		}
	}
	zeroCounts := bytes.Replace(bytes.Replace(payload, []byte(`"entry_count":1`), []byte(`"entry_count":0`), 1), []byte(`"total_bytes":3`), []byte(`"total_bytes":0`), 1)
	if _, err := DecodeAndValidateRunResponse(request, zeroCounts); err != nil {
		t.Fatalf("workspace receipt with legitimate zero counts was rejected: %v", err)
	}
	discarded := bytes.Replace(payload, []byte(`"policy":"export_on_success","disposition":"exported"`), []byte(`"policy":"discard","disposition":"discarded"`), 1)
	discarded = bytes.Replace(discarded, []byte(`,"capsule_sha256":"sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"`), nil, 1)
	if _, err := DecodeAndValidateRunResponse(request, discarded); err != nil {
		t.Fatalf("discarded receipt without capsule identity was rejected: %v", err)
	}
	nullExecutionRef := []byte(`{"status":"ok","result":1,"receipts":[],"metrics":{"capability_calls":0,"result_bytes":1},"error":null,"execution_ref":null}`)
	if _, err := DecodeAndValidateRunResponse(request, nullExecutionRef); err == nil {
		t.Fatal("explicit null execution_ref was accepted")
	}
}

func TestWorkspaceReceiptRejectsDispositionPolicyMismatches(t *testing.T) {
	digest := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	capsule := digest
	base := WorkspaceReceipt{
		SchemaVersion: WorkspaceReceiptSchemaVersion, RequestSHA256: digest, Policy: WorkspaceExportOnSuccess,
		Disposition: WorkspaceExported, InitialWorkspaceSHA256: digest, FinalWorkspaceSHA256: digest,
		FinalTreeSHA256: digest, CapsuleSHA256: &capsule,
	}
	cases := map[string]WorkspaceReceipt{
		"export missing capsule": func() WorkspaceReceipt { value := base; value.CapsuleSHA256 = nil; return value }(),
		"discard has capsule":    func() WorkspaceReceipt { value := base; value.Disposition = WorkspaceDiscarded; return value }(),
		"discard policy exports": func() WorkspaceReceipt { value := base; value.Policy = WorkspaceDiscardPolicy; return value }(),
		"unknown policy":         func() WorkspaceReceipt { value := base; value.Policy = "unknown"; return value }(),
	}
	for name, receipt := range cases {
		t.Run(name, func(t *testing.T) {
			if err := receipt.Validate(); err == nil {
				t.Fatal("invalid workspace receipt was accepted")
			}
		})
	}
	if err := base.ValidateForStatus(RunResponseError); err == nil {
		t.Fatal("export_on_success exported an error response")
	}
	discarded := base
	discarded.Disposition = WorkspaceDiscarded
	discarded.CapsuleSHA256 = nil
	if err := discarded.ValidateForStatus(RunResponseError); err != nil {
		t.Fatalf("valid error disposition rejected: %v", err)
	}
}

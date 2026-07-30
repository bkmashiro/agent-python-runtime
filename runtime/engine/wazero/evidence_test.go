package wazero

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/receipt"
)

func TestProjectHostEvidenceAddsExecutionRefWithoutBrokerEvidence(t *testing.T) {
	payload := []byte(`{"status":"ok","result":{"value":42},"receipts":[],"metrics":{"guest_time_ms":1.5,"capability_calls":0,"result_bytes":12},"error":null}`)
	ref := runtimeconfig.ExecutionRef{
		InvocationRef: runtimeconfig.InvocationRef{
			AgentRunID: "agent-run-123", TurnSeq: 4, OutputItemSeq: 2, SegmentSeq: 0,
			InvocationID: "python-invocation-789", InvocationAttempt: 1, ExecutionID: "exec-456",
		},
		ExecutedCodeSHA256: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	merged, err := projectHostEvidence(payload, nil, 0, &ref, 1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if json.Unmarshal(merged, &document) != nil || len(document["execution_ref"]) == 0 {
		t.Fatalf("merged=%s", merged)
	}
	var got runtimeconfig.ExecutionRef
	if json.Unmarshal(document["execution_ref"], &got) != nil || got != ref {
		t.Fatalf("execution_ref=%s got=%+v", document["execution_ref"], got)
	}
	decoded, err := runtimeconfig.DecodeAndValidateRunResponse(runtimeconfig.RunRequest{RunID: "guest", Code: "result = 1", Inputs: json.RawMessage(`{}`)}, merged)
	if err != nil || decoded.ExecutionRef == nil || *decoded.ExecutionRef != ref {
		t.Fatalf("decoded=%+v err=%v", decoded, err)
	}
}

func TestProjectHostEvidenceRejectsGuestExecutionRefAndBrokerIdentityMismatch(t *testing.T) {
	forged := []byte(`{"status":"ok","result":{},"execution_ref":{"execution_id":"guest"},"receipts":[],"metrics":{"capability_calls":0},"error":null}`)
	if _, err := projectHostEvidence(forged, nil, 0, nil, 1024); !errors.Is(err, ErrGuestClaimedExecutionRef) {
		t.Fatalf("err=%v", err)
	}
	payload := []byte(`{"status":"ok","result":{},"receipts":[],"metrics":{"capability_calls":0},"error":null}`)
	ref := runtimeconfig.ExecutionRef{InvocationRef: runtimeconfig.InvocationRef{
		AgentRunID: "agent", InvocationID: "invocation", InvocationAttempt: 1, ExecutionID: "exec-1",
	}, ExecutedCodeSHA256: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	receiptFromOtherExecution := receipt.New("exec-2", "call-1", "pwd", 0, "", "ok", []byte(`{}`))
	if _, err := projectHostEvidence(payload, []receipt.Receipt{receiptFromOtherExecution}, 1, &ref, 1024*1024); !errors.Is(err, ErrExecutionIdentityMismatch) {
		t.Fatalf("err=%v", err)
	}
}

func TestMergeHostEvidenceOverwritesGuestClaims(t *testing.T) {
	payload := []byte(`{"status":"ok","result":{"value":42},"receipts":[{"guest":"forged"}],"metrics":{"guest_time_ms":1.5,"capability_calls":999,"result_bytes":12},"error":null}`)
	hostReceipt := receipt.New("host-run", "call-1", "fetch_many", 0, "https://api.example.test/ok", "ok", []byte("body"))
	merged, err := mergeHostEvidence(payload, []receipt.Receipt{hostReceipt}, 1, 1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Receipts []receipt.Receipt `json:"receipts"`
		Metrics  struct {
			CapabilityCalls uint32  `json:"capability_calls"`
			GuestTimeMS     float64 `json:"guest_time_ms"`
			ResultBytes     uint32  `json:"result_bytes"`
		} `json:"metrics"`
	}
	if err := json.Unmarshal(merged, &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Receipts) != 1 || response.Receipts[0].ReceiptID != hostReceipt.ReceiptID {
		t.Fatalf("Host receipts were not authoritative: %s", merged)
	}
	if response.Metrics.CapabilityCalls != 1 || response.Metrics.GuestTimeMS != 1.5 || response.Metrics.ResultBytes != 12 {
		t.Fatalf("Host metrics merge lost or trusted fields incorrectly: %s", merged)
	}
	if strings.Contains(string(merged), "forged") || strings.Contains(string(merged), "999") {
		t.Fatalf("guest evidence survived merge: %s", merged)
	}
}

func TestMergeHostEvidenceRejectsNonCanonicalEvidenceAliases(t *testing.T) {
	for _, test := range []struct {
		name    string
		payload string
	}{
		{
			name:    "top-level case-fold alias",
			payload: `{"status":"ok","result":{},"receipts":[],"receiptſ":[{"guest":"forged"}],"metrics":{"capability_calls":0},"error":null}`,
		},
		{
			name:    "nested case-fold alias",
			payload: `{"status":"ok","result":{},"receipts":[],"metrics":{"capability_calls":0,"capability_callſ":999},"error":null}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := mergeHostEvidence([]byte(test.payload), nil, 1, 1024*1024); err == nil {
				t.Fatal("non-canonical Host-evidence alias was accepted")
			}
		})
	}
}

func TestMergeHostEvidenceFailsClosedOnMalformedOrOversizedResponse(t *testing.T) {
	if _, err := mergeHostEvidence([]byte(`{"metrics":null}`), nil, 0, 1024); err == nil {
		t.Fatal("expected malformed metrics rejection")
	}
	payload := []byte(`{"status":"ok","result":null,"receipts":[],"metrics":{"capability_calls":0},"error":null}`)
	if _, err := mergeHostEvidence(payload, nil, 0, 8); err == nil {
		t.Fatal("expected merged response size rejection")
	}
}

package wazero

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/receipt"
)

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

func TestMergeHostEvidenceFailsClosedOnMalformedOrOversizedResponse(t *testing.T) {
	if _, err := mergeHostEvidence([]byte(`{"metrics":null}`), nil, 0, 1024); err == nil {
		t.Fatal("expected malformed metrics rejection")
	}
	payload := []byte(`{"status":"ok","result":null,"receipts":[],"metrics":{"capability_calls":0},"error":null}`)
	if _, err := mergeHostEvidence(payload, nil, 0, 8); err == nil {
		t.Fatal("expected merged response size rejection")
	}
}

package receipt_test

import (
	"strings"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/receipt"
)

func TestReceiptIdentityIsDeterministicAndBindsOperation(t *testing.T) {
	target := "https://api.example.test/data?token=secret"
	plan := "sha256:" + strings.Repeat("a", 64)
	first := receipt.New("host-run", plan, "call-1", "fetch_many", 0, target, "ok", []byte("first"))
	repeated := receipt.New("host-run", plan, "call-1", "fetch_many", 0, target, "error", nil)
	if first.ReceiptID != repeated.ReceiptID {
		t.Fatalf("outcome changed operation identity: %q != %q", first.ReceiptID, repeated.ReceiptID)
	}
	changed := []receipt.Receipt{
		receipt.New("other-run", plan, "call-1", "fetch_many", 0, target, "ok", nil),
		receipt.New("host-run", "sha256:"+strings.Repeat("b", 64), "call-1", "fetch_many", 0, target, "ok", nil),
		receipt.New("host-run", plan, "other-call", "fetch_many", 0, target, "ok", nil),
		receipt.New("host-run", plan, "call-1", "fetch_many", 1, target, "ok", nil),
		receipt.New("host-run", plan, "call-1", "fetch_many", 0, "https://api.example.test/other", "ok", nil),
	}
	for _, candidate := range changed {
		if candidate.ReceiptID == first.ReceiptID {
			t.Fatalf("distinct operation reused receipt ID: %#v", candidate)
		}
	}
}

func TestReceiptStoresDigestsNotRawTargetOrResponse(t *testing.T) {
	target := "https://api.example.test/data?token=secret"
	response := []byte("secret response")
	got := receipt.New("host-run", "sha256:"+strings.Repeat("a", 64), "call-1", "fetch_many", 0, target, "ok", response)
	for _, value := range []string{got.RequestSHA256, got.ResponseSHA256, got.ReceiptID} {
		if strings.Contains(value, "secret") {
			t.Fatalf("receipt leaked input: %#v", got)
		}
	}
	if len(got.RequestSHA256) != 64 || len(got.ResponseSHA256) != 64 || !strings.HasPrefix(got.ReceiptID, "rcpt_") {
		t.Fatalf("receipt digest format is invalid: %#v", got)
	}
}

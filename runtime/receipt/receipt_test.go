package receipt_test

import (
	"strings"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/receipt"
)

func TestReceiptIdentityIsDeterministicAndBindsOperation(t *testing.T) {
	first := receipt.New("host-run", "call-1", "fetch_many", 0, "https://api.example.test/data?token=secret", "ok", 42)
	repeated := receipt.New("host-run", "call-1", "fetch_many", 0, "https://api.example.test/data?token=secret", "error", 0)
	if first.ID != repeated.ID {
		t.Fatalf("outcome changed operation identity: %q != %q", first.ID, repeated.ID)
	}
	changed := []receipt.Receipt{
		receipt.New("other-run", "call-1", "fetch_many", 0, "https://api.example.test/data?token=secret", "ok", 42),
		receipt.New("host-run", "other-call", "fetch_many", 0, "https://api.example.test/data?token=secret", "ok", 42),
		receipt.New("host-run", "call-1", "fetch_many", 1, "https://api.example.test/data?token=secret", "ok", 42),
		receipt.New("host-run", "call-1", "fetch_many", 0, "https://api.example.test/other", "ok", 42),
	}
	for _, candidate := range changed {
		if candidate.ID == first.ID {
			t.Fatalf("distinct operation reused receipt ID: %#v", candidate)
		}
	}
}

func TestReceiptStoresDigestNotRawTarget(t *testing.T) {
	target := "https://api.example.test/data?token=secret"
	got := receipt.New("host-run", "call-1", "fetch_many", 0, target, "ok", 42)
	if strings.Contains(got.TargetDigest, "secret") || strings.Contains(got.ID, "secret") {
		t.Fatalf("receipt leaked target: %#v", got)
	}
	if !strings.HasPrefix(got.TargetDigest, "sha256:") || !strings.HasPrefix(got.ID, "rcpt_") {
		t.Fatalf("receipt identity format is not versioned: %#v", got)
	}
}

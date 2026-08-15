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

func TestProgrammaticReceiptProjectsAndBindsParentAndChildIdentity(t *testing.T) {
	plan := "sha256:" + strings.Repeat("a", 64)
	got := receipt.NewBound("host-run", plan, "parent-call:program:1", "parent-call", "fetch_many", 0, `{}`, "ok", nil)
	changed := receipt.NewBound("host-run", plan, "other-parent:program:1", "other-parent", "fetch_many", 0, `{}`, "ok", nil)
	if got.CallID != "parent-call:program:1" || got.ParentCallID != "parent-call" || got.ReceiptID == changed.ReceiptID || !receipt.ValidIdentity(got) {
		t.Fatalf("receipt identity projection = %#v changed=%#v", got, changed)
	}
	got.ParentCallID = "other-parent"
	if receipt.ValidIdentity(got) {
		t.Fatal("tampered parent retained valid receipt identity")
	}
}

func TestApprovalRequestChangesReceiptIdentityAndIsProjected(t *testing.T) {
	plan := "sha256:" + strings.Repeat("a", 64)
	first := receipt.NewAuthorized("host-run", plan, "call-1", "", "apr_"+strings.Repeat("1", 64), "fetch_many", 0, `{}`, "ok", nil)
	second := receipt.NewAuthorized("host-run", plan, "call-1", "", "apr_"+strings.Repeat("2", 64), "fetch_many", 0, `{}`, "ok", nil)
	if first.ReceiptID == second.ReceiptID || first.ApprovalRequestID == "" || !receipt.ValidIdentity(first) {
		t.Fatalf("approval binding missing: first=%#v second=%#v", first, second)
	}
	first.ApprovalRequestID = second.ApprovalRequestID
	if receipt.ValidIdentity(first) {
		t.Fatal("tampered approval request retained valid receipt identity")
	}
}

func TestSourceBindingChangesReceiptIdentityAndRejectsTampering(t *testing.T) {
	plan := "sha256:" + strings.Repeat("a", 64)
	base := receipt.NewBound("host-run", plan, "parent-call:program:1", "parent-call", "fetch_many", 0, `{}`, "ok", nil)
	binding := receipt.SourceBinding{
		SchemaVersion: receipt.SourceBindingSchemaVersion, ClaimLevel: receipt.SourceClaimBound,
		DocumentID: "sha256:" + strings.Repeat("b", 64), SourceSHA256: "sha256:" + strings.Repeat("c", 64),
		OccurrenceID: "sha256:" + strings.Repeat("d", 64), Capability: "fetch_many", DynamicOccurrence: 1,
		StartLine: 3, StartColumn: 4, EndLine: 3, EndColumn: 19,
	}
	bound, err := receipt.BindSource(base, binding)
	if err != nil || bound.Source == nil || bound.ReceiptID == base.ReceiptID || !receipt.ValidIdentity(bound) {
		t.Fatalf("bound=%#v base=%#v err=%v", bound, base, err)
	}
	bound.Source.StartColumn++
	if receipt.ValidIdentity(bound) {
		t.Fatal("tampered source span retained valid receipt identity")
	}
	if _, err := receipt.BindSource(base, receipt.SourceBinding{}); err == nil {
		t.Fatal("invalid source binding accepted")
	}
	mismatched := binding
	mismatched.Capability = "other.read"
	if _, err := receipt.BindSource(base, mismatched); err == nil {
		t.Fatal("source capability mismatch accepted")
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

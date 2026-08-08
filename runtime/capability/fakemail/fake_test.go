package fakemail_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability/fakemail"
	"github.com/bkmashiro/agent-python-runtime/runtime/transaction"
)

const mailCatalog = "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"

type mailIDs struct{ next int }

func (ids *mailIDs) New(prefix string) (string, error) {
	ids.next++
	return fmt.Sprintf("%s_%d", prefix, ids.next), nil
}

type mailFixture struct {
	now         time.Time
	coordinator *transaction.Coordinator
	transaction transaction.Transaction
	adapter     *fakemail.Adapter
	provider    *fakemail.Provider
	resolver    *capability.StaticSecretResolver
	audits      *[]capability.SecretAudit
	broker      *capability.Broker
	nextCall    int
}

func newMailFixture(t testing.TB, sendResolverToken []byte) *mailFixture {
	t.Helper()
	now := time.Unix(900, 0).UTC()
	audits := []capability.SecretAudit{}
	resolver, err := capability.NewStaticSecretResolver(map[capability.SecretRef][]byte{
		"mail.read":  []byte("read-token"),
		"mail.draft": []byte("draft-token"),
		"mail.send":  append([]byte(nil), sendResolverToken...),
	}, func() time.Time { return now }, func(a capability.SecretAudit) { audits = append(audits, a) })
	if err != nil {
		t.Fatal(err)
	}
	provider, err := fakemail.NewProvider([]fakemail.Message{
		{ID: "message:1", From: "sender@example.invalid", To: []string{"owner@example.invalid"}, Subject: "Build report", Body: "all tests passed", Version: 1},
		{ID: "message:2", From: "alerts@example.invalid", To: []string{"owner@example.invalid"}, Subject: "Alert", Body: "quota warning", Version: 2},
	}, []byte("read-token"), []byte("draft-token"), []byte("send-token"))
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := fakemail.NewAdapter(fakemail.Config{Resolver: resolver, ReadSecretRef: "mail.read", DraftSecretRef: "mail.draft", SendSecretRef: "mail.send", RunIdentity: "run:mail", TaskIdentity: "task:mail", Tenant: "tenant:mail", AccountAlias: "mailbox:test", PolicyVersion: "mail:v1", LeaseDuration: time.Minute, Provider: provider})
	if err != nil {
		t.Fatal(err)
	}
	coordinator := transaction.NewCoordinator(transaction.NewMemoryLedger(), &mailIDs{}, func() time.Time { return now }, nil)
	fixture := &mailFixture{now: now, coordinator: coordinator, adapter: adapter, provider: provider, resolver: resolver, audits: &audits}
	fixture.newTransactionBroker(t)
	t.Cleanup(func() { resolver.Close(); provider.Close() })
	return fixture
}

func (fixture *mailFixture) newTransactionBroker(t testing.TB) {
	t.Helper()
	tx, err := fixture.coordinator.Begin(transaction.BeginRequest{RunID: "run:mail", CatalogDigest: mailCatalog, Mode: transaction.TransactionModeWorkflow})
	if err != nil {
		t.Fatal(err)
	}
	fixture.transaction = tx
	binder, err := capability.NewCoordinatorBinder(fixture.coordinator, tx.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	registry := capability.NewRegistry()
	specs, _ := fakemail.HandlerSpecs(fixture.adapter)
	for _, spec := range specs {
		if err := registry.Register(spec); err != nil {
			t.Fatal(err)
		}
	}
	grants, _ := fakemail.ToolGrants("mail:v1", 32)
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "run:mail", CatalogDigest: mailCatalog, Registry: registry, Binder: binder, ToolGrants: grants, MaxTransactionCalls: 64}, capability.FetcherFunc(func(context.Context, capability.ResolvedRequest, uint32) (capability.FetchOutput, error) {
		return capability.FetchOutput{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	fixture.broker = broker
	fixture.nextCall = 0
}

type mailResponse struct {
	Status string          `json:"status"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code string `json:"code"`
	} `json:"error"`
}

func (fixture *mailFixture) call(t testing.TB, toolID string, arguments any) mailResponse {
	t.Helper()
	fixture.nextCall++
	argumentBytes, _ := json.Marshal(arguments)
	envelope := map[string]any{"call_id": fmt.Sprintf("call:%d", fixture.nextCall), "capability": toolID, "catalog_digest": mailCatalog, "handler_version": fakemail.HandlerVersion, "arguments": json.RawMessage(argumentBytes)}
	payload, _ := json.Marshal(envelope)
	raw, err := fixture.broker.Call(context.Background(), payload)
	if err != nil {
		t.Fatal(err)
	}
	var response mailResponse
	if json.Unmarshal(raw, &response) != nil {
		t.Fatalf("response=%s", raw)
	}
	return response
}

func TestFakeMailSearchReadAndDraftRollback(t *testing.T) {
	fixture := newMailFixture(t, []byte("send-token"))
	search := fixture.call(t, fakemail.SearchToolID, map[string]any{"query": "tests", "max_results": 10})
	if search.Status != "ok" || strings.Contains(string(search.Result), "token") {
		t.Fatalf("search=%+v", search)
	}
	var searchResult struct {
		Messages []fakemail.Message `json:"messages"`
	}
	if json.Unmarshal(search.Result, &searchResult) != nil || len(searchResult.Messages) != 1 || searchResult.Messages[0].ID != "message:1" {
		t.Fatalf("result=%s", search.Result)
	}
	read := fixture.call(t, fakemail.ReadManyToolID, map[string]any{"message_ids": []string{"message:2", "message:1"}})
	var readResult struct {
		Messages []fakemail.Message `json:"messages"`
	}
	if read.Status != "ok" || json.Unmarshal(read.Result, &readResult) != nil || len(readResult.Messages) != 2 || readResult.Messages[0].ID != "message:2" {
		t.Fatalf("read=%+v result=%s", read, read.Result)
	}
	drafted := fixture.call(t, fakemail.DraftPrepareToolID, map[string]any{"to": []string{"recipient@example.invalid"}, "subject": "Draft", "body": "body"})
	var draft fakemail.Draft
	if drafted.Status != "ok" || json.Unmarshal(drafted.Result, &draft) != nil || draft.ID == "" {
		t.Fatalf("draft=%+v result=%s", drafted, drafted.Result)
	}
	if err := fixture.broker.RollbackCurrentTransaction(context.Background(), "guest_error"); err != nil {
		t.Fatal(err)
	}
	if len(fixture.provider.Drafts()) != 0 {
		t.Fatalf("draft rollback failed: %+v", fixture.provider.Drafts())
	}
}

func TestFakeMailDraftUpdateAndDeleteRollbackWithDriftProtection(t *testing.T) {
	fixture := newMailFixture(t, []byte("send-token"))
	createdResponse := fixture.call(t, fakemail.DraftPrepareToolID, map[string]any{"to": []string{"recipient@example.invalid"}, "subject": "Before", "body": "before"})
	var created fakemail.Draft
	_ = json.Unmarshal(createdResponse.Result, &created)
	if err := fixture.broker.FinalizeRun(context.Background(), true, "success"); err != nil {
		t.Fatal(err)
	}

	fixture.newTransactionBroker(t)
	updatedResponse := fixture.call(t, fakemail.DraftUpdateToolID, map[string]any{"draft_id": created.ID, "expected_version": created.Version, "to": []string{"recipient@example.invalid"}, "subject": "After", "body": "after"})
	var updated fakemail.Draft
	if updatedResponse.Status != "ok" || json.Unmarshal(updatedResponse.Result, &updated) != nil {
		t.Fatalf("update=%+v", updatedResponse)
	}
	if err := fixture.broker.RollbackCurrentTransaction(context.Background(), "guest_error"); err != nil {
		t.Fatal(err)
	}
	restored := fixture.provider.Drafts()[0]
	if restored.Subject != "Before" || restored.Body != "before" {
		t.Fatalf("restored=%+v", restored)
	}

	fixture.newTransactionBroker(t)
	deleteResponse := fixture.call(t, fakemail.DraftDeleteToolID, map[string]any{"draft_id": restored.ID, "expected_version": restored.Version})
	if deleteResponse.Status != "ok" || len(fixture.provider.Drafts()) != 0 {
		t.Fatalf("delete=%+v drafts=%+v", deleteResponse, fixture.provider.Drafts())
	}
	if err := fixture.broker.RollbackCurrentTransaction(context.Background(), "guest_error"); err != nil {
		t.Fatal(err)
	}
	restored = fixture.provider.Drafts()[0]

	fixture.newTransactionBroker(t)
	updatedResponse = fixture.call(t, fakemail.DraftUpdateToolID, map[string]any{"draft_id": restored.ID, "expected_version": restored.Version, "to": []string{"recipient@example.invalid"}, "subject": "Next", "body": "next"})
	if updatedResponse.Status != "ok" {
		t.Fatalf("second update=%+v", updatedResponse)
	}
	if err := fixture.provider.DriftDraft(restored.ID, "external"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.broker.RollbackCurrentTransaction(context.Background(), "guest_error"); err == nil {
		t.Fatal("rollback overwrote external draft drift")
	}
	if fixture.provider.Drafts()[0].Body != "external" {
		t.Fatalf("drafts=%+v", fixture.provider.Drafts())
	}
}

func TestFakeMailSendRequiresApprovalCommitsOnceAndNeverResolvesSecretDuringPrepare(t *testing.T) {
	fixture := newMailFixture(t, []byte("send-token"))
	controller, err := fakemail.NewSendController(fixture.coordinator, fixture.transaction.ID, fixture.adapter)
	if err != nil {
		t.Fatal(err)
	}
	staged, err := controller.Prepare(fakemail.SendRequest{To: []string{"recipient@example.invalid"}, Subject: "Subject", Body: "Body"})
	if err != nil || fixture.provider.SentCount() != 0 {
		t.Fatalf("staged=%+v sent=%d err=%v", staged, fixture.provider.SentCount(), err)
	}
	for _, audit := range *fixture.audits {
		if audit.Operation == "commit" {
			t.Fatal("prepare resolved send credential")
		}
	}
	credential := transaction.CommitCredential{Token: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}
	if _, err := controller.Commit(context.Background(), credential, staged.OperationID); !errors.Is(err, fakemail.ErrSendApprovalRequired) {
		t.Fatalf("unapproved commit err=%v", err)
	}
	if fixture.provider.SentCount() != 0 {
		t.Fatal("unapproved send dispatched")
	}
	if err := controller.RegisterApproval(credential, staged.OperationID, "approval:mail", "owner", fixture.now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	receipt, err := controller.Commit(context.Background(), credential, staged.OperationID)
	if err != nil || receipt.ProviderMessageID == "" || fixture.provider.SentCount() != 1 {
		t.Fatalf("receipt=%+v sent=%d err=%v", receipt, fixture.provider.SentCount(), err)
	}
	replayed, err := controller.Commit(context.Background(), credential, staged.OperationID)
	if err != nil || replayed != receipt || fixture.provider.SentCount() != 1 {
		t.Fatalf("replay=%+v sent=%d err=%v", replayed, fixture.provider.SentCount(), err)
	}
	if _, err := fixture.coordinator.FinalizeWorkflow(fixture.transaction.ID); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(struct {
		Staged  fakemail.StagedSend  `json:"staged"`
		Receipt fakemail.SendReceipt `json:"receipt"`
	}{staged, receipt})
	if strings.Contains(string(encoded), credential.Token) || strings.Contains(string(encoded), "send-token") {
		t.Fatalf("send evidence leaked secret: %s", encoded)
	}
}

func TestFakeMailAmbiguousSendReconcilesWithoutDuplicate(t *testing.T) {
	fixture := newMailFixture(t, []byte("send-token"))
	controller, _ := fakemail.NewSendController(fixture.coordinator, fixture.transaction.ID, fixture.adapter)
	staged, _ := controller.Prepare(fakemail.SendRequest{To: []string{"recipient@example.invalid"}, Subject: "Subject", Body: "Body"})
	credential := transaction.CommitCredential{Token: "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"}
	if err := controller.RegisterApproval(credential, staged.OperationID, "approval:ambiguous", "owner", fixture.now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	fixture.provider.SetAmbiguousNextSend()
	if _, err := controller.Commit(context.Background(), credential, staged.OperationID); !errors.Is(err, fakemail.ErrSendReconciliation) {
		t.Fatalf("ambiguous commit err=%v", err)
	}
	if fixture.provider.SentCount() != 1 {
		t.Fatalf("sent=%d", fixture.provider.SentCount())
	}
	if _, err := controller.Commit(context.Background(), credential, staged.OperationID); !errors.Is(err, fakemail.ErrSendReconciliation) || fixture.provider.SentCount() != 1 {
		t.Fatalf("ambiguous replay sent=%d err=%v", fixture.provider.SentCount(), err)
	}
	receipt, err := controller.Reconcile(context.Background(), credential, staged.OperationID)
	if err != nil || receipt.ProviderMessageID == "" || fixture.provider.SentCount() != 1 {
		t.Fatalf("receipt=%+v sent=%d err=%v", receipt, fixture.provider.SentCount(), err)
	}
	if _, err := fixture.coordinator.FinalizeWorkflow(fixture.transaction.ID); err != nil {
		t.Fatal(err)
	}
}

func TestFakeMailAcceptedTimeoutRequiresReadbackAndNeverBlindRetries(t *testing.T) {
	fixture := newMailFixture(t, []byte("send-token"))
	controller, _ := fakemail.NewSendController(fixture.coordinator, fixture.transaction.ID, fixture.adapter)
	staged, _ := controller.Prepare(fakemail.SendRequest{To: []string{"recipient@example.invalid"}, Subject: "Subject", Body: "Body"})
	credential := transaction.CommitCredential{Token: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}
	if err := controller.RegisterApproval(credential, staged.OperationID, "approval:timeout", "owner", fixture.now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	fixture.provider.SetAcceptedTimeoutNextSend()
	if _, err := controller.Commit(context.Background(), credential, staged.OperationID); !errors.Is(err, fakemail.ErrSendReconciliation) {
		t.Fatalf("timeout commit err=%v", err)
	}
	if fixture.provider.SentCount() != 1 {
		t.Fatalf("sent=%d", fixture.provider.SentCount())
	}
	inspection, err := fixture.coordinator.Inspect(fixture.transaction.ID, nil)
	if err != nil || len(inspection.Attempts) != 1 || inspection.Attempts[0].State != transaction.AttemptAmbiguous {
		t.Fatalf("inspection=%+v err=%v", inspection, err)
	}
	if _, err := controller.Commit(context.Background(), credential, staged.OperationID); !errors.Is(err, fakemail.ErrSendReconciliation) || fixture.provider.SentCount() != 1 {
		t.Fatalf("blind retry sent=%d err=%v", fixture.provider.SentCount(), err)
	}
	receipt, err := controller.Reconcile(context.Background(), credential, staged.OperationID)
	if err != nil || receipt.ProviderMessageID == "" || fixture.provider.SentCount() != 1 {
		t.Fatalf("receipt=%+v sent=%d err=%v", receipt, fixture.provider.SentCount(), err)
	}
	inspection, err = fixture.coordinator.Inspect(fixture.transaction.ID, nil)
	if err != nil || len(inspection.Attempts) != 1 || inspection.Attempts[0].State != transaction.AttemptSucceeded ||
		inspection.Attempts[0].ReconciliationDigest != receipt.ReceiptDigest {
		t.Fatalf("reconciled inspection=%+v receipt=%+v err=%v", inspection, receipt, err)
	}
}

func TestFakeMailRejectsRealRecipientAndWrongSendCredential(t *testing.T) {
	fixture := newMailFixture(t, []byte("wrong-token"))
	controller, _ := fakemail.NewSendController(fixture.coordinator, fixture.transaction.ID, fixture.adapter)
	if _, err := controller.Prepare(fakemail.SendRequest{To: []string{"real@example.com"}, Subject: "Subject", Body: "Body"}); !errors.Is(err, fakemail.ErrMailboxDenied) {
		t.Fatalf("real recipient err=%v", err)
	}
	staged, _ := controller.Prepare(fakemail.SendRequest{To: []string{"recipient@example.invalid"}, Subject: "Subject", Body: "Body"})
	credential := transaction.CommitCredential{Token: "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"}
	if err := controller.RegisterApproval(credential, staged.OperationID, "approval:wrong-secret", "owner", fixture.now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Commit(context.Background(), credential, staged.OperationID); err == nil || fixture.provider.SentCount() != 0 {
		t.Fatalf("wrong credential sent=%d err=%v", fixture.provider.SentCount(), err)
	}
}

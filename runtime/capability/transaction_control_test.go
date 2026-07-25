package capability_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/transaction"
)

type controlIDs struct{ next int }

func (ids *controlIDs) New(prefix string) (string, error) {
	ids.next++
	return fmt.Sprintf("%s_%d", prefix, ids.next), nil
}

type controlledHandler struct {
	applied, rolledBack, compensated int
	fail                             bool
}

func (handler *controlledHandler) Handle(context.Context, capability.HostCall) (json.RawMessage, error) {
	if handler.fail {
		return nil, fmt.Errorf("fixture handler failure")
	}
	handler.applied++
	return json.RawMessage(`{"ok":true}`), nil
}
func (handler *controlledHandler) Rollback(context.Context, capability.AbortCall) error {
	handler.rolledBack++
	return nil
}
func (handler *controlledHandler) Compensate(context.Context, capability.AbortCall) error {
	handler.compensated++
	return nil
}

func newControlledBroker(t *testing.T, toolID, effectClass string, autoCompensate, fail bool) (*capability.Broker, *controlledHandler) {
	t.Helper()
	handler := &controlledHandler{fail: fail}
	registry := capability.NewRegistry()
	if err := registry.Register(capability.HandlerSpec{
		ToolID: toolID, HandlerVersion: "v1", Handler: handler,
		InputSchema:  []byte(`{"type":"object","additionalProperties":false}`),
		OutputSchema: []byte(`{"type":"object","additionalProperties":false,"required":["ok"],"properties":{"ok":{"type":"boolean"}}}`),
	}); err != nil {
		t.Fatal(err)
	}
	catalogDigest := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	coordinator := transaction.NewCoordinator(transaction.NewMemoryLedger(), &controlIDs{}, func() time.Time { return time.Unix(100, 0).UTC() }, nil)
	tx, err := coordinator.Begin(transaction.BeginRequest{RunID: "run-control", CatalogDigest: catalogDigest, Mode: transaction.TransactionModeWorkflow})
	if err != nil {
		t.Fatal(err)
	}
	binder, err := capability.NewCoordinatorBinder(coordinator, tx.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	auto := map[string]bool{}
	if autoCompensate {
		auto[toolID] = true
	}
	broker, err := capability.NewBroker(capability.Config{
		RunIdentity: "run-control", CatalogDigest: catalogDigest, Registry: registry, Binder: binder,
		ToolGrants:          map[string]capability.ToolGrant{toolID: {ToolID: toolID, HandlerVersion: "v1", EffectClass: effectClass, Policy: "AUTO_COMMIT", PolicyVersion: "grant_v1", MaxCalls: 1}},
		AutoCompensateTools: auto,
	}, capability.FetcherFunc(func(context.Context, capability.ResolvedRequest, uint32) (capability.FetchOutput, error) {
		return capability.FetchOutput{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	payload := fmt.Sprintf(`{"call_id":"call:1","capability":%q,"catalog_digest":%q,"handler_version":"v1","arguments":{}}`, toolID, catalogDigest)
	if _, err := broker.Call(context.Background(), []byte(payload)); err != nil {
		t.Fatal(err)
	}
	return broker, handler
}

func TestHostControlsRollbackAndCompensateOnlyCurrentTransaction(t *testing.T) {
	t.Run("rollback", func(t *testing.T) {
		broker, handler := newControlledBroker(t, "config.set", "reversible", false, false)
		if err := broker.RollbackCurrentTransaction(context.Background(), "guest_error"); err != nil {
			t.Fatal(err)
		}
		inspection, err := broker.InspectTransaction()
		if err != nil || inspection.Transaction.State != transaction.TransactionRolledBack || handler.rolledBack != 1 || handler.compensated != 0 {
			t.Fatalf("inspection=%+v handler=%+v err=%v", inspection, handler, err)
		}
	})
	t.Run("compensate", func(t *testing.T) {
		broker, handler := newControlledBroker(t, "inventory.reserve", "compensatable", true, false)
		if err := broker.CompensateCurrentTransaction(context.Background(), "guest_error"); err != nil {
			t.Fatal(err)
		}
		inspection, err := broker.InspectTransaction()
		if err != nil || inspection.Transaction.State != transaction.TransactionCompensated || handler.compensated != 1 || handler.rolledBack != 0 {
			t.Fatalf("inspection=%+v handler=%+v err=%v", inspection, handler, err)
		}
	})
	t.Run("compensation requires Host policy", func(t *testing.T) {
		broker, handler := newControlledBroker(t, "inventory.reserve", "compensatable", false, false)
		if err := broker.CompensateCurrentTransaction(context.Background(), "guest_error"); err == nil {
			t.Fatal("compensation without Host policy succeeded")
		}
		if handler.compensated != 0 {
			t.Fatal("compensation handler was called")
		}
	})
}

func TestRunFinalizerAutoAbortsCurrentWorkflow(t *testing.T) {
	broker, handler := newControlledBroker(t, "config.set", "reversible", false, false)
	if err := broker.FinalizeRun(context.Background(), false, "invalid_output"); err != nil {
		t.Fatal(err)
	}
	inspection, err := broker.InspectTransaction()
	if err != nil || inspection.Transaction.State != transaction.TransactionRolledBack || handler.rolledBack != 1 {
		t.Fatalf("inspection=%+v handler=%+v err=%v", inspection, handler, err)
	}

	failedBroker, _ := newControlledBroker(t, "config.set", "reversible", false, true)
	if err := failedBroker.FinalizeRun(context.Background(), true, "success"); err == nil {
		t.Fatal("workflow with a rejected operation committed")
	}
	failedInspection, err := failedBroker.InspectTransaction()
	if err != nil || failedInspection.Transaction.State != transaction.TransactionAborted {
		t.Fatalf("failed inspection=%+v err=%v", failedInspection, err)
	}
}

func TestBuiltinFetchManyUsesRegistryCoordinatorAndBoundReceipts(t *testing.T) {
	grant := capability.Grant{
		Name: capability.FetchManyCapability, MaxCalls: 1, MaxRequestsPerCall: 1, MaxTotalRequests: 1,
		MaxConcurrency: 1, PerRequestTimeout: time.Second, MaxResponseBytes: 1024,
		Targets: map[string]capability.TargetGrant{"api": {BaseURL: "https://example.test", Headers: map[string]string{"X-Test": "secret"}}},
	}
	fetcher := capability.FetcherFunc(func(_ context.Context, request capability.ResolvedRequest, _ uint32) (capability.FetchOutput, error) {
		if request.URL != "https://example.test/item" || request.Headers["X-Test"] != "secret" {
			t.Fatalf("resolved request=%+v", request)
		}
		return capability.FetchOutput{StatusCode: 200, Body: []byte(`{"ok":true}`), ContentType: "application/json"}, nil
	})
	registry, toolGrant, catalogDigest, err := capability.BuildBuiltinFetchManyRegistry(grant, fetcher)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(capability.HandlerSpec{ToolID: "forged.tool", HandlerVersion: "v1", InputSchema: []byte(`{}`), OutputSchema: []byte(`{}`), Handler: capability.HandlerFunc(func(context.Context, capability.HostCall) (json.RawMessage, error) { return json.RawMessage(`{}`), nil })}); err == nil {
		t.Fatal("sealed builtin registry accepted a post-digest handler")
	}
	coordinator := transaction.NewCoordinator(transaction.NewMemoryLedger(), &controlIDs{}, func() time.Time { return time.Unix(200, 0).UTC() }, nil)
	tx, err := coordinator.Begin(transaction.BeginRequest{RunID: "run-fetch", CatalogDigest: catalogDigest, Mode: transaction.TransactionModeWorkflow})
	if err != nil {
		t.Fatal(err)
	}
	binder, _ := capability.NewCoordinatorBinder(coordinator, tx.ID, time.Minute)
	broker, err := capability.NewBroker(capability.Config{
		RunIdentity: "run-fetch", Grants: map[string]capability.Grant{capability.FetchManyCapability: grant},
		CatalogDigest: catalogDigest, Registry: registry, Binder: binder,
		ToolGrants: map[string]capability.ToolGrant{capability.FetchManyCapability: toolGrant},
	}, fetcher)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"call_id":"fetch:1","capability":"fetch_many","arguments":{"requests":[{"request_id":"one","target":"api","path":"item"}]}}`)
	responseBytes, err := broker.Call(context.Background(), payload)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := broker.Call(context.Background(), payload)
	if err != nil || string(replayed) != string(responseBytes) {
		t.Fatalf("replay=%s response=%s err=%v", replayed, responseBytes, err)
	}
	changed, err := broker.Call(context.Background(), []byte(`{"call_id":"fetch:1","capability":"fetch_many","arguments":{"requests":[{"request_id":"one","target":"api","path":"other"}]}}`))
	var changedResponse capability.ToolResponse
	if err != nil || json.Unmarshal(changed, &changedResponse) != nil || changedResponse.Error == nil || changedResponse.Error.Code != "duplicate_call_id" {
		t.Fatalf("changed replay=%s err=%v", changed, err)
	}
	var response capability.ToolResponse
	if json.Unmarshal(responseBytes, &response) != nil || response.Status != capability.StatusOK || len(response.Result.Items) != 1 {
		t.Fatalf("response=%s", responseBytes)
	}
	inspection, err := broker.InspectTransaction()
	if err != nil || len(inspection.Operations) != 1 || len(inspection.Attempts) != 1 || inspection.Operations[0].ToolID != capability.FetchManyCapability || inspection.Operations[0].State != transaction.OperationApplied {
		t.Fatalf("inspection=%+v err=%v", inspection, err)
	}
	receipts := broker.Receipts()
	if len(receipts) != 1 || receipts[0].TransactionID != tx.ID || receipts[0].OperationID == "" || receipts[0].AttemptID == "" || receipts[0].CatalogDigest != catalogDigest || receipts[0].HandlerVersion != "builtin-fetch-many-v1" || receipts[0].RequestSHA256 == "" {
		t.Fatalf("receipts=%+v", receipts)
	}
	if err := broker.FinalizeRun(context.Background(), true, "success"); err != nil {
		t.Fatal(err)
	}
	inspection, _ = broker.InspectTransaction()
	if inspection.Transaction.State != transaction.TransactionCommitted {
		t.Fatalf("final transaction=%+v", inspection.Transaction)
	}
}

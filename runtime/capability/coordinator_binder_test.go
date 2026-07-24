package capability_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/effect"
	"github.com/bkmashiro/agent-python-runtime/runtime/transaction"
)

type binderIDs struct{ next int }

func (ids *binderIDs) New(prefix string) (string, error) {
	ids.next++
	return fmt.Sprintf("%s_host_%d", prefix, ids.next), nil
}

func TestCoordinatorBinderCommitsDirectTypedReadThroughJournal(t *testing.T) {
	catalogDigest := digestForTest("coordinator-catalog")
	ledger := transaction.NewMemoryLedger()
	coordinator := transaction.NewCoordinator(ledger, &binderIDs{}, func() time.Time { return time.Unix(1000, 0).UTC() }, nil)
	tx, err := coordinator.Begin(transaction.BeginRequest{RunID: "run-coordinator", CatalogDigest: catalogDigest, Mode: transaction.TransactionModeDirect})
	if err != nil {
		t.Fatal(err)
	}
	binder, err := capability.NewCoordinatorBinder(coordinator, tx.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	registry := capability.NewRegistry()
	handlerCalls := 0
	if err := registry.Register(capability.HandlerSpec{
		ToolID: "demo.echo", HandlerVersion: "v1",
		InputSchema:  []byte(`{"type":"object","additionalProperties":false,"required":["text"],"properties":{"text":{"type":"string"}}}`),
		OutputSchema: []byte(`{"type":"object","additionalProperties":false,"required":["text"],"properties":{"text":{"type":"string"}}}`),
		Handler: capability.HandlerFunc(func(_ context.Context, call capability.HostCall) (json.RawMessage, error) {
			handlerCalls++
			return append(json.RawMessage(nil), call.Arguments...), nil
		}),
	}); err != nil {
		t.Fatal(err)
	}
	broker, err := capability.NewBroker(capability.Config{
		RunIdentity: "run-coordinator", CatalogDigest: catalogDigest, Registry: registry, Binder: binder,
		ToolGrants: map[string]capability.ToolGrant{
			"demo.echo": {ToolID: "demo.echo", HandlerVersion: "v1", EffectClass: "read_only", Policy: "AUTO_COMMIT", PolicyVersion: "grant_v1", MaxCalls: 1},
		},
	}, capability.FetcherFunc(func(context.Context, capability.ResolvedRequest, uint32) (capability.FetchOutput, error) {
		return capability.FetchOutput{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"call_id":"bound-call","capability":"demo.echo","catalog_digest":"` + catalogDigest + `","handler_version":"v1","arguments":{"text":"hello"}}`)
	response, err := broker.Call(context.Background(), payload)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Status capability.Status `json:"status"`
	}
	if err := json.Unmarshal(response, &decoded); err != nil || decoded.Status != capability.StatusOK {
		t.Fatalf("bound response=%s err=%v", response, err)
	}
	inspected, err := coordinator.InspectTransaction(tx.ID)
	if err != nil || inspected.State != transaction.TransactionCommitted {
		t.Fatalf("direct transaction = %+v, %v", inspected, err)
	}
	receipts := broker.Receipts()
	if len(receipts) != 1 || receipts[0].TransactionID != tx.ID || receipts[0].OperationID == "" || receipts[0].AttemptID == "" {
		t.Fatalf("unbound receipt: %+v", receipts)
	}
	operation, err := coordinator.InspectOperation(receipts[0].OperationID)
	if err != nil || operation.State != transaction.OperationApplied || operation.ManifestDigest != receipts[0].ManifestDigest {
		t.Fatalf("journaled operation = %+v, %v", operation, err)
	}
	if receipts[0].ProviderRequestDigest != digestForTest(string(payload)) {
		t.Fatalf("provider request digest = %q", receipts[0].ProviderRequestDigest)
	}
	recreatedBinder, err := capability.NewCoordinatorBinder(coordinator, tx.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	recreatedBroker, err := capability.NewBroker(capability.Config{
		RunIdentity: "run-coordinator", CatalogDigest: catalogDigest, Registry: registry, Binder: recreatedBinder,
		ToolGrants: map[string]capability.ToolGrant{
			"demo.echo": {ToolID: "demo.echo", HandlerVersion: "v1", EffectClass: "read_only", Policy: "AUTO_COMMIT", PolicyVersion: "grant_v1", MaxCalls: 1},
		},
	}, capability.FetcherFunc(func(context.Context, capability.ResolvedRequest, uint32) (capability.FetchOutput, error) {
		return capability.FetchOutput{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	replayResponse, err := recreatedBroker.Call(context.Background(), payload)
	if err != nil {
		t.Fatal(err)
	}
	var replay struct {
		Status capability.Status `json:"status"`
		Error  *capability.Error `json:"error"`
	}
	if err := json.Unmarshal(replayResponse, &replay); err != nil || replay.Status != capability.StatusError || replay.Error == nil || replay.Error.Code != "reconciliation_required" {
		t.Fatalf("recreated replay=%s err=%v", replayResponse, err)
	}
	if handlerCalls != 1 {
		t.Fatalf("recreated Broker re-entered handler %d times", handlerCalls)
	}
}

func TestCoordinatorBinderAppliesLocalReversibleHandlerButBlocksAuthorityClasses(t *testing.T) {
	catalogDigest := digestForTest("effectful-catalog")
	ledger := transaction.NewMemoryLedger()
	coordinator := transaction.NewCoordinator(ledger, &binderIDs{}, func() time.Time { return time.Unix(1050, 0).UTC() }, nil)
	tx, err := coordinator.Begin(transaction.BeginRequest{RunID: "run-effectful", CatalogDigest: catalogDigest, Mode: transaction.TransactionModeWorkflow})
	if err != nil {
		t.Fatal(err)
	}
	binder, err := capability.NewCoordinatorBinder(coordinator, tx.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	store := effect.NewConfigStore(map[string]string{"feature": "off"})
	registry := capability.NewRegistry()
	if err := registry.Register(capability.HandlerSpec{
		ToolID: "config.set", HandlerVersion: "v1",
		InputSchema:  []byte(`{"type":"object","additionalProperties":false,"required":["resource_id","expected_version","value"],"properties":{"resource_id":{"type":"string"},"expected_version":{"type":"integer","minimum":1},"value":{"type":"string"}}}`),
		OutputSchema: []byte(`{"type":"object","additionalProperties":false,"required":["before_version","post_version","before_digest","post_digest"],"properties":{"before_version":{"type":"integer"},"post_version":{"type":"integer"},"before_digest":{"type":"string"},"post_digest":{"type":"string"}}}`),
		Handler: capability.HandlerFunc(func(_ context.Context, call capability.HostCall) (json.RawMessage, error) {
			var arguments struct {
				ResourceID      string `json:"resource_id"`
				ExpectedVersion uint64 `json:"expected_version"`
				Value           string `json:"value"`
			}
			if err := json.Unmarshal(call.Arguments, &arguments); err != nil {
				return nil, err
			}
			receipt, err := store.Apply(call.AttemptID, arguments.ResourceID, arguments.ExpectedVersion, arguments.Value)
			if err != nil {
				return nil, err
			}
			return json.Marshal(map[string]any{"before_version": receipt.BeforeVersion, "post_version": receipt.PostVersion, "before_digest": receipt.BeforeDigest, "post_digest": receipt.PostDigest})
		}),
	}); err != nil {
		t.Fatal(err)
	}
	reservationStore := effect.NewReservationStore()
	if err := registry.Register(capability.HandlerSpec{
		ToolID: "inventory.reserve", HandlerVersion: "v1",
		InputSchema:  []byte(`{"type":"object","additionalProperties":false,"required":["sku","quantity"],"properties":{"sku":{"type":"string"},"quantity":{"type":"integer","minimum":1}}}`),
		OutputSchema: []byte(`{"type":"object","additionalProperties":false,"required":["reservation_id","effect_digest"],"properties":{"reservation_id":{"type":"string"},"effect_digest":{"type":"string"}}}`),
		Handler: capability.HandlerFunc(func(_ context.Context, call capability.HostCall) (json.RawMessage, error) {
			var arguments struct {
				SKU      string `json:"sku"`
				Quantity uint32 `json:"quantity"`
			}
			if err := json.Unmarshal(call.Arguments, &arguments); err != nil {
				return nil, err
			}
			receipt, err := reservationStore.Reserve(call.AttemptID, arguments.SKU, arguments.Quantity)
			if err != nil {
				return nil, err
			}
			return json.Marshal(map[string]any{"reservation_id": receipt.ReservationID, "effect_digest": receipt.EffectDigest})
		}),
	}); err != nil {
		t.Fatal(err)
	}
	broker, err := capability.NewBroker(capability.Config{
		RunIdentity: "run-effectful", CatalogDigest: catalogDigest, Registry: registry, Binder: binder,
		ToolGrants: map[string]capability.ToolGrant{
			"config.set":        {ToolID: "config.set", HandlerVersion: "v1", EffectClass: "reversible", Policy: "AUTO_COMMIT", PolicyVersion: "grant_v1", MaxCalls: 1},
			"inventory.reserve": {ToolID: "inventory.reserve", HandlerVersion: "v1", EffectClass: "compensatable", Policy: "AUTO_COMMIT", PolicyVersion: "grant_v1", MaxCalls: 1},
		},
	}, capability.FetcherFunc(func(context.Context, capability.ResolvedRequest, uint32) (capability.FetchOutput, error) {
		return capability.FetchOutput{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"call_id":"set-call","capability":"config.set","catalog_digest":"` + catalogDigest + `","handler_version":"v1","arguments":{"resource_id":"feature","expected_version":1,"value":"on"}}`)
	response, err := broker.Call(context.Background(), payload)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Status capability.Status `json:"status"`
	}
	if err := json.Unmarshal(response, &decoded); err != nil || decoded.Status != capability.StatusOK || store.Value("feature") != "on" {
		t.Fatalf("response=%s value=%q err=%v", response, store.Value("feature"), err)
	}
	reservePayload := []byte(`{"call_id":"reserve-call","capability":"inventory.reserve","catalog_digest":"` + catalogDigest + `","handler_version":"v1","arguments":{"sku":"sku_1","quantity":2}}`)
	reserveResponse, err := broker.Call(context.Background(), reservePayload)
	if err != nil {
		t.Fatal(err)
	}
	var reserved struct {
		Status capability.Status `json:"status"`
		Result struct {
			ReservationID string `json:"reservation_id"`
		} `json:"result"`
	}
	if err := json.Unmarshal(reserveResponse, &reserved); err != nil || reserved.Status != capability.StatusOK || !reservationStore.Active(reserved.Result.ReservationID) {
		t.Fatalf("reserve response=%s err=%v", reserveResponse, err)
	}
	receipts := broker.Receipts()
	if len(receipts) != 2 {
		t.Fatalf("receipts=%+v", receipts)
	}
	operation, err := coordinator.InspectOperation(receipts[0].OperationID)
	if err != nil || operation.EffectClass != transaction.EffectReversible || operation.State != transaction.OperationApplied {
		t.Fatalf("operation=%+v err=%v", operation, err)
	}
	compensatable, err := coordinator.InspectOperation(receipts[1].OperationID)
	if err != nil || compensatable.EffectClass != transaction.EffectCompensatable || compensatable.State != transaction.OperationApplied {
		t.Fatalf("compensatable operation=%+v err=%v", compensatable, err)
	}
	for _, denied := range []capability.BoundCall{
		{RunIdentity: "run-effectful", ToolID: "mail.send", CatalogDigest: catalogDigest, HandlerVersion: "v1", EffectClass: "irreversible", Policy: "AUTO_COMMIT", PolicyVersion: "grant_v1", ArgumentsDigest: digestForTest("mail-args"), RequestDigest: digestForTest("mail-request")},
		{RunIdentity: "run-effectful", ToolID: "config.set", CatalogDigest: catalogDigest, HandlerVersion: "v1", EffectClass: "reversible", Policy: "USER_APPROVAL_REQUIRED", PolicyVersion: "grant_v1", ArgumentsDigest: digestForTest("approval-args"), RequestDigest: digestForTest("approval-request")},
	} {
		if _, err := binder.Begin(context.Background(), denied); err == nil {
			t.Fatalf("authority-bearing call admitted: %+v", denied)
		}
	}
	inspection, err := coordinator.Inspect(tx.ID, nil)
	if err != nil || len(inspection.Operations) != 2 {
		t.Fatalf("denied calls created operations: %+v err=%v", inspection, err)
	}
}

func TestCoordinatorBinderRejectsDirectTransactionWhenResultSchemaFails(t *testing.T) {
	catalogDigest := digestForTest("failed-catalog")
	ledger := transaction.NewMemoryLedger()
	coordinator := transaction.NewCoordinator(ledger, &binderIDs{}, func() time.Time { return time.Unix(1100, 0).UTC() }, nil)
	tx, err := coordinator.Begin(transaction.BeginRequest{RunID: "run-failed", CatalogDigest: catalogDigest, Mode: transaction.TransactionModeDirect})
	if err != nil {
		t.Fatal(err)
	}
	binder, err := capability.NewCoordinatorBinder(coordinator, tx.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	registry := capability.NewRegistry()
	if err := registry.Register(capability.HandlerSpec{
		ToolID: "demo.bad", HandlerVersion: "v1",
		InputSchema:  []byte(`{"type":"object","properties":{}}`),
		OutputSchema: []byte(`{"type":"object","required":["ok"],"properties":{"ok":{"type":"boolean"}}}`),
		Handler:      capability.HandlerFunc(func(context.Context, capability.HostCall) (json.RawMessage, error) { return json.RawMessage(`{}`), nil }),
	}); err != nil {
		t.Fatal(err)
	}
	broker, err := capability.NewBroker(capability.Config{
		RunIdentity: "run-failed", CatalogDigest: catalogDigest, Registry: registry, Binder: binder,
		ToolGrants: map[string]capability.ToolGrant{
			"demo.bad": {ToolID: "demo.bad", HandlerVersion: "v1", EffectClass: "read_only", Policy: "AUTO_COMMIT", PolicyVersion: "grant_v1", MaxCalls: 1},
		},
	}, capability.FetcherFunc(func(context.Context, capability.ResolvedRequest, uint32) (capability.FetchOutput, error) {
		return capability.FetchOutput{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	response, err := broker.Call(context.Background(), []byte(`{"call_id":"bad-call","capability":"demo.bad","catalog_digest":"`+catalogDigest+`","handler_version":"v1","arguments":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Status capability.Status `json:"status"`
		Error  *capability.Error `json:"error"`
	}
	if err := json.Unmarshal(response, &decoded); err != nil || decoded.Status != capability.StatusError || decoded.Error == nil || decoded.Error.Code != "result_schema_mismatch" {
		t.Fatalf("schema failure response=%s err=%v", response, err)
	}
	inspected, err := coordinator.InspectTransaction(tx.ID)
	if err != nil || inspected.State != transaction.TransactionRejected {
		t.Fatalf("failed direct transaction = %+v, %v", inspected, err)
	}
}

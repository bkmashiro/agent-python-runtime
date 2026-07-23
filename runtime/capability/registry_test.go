package capability_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

func TestBrokerDispatchesRegisteredTypedToolWithCatalogAndHandlerBinding(t *testing.T) {
	registry := capability.NewRegistry()
	calls := 0
	binder := &recordingBinder{}
	err := registry.Register(capability.HandlerSpec{
		ToolID:         "demo.echo",
		HandlerVersion: "v1",
		InputSchema: []byte(`{
			"type":"object",
			"additionalProperties":false,
			"required":["text"],
			"properties":{"text":{"type":"string","maxLength":32}}
		}`),
		OutputSchema: []byte(`{
			"type":"object",
			"additionalProperties":false,
			"required":["text"],
			"properties":{"text":{"type":"string","maxLength":32}}
		}`),
		Handler: capability.HandlerFunc(func(_ context.Context, call capability.HostCall) (json.RawMessage, error) {
			calls++
			return append(json.RawMessage(nil), call.Arguments...), nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	catalogDigest := digestForTest("catalog")
	toolGrants := map[string]capability.ToolGrant{
		"demo.echo": {
			ToolID: "demo.echo", HandlerVersion: "v1", MaxCalls: 1,
			EffectClass: "read_only", Policy: "AUTO_COMMIT",
		},
	}
	broker, err := capability.NewBroker(capability.Config{
		RunIdentity:   "run-registry",
		CatalogDigest: catalogDigest,
		Registry:      registry,
		Binder:        binder,
		ToolGrants:    toolGrants,
	}, capability.FetcherFunc(func(context.Context, capability.ResolvedRequest, uint32) (capability.FetchOutput, error) {
		return capability.FetchOutput{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	toolGrants["demo.echo"] = capability.ToolGrant{
		ToolID: "demo.echo", HandlerVersion: "v1", MaxCalls: 99,
		EffectClass: "irreversible", Policy: "USER_APPROVAL_REQUIRED",
	}

	payload := []byte(`{"call_id":"call-1","capability":"demo.echo","catalog_digest":"` + catalogDigest + `","handler_version":"v1","arguments":{"text":"hello"}}`)
	responseBytes, err := broker.Call(context.Background(), payload)
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Status capability.Status `json:"status"`
		Result struct {
			Text string `json:"text"`
		} `json:"result"`
		Error *capability.Error `json:"error"`
	}
	if err := json.Unmarshal(responseBytes, &response); err != nil {
		t.Fatal(err)
	}
	if response.Status != capability.StatusOK || response.Result.Text != "hello" || response.Error != nil || calls != 1 || binder.begins != 1 || binder.completes != 1 {
		t.Fatalf("unexpected typed dispatch response=%s calls=%d binder=%+v", responseBytes, calls, binder)
	}
	receipts := broker.Receipts()
	if len(receipts) != 1 || receipts[0].TransactionID != "tx_bound" || receipts[0].OperationID != "op_bound" || receipts[0].AttemptID != "att_bound" ||
		receipts[0].EffectClass != "read_only" || receipts[0].Policy != "AUTO_COMMIT" {
		t.Fatalf("typed receipt is not frozen and transaction-bound: %+v", receipts)
	}

	responseBytes, err = broker.Call(context.Background(), payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(responseBytes, &response); err != nil {
		t.Fatal(err)
	}
	if response.Status != capability.StatusOK || response.Result.Text != "hello" || calls != 1 || binder.begins != 1 {
		t.Fatalf("idempotent replay response=%s calls=%d binder=%+v", responseBytes, calls, binder)
	}
	changed := []byte(`{"call_id":"call-1","capability":"demo.echo","catalog_digest":"` + catalogDigest + `","handler_version":"v1","arguments":{"text":"changed"}}`)
	responseBytes, err = broker.Call(context.Background(), changed)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(responseBytes, &response); err != nil || response.Error == nil || response.Error.Code != "duplicate_call_id" || calls != 1 {
		t.Fatalf("changed replay response=%s calls=%d err=%v", responseBytes, calls, err)
	}
	newCall := []byte(`{"call_id":"c2","capability":"demo.echo","catalog_digest":"` + catalogDigest + `","handler_version":"v1","arguments":{"text":"hello"}}`)
	responseBytes, err = broker.Call(context.Background(), newCall)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(responseBytes, &response); err != nil || response.Error == nil || response.Error.Code != "call_budget_exceeded" || calls != 1 {
		t.Fatalf("budget response=%s calls=%d err=%v", responseBytes, calls, err)
	}
}

func TestBrokerRejectsEffectfulTypedGrantUntilTransactionalBinderIsQualified(t *testing.T) {
	registry := capability.NewRegistry()
	if err := registry.Register(capability.HandlerSpec{
		ToolID: "demo.write", HandlerVersion: "v1",
		InputSchema: []byte(`{"type":"object"}`), OutputSchema: []byte(`{"type":"object"}`),
		Handler: capability.HandlerFunc(func(context.Context, capability.HostCall) (json.RawMessage, error) {
			return json.RawMessage(`{}`), nil
		}),
	}); err != nil {
		t.Fatal(err)
	}
	_, err := capability.NewBroker(capability.Config{
		RunIdentity: "run-registry", CatalogDigest: digestForTest("catalog"), Registry: registry, Binder: &recordingBinder{},
		ToolGrants: map[string]capability.ToolGrant{
			"demo.write": {ToolID: "demo.write", HandlerVersion: "v1", EffectClass: "irreversible", Policy: "USER_APPROVAL_REQUIRED", MaxCalls: 1},
		},
	}, capability.FetcherFunc(func(context.Context, capability.ResolvedRequest, uint32) (capability.FetchOutput, error) {
		return capability.FetchOutput{}, nil
	}))
	if err == nil {
		t.Fatal("effectful typed grant was accepted before in-tree transactional binder qualification")
	}
}

func TestBrokerRejectsTypedToolBindingAndSchemaFailuresBeforeHandler(t *testing.T) {
	registry := capability.NewRegistry()
	calls := 0
	binder := &recordingBinder{}
	if err := registry.Register(capability.HandlerSpec{
		ToolID: "demo.echo", HandlerVersion: "v1",
		InputSchema:  []byte(`{"type":"object","additionalProperties":false,"required":["text"],"properties":{"text":{"type":"string"}}}`),
		OutputSchema: []byte(`{"type":"object","additionalProperties":false,"required":["text"],"properties":{"text":{"type":"string"}}}`),
		Handler: capability.HandlerFunc(func(_ context.Context, call capability.HostCall) (json.RawMessage, error) {
			calls++
			return call.Arguments, nil
		}),
	}); err != nil {
		t.Fatal(err)
	}
	catalogDigest := digestForTest("catalog")
	broker, err := capability.NewBroker(capability.Config{
		RunIdentity: "run-registry", CatalogDigest: catalogDigest, Registry: registry, Binder: binder,
		ToolGrants: map[string]capability.ToolGrant{
			"demo.echo": {
				ToolID: "demo.echo", HandlerVersion: "v1", MaxCalls: 2,
				EffectClass: "read_only", Policy: "AUTO_COMMIT",
			},
		},
	}, capability.FetcherFunc(func(context.Context, capability.ResolvedRequest, uint32) (capability.FetchOutput, error) {
		return capability.FetchOutput{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		body string
		code string
	}{
		{name: "stale catalog", body: `{"call_id":"c1","capability":"demo.echo","catalog_digest":"` + digestForTest("stale") + `","handler_version":"v1","arguments":{"text":"ok"}}`, code: "stale_catalog"},
		{name: "handler drift", body: `{"call_id":"c2","capability":"demo.echo","catalog_digest":"` + catalogDigest + `","handler_version":"v2","arguments":{"text":"ok"}}`, code: "handler_version_mismatch"},
		{name: "schema mismatch", body: `{"call_id":"c3","capability":"demo.echo","catalog_digest":"` + catalogDigest + `","handler_version":"v1","arguments":{"text":"ok","authority":"forged"}}`, code: "invalid_arguments"},
		{name: "missing registry", body: `{"call_id":"c4","capability":"demo.missing","catalog_digest":"` + catalogDigest + `","handler_version":"v1","arguments":{"text":"ok"}}`, code: "capability_denied"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			responseBytes, callErr := broker.Call(context.Background(), []byte(test.body))
			if callErr != nil {
				t.Fatal(callErr)
			}
			var response struct {
				Status capability.Status `json:"status"`
				Error  *capability.Error `json:"error"`
			}
			if err := json.Unmarshal(responseBytes, &response); err != nil {
				t.Fatal(err)
			}
			if response.Status != capability.StatusDenied && response.Status != capability.StatusError {
				t.Fatalf("status=%q response=%s", response.Status, responseBytes)
			}
			if response.Error == nil || response.Error.Code != test.code {
				t.Fatalf("error=%+v want code=%q response=%s", response.Error, test.code, responseBytes)
			}
		})
	}
	if calls != 0 {
		t.Fatalf("rejected calls reached handler: %d", calls)
	}
}

type recordingBinder struct {
	begins    int
	completes int
}

func (binder *recordingBinder) Begin(_ context.Context, call capability.BoundCall) (capability.BoundOperation, error) {
	binder.begins++
	return capability.BoundOperation{
		TransactionID: "tx_bound", OperationID: "op_bound", AttemptID: "att_bound", OperationIndex: 1,
		ManifestDigest: digestForTest("bound-manifest"),
	}, nil
}

func (binder *recordingBinder) Complete(_ context.Context, operation capability.BoundOperation, outcome capability.BoundOutcome) error {
	binder.completes++
	return nil
}

func digestForTest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

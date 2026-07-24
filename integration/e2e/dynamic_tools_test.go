package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/toolcatalog"
	"github.com/bkmashiro/agent-python-runtime/runtime/transaction"
)

type dynamicToolIDs struct{ next int }

func (ids *dynamicToolIDs) New(prefix string) (string, error) {
	ids.next++
	return fmt.Sprintf("%s_e2e_%d", prefix, ids.next), nil
}

func TestGeneratedHostToolsExecuteThroughTrustedPrepareAndTransactionLedger(t *testing.T) {
	snapshot, err := toolcatalog.BuildSnapshot([]toolcatalog.DiscoveredTool{{
		ToolID: "demo.echo", ServerID: "fixture", Name: "echo", HandlerVersion: "v1",
		InputSchema:  json.RawMessage(`{"type":"object","additionalProperties":false,"required":["text"],"properties":{"text":{"type":"string"}}}`),
		OutputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["text"],"properties":{"text":{"type":"string"}}}`),
	}}, map[string]toolcatalog.Grant{"demo.echo": {ToolID: "demo.echo", EffectClass: "read_only", Policy: "AUTO_COMMIT", GrantVersion: "g1", MaxCalls: 1}}, toolcatalog.BuildOptions{Revision: 1})
	if err != nil {
		t.Fatal(err)
	}
	prepare, err := snapshot.GenerateTrustedPrepare()
	if err != nil {
		t.Fatal(err)
	}
	registry, grants, err := capability.BuildRegistryFromSnapshot(snapshot, map[string]capability.Handler{
		"demo.echo": capability.HandlerFunc(func(_ context.Context, call capability.HostCall) (json.RawMessage, error) {
			return append(json.RawMessage(nil), call.Arguments...), nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	ledger := transaction.NewMemoryLedger()
	coordinator := transaction.NewCoordinator(ledger, &dynamicToolIDs{}, func() time.Time { return time.Unix(2_000, 0).UTC() }, nil)
	var transactionID string
	factory := wazeroengine.Factory{BrokerFactory: func(context.Context) (*capability.Broker, error) {
		tx, err := coordinator.Begin(transaction.BeginRequest{RunID: "dynamic-sdk-e2e", CatalogDigest: snapshot.Digest(), Mode: transaction.TransactionModeDirect})
		if err != nil {
			return nil, err
		}
		transactionID = tx.ID
		binder, err := capability.NewCoordinatorBinder(coordinator, tx.ID, time.Minute)
		if err != nil {
			return nil, err
		}
		return capability.NewBroker(capability.Config{RunIdentity: "dynamic-sdk-e2e", CatalogDigest: snapshot.Digest(), Registry: registry, Binder: binder, ToolGrants: grants}, capability.FetcherFunc(func(context.Context, capability.ResolvedRequest, uint32) (capability.FetchOutput, error) {
			return capability.FetchOutput{}, nil
		}))
	}}
	response := runWithPrepare(t, newEngineWithFactory(t, runtimeconfig.DefaultRunConfig(), factory), "dynamic-sdk-e2e", "import inspect\nfrom host_tools import echo\nvalue = echo(text=inputs['text'])\nresult = {'value': value, 'signature': str(inspect.signature(echo))}", map[string]any{"text": "hello"}, prepare)
	result, _ := response.Result.(map[string]any)
	value, _ := result["value"].(map[string]any)
	signature, _ := result["signature"].(string)
	if response.Status != "ok" || value["text"] != "hello" || !strings.Contains(signature, "text: 'str'") || len(response.Receipts) != 1 {
		t.Fatalf("dynamic SDK response=%+v", response)
	}
	inspected, err := coordinator.InspectTransaction(transactionID)
	if err != nil || inspected.State != transaction.TransactionCommitted {
		t.Fatalf("transaction=%+v err=%v", inspected, err)
	}
}

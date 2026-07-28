package fakecloudflare_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability/adaptertest"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability/fakecloudflare"
	"github.com/bkmashiro/agent-python-runtime/runtime/transaction"
)

const catalogDigest = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"

type idSource struct{ next int }

func (source *idSource) New(prefix string) (string, error) {
	source.next++
	return fmt.Sprintf("%s_%d", prefix, source.next), nil
}

type fixture struct {
	broker   *capability.Broker
	provider *fakecloudflare.Provider
	resolver *capability.StaticSecretResolver
	audits   *[]capability.SecretAudit
	nextCall int
}

func newFixture(t *testing.T, resolverWriteToken []byte) fixture {
	t.Helper()
	now := time.Unix(700, 0).UTC()
	audits := []capability.SecretAudit{}
	resolver, err := capability.NewStaticSecretResolver(map[capability.SecretRef][]byte{
		"cloudflare.read":  []byte("read-token"),
		"cloudflare.write": append([]byte(nil), resolverWriteToken...),
	}, func() time.Time { return now }, func(audit capability.SecretAudit) { audits = append(audits, audit) })
	if err != nil {
		t.Fatal(err)
	}
	provider, err := fakecloudflare.NewProvider([]fakecloudflare.Record{
		{ID: "record:1", Name: "api.example.test", Type: "A", Content: "192.0.2.10", TTL: 300, Version: 1},
		{ID: "record:2", Name: "txt.example.test", Type: "TXT", Content: "before", TTL: 60, Version: 2},
	}, []byte("read-token"), []byte("write-token"))
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := fakecloudflare.NewAdapter(fakecloudflare.Config{Resolver: resolver, ReadSecretRef: "cloudflare.read", WriteSecretRef: "cloudflare.write", RunIdentity: "run:cf", TaskIdentity: "task:cf", Tenant: "tenant:cf", ZoneAlias: "zone:example", PolicyVersion: "cloudflare:v1", LeaseDuration: time.Minute, Provider: provider})
	if err != nil {
		t.Fatal(err)
	}
	specs, err := fakecloudflare.HandlerSpecs(adapter)
	if err != nil {
		t.Fatal(err)
	}
	registry := capability.NewRegistry()
	for _, spec := range specs {
		if err := registry.Register(spec); err != nil {
			t.Fatal(err)
		}
	}
	grants, err := fakecloudflare.ToolGrants("cloudflare:v1", 16)
	if err != nil {
		t.Fatal(err)
	}
	coordinator := transaction.NewCoordinator(transaction.NewMemoryLedger(), &idSource{}, func() time.Time { return now }, nil)
	tx, err := coordinator.Begin(transaction.BeginRequest{RunID: "run:cf", CatalogDigest: catalogDigest, Mode: transaction.TransactionModeWorkflow})
	if err != nil {
		t.Fatal(err)
	}
	binder, err := capability.NewCoordinatorBinder(coordinator, tx.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "run:cf", CatalogDigest: catalogDigest, Registry: registry, Binder: binder, ToolGrants: grants, MaxTransactionCalls: 64}, capability.FetcherFunc(func(context.Context, capability.ResolvedRequest, uint32) (capability.FetchOutput, error) {
		return capability.FetchOutput{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resolver.Close(); provider.Close() })
	return fixture{broker: broker, provider: provider, resolver: resolver, audits: &audits}
}

type response struct {
	Status string          `json:"status"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code string `json:"code"`
	} `json:"error"`
}

func (fixture *fixture) callWithID(t *testing.T, callID, toolID string, arguments any) ([]byte, response) {
	t.Helper()
	argumentBytes, err := json.Marshal(arguments)
	if err != nil {
		t.Fatal(err)
	}
	envelope := map[string]any{"call_id": callID, "capability": toolID, "catalog_digest": catalogDigest, "handler_version": fakecloudflare.HandlerVersion, "arguments": json.RawMessage(argumentBytes)}
	payload, _ := json.Marshal(envelope)
	raw, err := fixture.broker.Call(context.Background(), payload)
	if err != nil {
		t.Fatal(err)
	}
	var decoded response
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	return raw, decoded
}

func (fixture *fixture) call(t *testing.T, toolID string, arguments any) response {
	t.Helper()
	fixture.nextCall++
	_, decoded := fixture.callWithID(t, fmt.Sprintf("call:%d", fixture.nextCall), toolID, arguments)
	return decoded
}

func planChange(t *testing.T, fixture *fixture, arguments map[string]any) fakecloudflare.Plan {
	t.Helper()
	response := fixture.call(t, fakecloudflare.DNSPlanToolID, arguments)
	if response.Status != "ok" || response.Error != nil {
		t.Fatalf("plan response=%+v", response)
	}
	var plan fakecloudflare.Plan
	if err := json.Unmarshal(response.Result, &plan); err != nil {
		t.Fatal(err)
	}
	return plan
}

func applyPlan(t *testing.T, fixture *fixture, digest string) response {
	t.Helper()
	return fixture.call(t, fakecloudflare.DNSApplyToolID, map[string]any{"plan_digest": digest})
}

func TestFakeCloudflareListUpdateAndConditionalRollback(t *testing.T) {
	fixture := newFixture(t, []byte("write-token"))
	listed := fixture.call(t, fakecloudflare.DNSListToolID, map[string]any{})
	if listed.Status != "ok" || strings.Contains(string(listed.Result), "token") {
		t.Fatalf("list=%+v", listed)
	}
	plan := planChange(t, &fixture, map[string]any{"action": "update", "record_id": "record:1", "name": "api.example.test", "type": "A", "content": "192.0.2.20", "ttl": 120})
	if plan.Before == nil || plan.After == nil || plan.ExpectedVersion != 1 || plan.After.Content != "192.0.2.20" {
		t.Fatalf("plan=%+v", plan)
	}
	applied := applyPlan(t, &fixture, plan.Digest)
	if applied.Status != "ok" || applied.Error != nil {
		t.Fatalf("apply=%+v", applied)
	}
	if err := fixture.broker.RollbackCurrentTransaction(context.Background(), "guest_error"); err != nil {
		t.Fatal(err)
	}
	records := fixture.provider.Snapshot()
	if records[0].ID != "record:1" || records[0].Content != "192.0.2.10" || records[0].Version <= 1 {
		t.Fatalf("records=%+v", records)
	}
	inspection, err := fixture.broker.InspectTransaction()
	if err != nil || inspection.Transaction.State != transaction.TransactionRolledBack {
		t.Fatalf("inspection=%+v err=%v", inspection, err)
	}
	operations := []string{}
	for _, audit := range *fixture.audits {
		operations = append(operations, audit.Operation)
	}
	if strings.Join(operations, ",") != "read,read,apply,rollback" {
		t.Fatalf("audits=%+v", *fixture.audits)
	}
}

func TestFakeCloudflareCreateAndDeleteRollback(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		fixture := newFixture(t, []byte("write-token"))
		plan := planChange(t, &fixture, map[string]any{"action": "create", "name": "new.example.test", "type": "CNAME", "content": "api.example.test", "ttl": 300})
		if result := applyPlan(t, &fixture, plan.Digest); result.Status != "ok" {
			t.Fatalf("apply=%+v", result)
		}
		if len(fixture.provider.Snapshot()) != 3 {
			t.Fatal("create did not add record")
		}
		if err := fixture.broker.RollbackCurrentTransaction(context.Background(), "guest_error"); err != nil {
			t.Fatal(err)
		}
		if len(fixture.provider.Snapshot()) != 2 {
			t.Fatal("create rollback did not remove exact record")
		}
	})
	t.Run("delete", func(t *testing.T) {
		fixture := newFixture(t, []byte("write-token"))
		plan := planChange(t, &fixture, map[string]any{"action": "delete", "record_id": "record:2"})
		if result := applyPlan(t, &fixture, plan.Digest); result.Status != "ok" {
			t.Fatalf("apply=%+v", result)
		}
		if len(fixture.provider.Snapshot()) != 1 {
			t.Fatal("delete did not remove record")
		}
		if err := fixture.broker.RollbackCurrentTransaction(context.Background(), "guest_error"); err != nil {
			t.Fatal(err)
		}
		records := fixture.provider.Snapshot()
		if len(records) != 2 || records[1].ID != "record:2" || records[1].Content != "before" {
			t.Fatalf("delete rollback records=%+v", records)
		}
	})
}

func TestFakeCloudflareRollbackRejectsExternalDrift(t *testing.T) {
	fixture := newFixture(t, []byte("write-token"))
	plan := planChange(t, &fixture, map[string]any{"action": "update", "record_id": "record:1", "name": "api.example.test", "type": "A", "content": "192.0.2.20", "ttl": 120})
	if result := applyPlan(t, &fixture, plan.Digest); result.Status != "ok" {
		t.Fatalf("apply=%+v", result)
	}
	if err := fixture.provider.Drift("record:1", "192.0.2.99"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.broker.RollbackCurrentTransaction(context.Background(), "guest_error"); err == nil {
		t.Fatal("rollback overwrote external drift")
	}
	if records := fixture.provider.Snapshot(); records[0].Content != "192.0.2.99" {
		t.Fatalf("records=%+v", records)
	}
}

func TestFakeCloudflareAmbiguousApplyBlocksReplay(t *testing.T) {
	fixture := newFixture(t, []byte("write-token"))
	plan := planChange(t, &fixture, map[string]any{"action": "update", "record_id": "record:1", "name": "api.example.test", "type": "A", "content": "192.0.2.20", "ttl": 120})
	fixture.provider.SetAmbiguousNext()
	callID := "call:ambiguous"
	invoke := func(digest string) func() ([]byte, error) {
		return func() ([]byte, error) {
			raw, _ := fixture.callWithID(t, callID, fakecloudflare.DNSApplyToolID, map[string]any{"plan_digest": digest})
			return raw, nil
		}
	}
	adaptertest.AssertReplayConformance(t, adaptertest.ReplayCase{
		First: invoke(plan.Digest), Same: invoke(plan.Digest),
		Changed:                invoke("sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"),
		MutationCount:          func() uint64 { return fixture.provider.Snapshot()[0].Version },
		ExpectedFirstErrorCode: "reconciliation_required",
		SecretMarkers:          [][]byte{[]byte("read-token"), []byte("write-token")},
	})
	records := fixture.provider.Snapshot()
	if records[0].Content != "192.0.2.20" {
		t.Fatalf("records=%+v", records)
	}
	inspection, err := fixture.broker.InspectTransaction()
	if err != nil || inspection.Transaction.State != transaction.TransactionReconciliationRequired {
		t.Fatalf("inspection=%+v err=%v", inspection, err)
	}
}

func TestFakeCloudflareWriteCredentialFailsClosedWithoutSecretLeak(t *testing.T) {
	fixture := newFixture(t, []byte("wrong-token"))
	plan := planChange(t, &fixture, map[string]any{"action": "update", "record_id": "record:1", "name": "api.example.test", "type": "A", "content": "192.0.2.20", "ttl": 120})
	result := applyPlan(t, &fixture, plan.Digest)
	encoded, _ := json.Marshal(result)
	if result.Error == nil || result.Error.Code != "credential_denied" || strings.Contains(string(encoded), "token") {
		t.Fatalf("result=%s", encoded)
	}
	if records := fixture.provider.Snapshot(); records[0].Content != "192.0.2.10" {
		t.Fatalf("credential failure mutated provider: %+v", records)
	}
}

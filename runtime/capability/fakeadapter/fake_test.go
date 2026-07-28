package fakeadapter_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability/fakeadapter"
	"github.com/bkmashiro/agent-python-runtime/runtime/transaction"
)

type ids struct{ next int }

func (source *ids) New(prefix string) (string, error) {
	source.next++
	return fmt.Sprintf("%s_%d", prefix, source.next), nil
}

type fixture struct {
	broker   *capability.Broker
	provider *fakeadapter.Provider
	resolver *capability.StaticSecretResolver
	audits   *[]capability.SecretAudit
	payload  []byte
}

func newFixture(t *testing.T, effectClass string, autoCompensate bool) fixture {
	t.Helper()
	now := time.Unix(100, 0).UTC()
	audits := []capability.SecretAudit{}
	resolver, err := capability.NewStaticSecretResolver(map[capability.SecretRef][]byte{"provider.token": []byte("fixture-token")}, func() time.Time { return now }, func(a capability.SecretAudit) { audits = append(audits, a) })
	if err != nil {
		t.Fatal(err)
	}
	provider, err := fakeadapter.NewProvider("before", []byte("fixture-token"))
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := fakeadapter.NewChangeAdapter(fakeadapter.Config{Resolver: resolver, SecretRef: "provider.token", RunIdentity: "run:fake", TaskIdentity: "task:fake", Tenant: "tenant:fake", ResourceAlias: "resource:fake", PolicyVersion: "policy:v1", LeaseDuration: time.Minute, Provider: provider})
	if err != nil {
		t.Fatal(err)
	}
	registry := capability.NewRegistry()
	if err := registry.Register(capability.HandlerSpec{ToolID: "fake.change", HandlerVersion: "v1", Handler: adapter,
		InputSchema:  []byte(`{"type":"object","additionalProperties":false,"required":["value"],"properties":{"value":{"type":"string","minLength":1,"maxLength":4096}}}`),
		OutputSchema: []byte(`{"type":"object","additionalProperties":false,"required":["value","version"],"properties":{"value":{"type":"string"},"version":{"type":"integer","minimum":1}}}`)}); err != nil {
		t.Fatal(err)
	}
	catalog := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	coordinator := transaction.NewCoordinator(transaction.NewMemoryLedger(), &ids{}, func() time.Time { return now }, nil)
	tx, err := coordinator.Begin(transaction.BeginRequest{RunID: "run:fake", CatalogDigest: catalog, Mode: transaction.TransactionModeWorkflow})
	if err != nil {
		t.Fatal(err)
	}
	binder, err := capability.NewCoordinatorBinder(coordinator, tx.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	auto := map[string]bool{}
	if autoCompensate {
		auto["fake.change"] = true
	}
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "run:fake", CatalogDigest: catalog, Registry: registry, Binder: binder,
		ToolGrants: map[string]capability.ToolGrant{"fake.change": {ToolID: "fake.change", HandlerVersion: "v1", EffectClass: effectClass, Policy: "AUTO_COMMIT", PolicyVersion: "policy:v1", MaxCalls: 2}}, AutoCompensateTools: auto}, capability.FetcherFunc(func(context.Context, capability.ResolvedRequest, uint32) (capability.FetchOutput, error) {
		return capability.FetchOutput{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"call_id":"call:1","capability":"fake.change","catalog_digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","handler_version":"v1","arguments":{"value":"after"}}`)
	t.Cleanup(func() { resolver.Close(); provider.Close() })
	return fixture{broker: broker, provider: provider, resolver: resolver, audits: &audits, payload: payload}
}

func responseCode(t *testing.T, raw []byte) string {
	t.Helper()
	var value struct {
		Status string `json:"status"`
		Error  *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	if value.Error == nil {
		return ""
	}
	return value.Error.Code
}

func TestFakeAdapterReversibleRollbackAndDrift(t *testing.T) {
	t.Run("exact rollback", func(t *testing.T) {
		f := newFixture(t, "reversible", false)
		response, err := f.broker.Call(context.Background(), f.payload)
		if err != nil || responseCode(t, response) != "" {
			t.Fatalf("response=%s err=%v", response, err)
		}
		if err := f.broker.RollbackCurrentTransaction(context.Background(), "guest_error"); err != nil {
			t.Fatal(err)
		}
		record, compensations := f.provider.Snapshot()
		inspection, err := f.broker.InspectTransaction()
		if err != nil || record.Value != "before" || compensations != 0 || inspection.Transaction.State != transaction.TransactionRolledBack {
			t.Fatalf("record=%+v compensations=%d inspection=%+v err=%v", record, compensations, inspection, err)
		}
		if len(*f.audits) != 2 || (*f.audits)[0].Operation != "apply" || (*f.audits)[1].Operation != "rollback" {
			t.Fatalf("audits=%+v", *f.audits)
		}
	})
	t.Run("version drift fails closed", func(t *testing.T) {
		f := newFixture(t, "reversible", false)
		if _, err := f.broker.Call(context.Background(), f.payload); err != nil {
			t.Fatal(err)
		}
		f.provider.Drift("external")
		if err := f.broker.RollbackCurrentTransaction(context.Background(), "guest_error"); err == nil {
			t.Fatal("drift rollback succeeded")
		}
		record, _ := f.provider.Snapshot()
		if record.Value != "external" {
			t.Fatalf("rollback overwrote drift: %+v", record)
		}
	})
}

func TestFakeAdapterCompensationPreservesOriginalEffect(t *testing.T) {
	f := newFixture(t, "compensatable", true)
	if _, err := f.broker.Call(context.Background(), f.payload); err != nil {
		t.Fatal(err)
	}
	if err := f.broker.CompensateCurrentTransaction(context.Background(), "guest_error"); err != nil {
		t.Fatal(err)
	}
	record, compensations := f.provider.Snapshot()
	inspection, err := f.broker.InspectTransaction()
	if err != nil || record.Value != "after" || compensations != 1 || inspection.Transaction.State != transaction.TransactionCompensated {
		t.Fatalf("record=%+v compensations=%d inspection=%+v err=%v", record, compensations, inspection, err)
	}
}

func TestFakeAdapterAmbiguousCommitRequiresReconciliationAndBlocksReplay(t *testing.T) {
	f := newFixture(t, "reversible", false)
	f.provider.SetAmbiguousNext()
	response, err := f.broker.Call(context.Background(), f.payload)
	if err != nil || responseCode(t, response) != "reconciliation_required" {
		t.Fatalf("response=%s err=%v", response, err)
	}
	record, _ := f.provider.Snapshot()
	if record.Value != "after" || record.Version != 2 {
		t.Fatalf("provider did not commit before ambiguity: %+v", record)
	}
	inspection, err := f.broker.InspectTransaction()
	if err != nil || inspection.Transaction.State != transaction.TransactionReconciliationRequired {
		t.Fatalf("inspection=%+v err=%v", inspection, err)
	}
	replayed, err := f.broker.Call(context.Background(), f.payload)
	if err != nil || responseCode(t, replayed) != "reconciliation_required" {
		t.Fatalf("replay=%s err=%v", replayed, err)
	}
	record, _ = f.provider.Snapshot()
	if record.Version != 2 {
		t.Fatalf("ambiguous replay duplicated mutation: %+v", record)
	}
}

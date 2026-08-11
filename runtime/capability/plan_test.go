package capability_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

func TestSealedPlanIsOrderIndependentAndRejectsLateRegistration(t *testing.T) {
	first := capability.NewRegistry()
	mustRegister(t, first, "workspace.write_text", "pysolate.workspace.write-text.v1")
	mustRegister(t, first, "workspace.read_text", "pysolate.workspace.read-text.v1")
	firstPlan, err := first.Seal(capability.PlanConfig{MaxCalls: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Seal(capability.PlanConfig{MaxCalls: 2}); err != capability.ErrRegistrySealed {
		t.Fatalf("second seal error=%v", err)
	}
	if err := first.Register("git.status", "pysolate.git.status.v1", noopHandler); err != capability.ErrRegistrySealed {
		t.Fatalf("late registration error=%v", err)
	}

	second := capability.NewRegistry()
	mustRegister(t, second, "workspace.read_text", "pysolate.workspace.read-text.v1")
	mustRegister(t, second, "workspace.write_text", "pysolate.workspace.write-text.v1")
	secondPlan, err := second.Seal(capability.PlanConfig{MaxCalls: 2})
	if err != nil {
		t.Fatal(err)
	}
	if firstPlan.Identity() != secondPlan.Identity() {
		t.Fatalf("registration order changed identity: %q != %q", firstPlan.Identity(), secondPlan.Identity())
	}
	if got := firstPlan.Capabilities(); len(got) != 2 || got[0].Name != "workspace.read_text" || got[1].Name != "workspace.write_text" {
		t.Fatalf("capabilities are not canonical: %#v", got)
	}
	bindings := firstPlan.Capabilities()
	bindings[0].Name = "mutated"
	if firstPlan.Capabilities()[0].Name != "workspace.read_text" {
		t.Fatal("Plan.Capabilities leaked mutable plan state")
	}
}

func TestRegisterAndSealAreAtomic(t *testing.T) {
	for iteration := 0; iteration < 100; iteration++ {
		registry := capability.NewRegistry()
		start := make(chan struct{})
		var wait sync.WaitGroup
		wait.Add(2)
		var registerErr error
		var plan *capability.Plan
		var sealErr error
		go func() {
			defer wait.Done()
			<-start
			registerErr = registry.Register("git.status", "pysolate.git.status.v1", noopHandler)
		}()
		go func() {
			defer wait.Done()
			<-start
			plan, sealErr = registry.Seal(capability.PlanConfig{MaxCalls: 1})
		}()
		close(start)
		wait.Wait()
		if sealErr != nil {
			t.Fatalf("iteration %d seal error=%v", iteration, sealErr)
		}
		hasCapability := len(plan.Capabilities()) == 1
		if (registerErr == nil) != hasCapability {
			t.Fatalf("iteration %d register error=%v bindings=%#v", iteration, registerErr, plan.Capabilities())
		}
		if registerErr != nil && registerErr != capability.ErrRegistrySealed {
			t.Fatalf("iteration %d unexpected register error=%v", iteration, registerErr)
		}
	}
}

func TestPlanIdentityBindsHandlerIdentity(t *testing.T) {
	first := capability.NewRegistry()
	mustRegister(t, first, "git.status", "pysolate.git.status.v1")
	firstPlan, err := first.Seal(capability.PlanConfig{MaxCalls: 1})
	if err != nil {
		t.Fatal(err)
	}

	second := capability.NewRegistry()
	mustRegister(t, second, "git.status", "pysolate.git.status.v2")
	secondPlan, err := second.Seal(capability.PlanConfig{MaxCalls: 1})
	if err != nil {
		t.Fatal(err)
	}
	if firstPlan.Identity() == secondPlan.Identity() {
		t.Fatal("handler identity did not affect capability plan identity")
	}
}

func TestPlanIdentityBindsCallBudget(t *testing.T) {
	first := capability.NewRegistry()
	mustRegister(t, first, "git.status", "pysolate.git.status.v1")
	firstPlan, err := first.Seal(capability.PlanConfig{MaxCalls: 1})
	if err != nil {
		t.Fatal(err)
	}
	second := capability.NewRegistry()
	mustRegister(t, second, "git.status", "pysolate.git.status.v1")
	secondPlan, err := second.Seal(capability.PlanConfig{MaxCalls: 2})
	if err != nil {
		t.Fatal(err)
	}
	if firstPlan.Identity() == secondPlan.Identity() {
		t.Fatal("call budget did not affect capability plan identity")
	}
}

func TestBrokerUsesOnlyASealedPlanAndBindsReceipts(t *testing.T) {
	registry := capability.NewRegistry()
	mustRegister(t, registry, "workspace.read_text", "pysolate.workspace.read-text.v1")
	if _, err := capability.NewBroker(capability.Config{RunIdentity: "host-run"}); err == nil {
		t.Fatal("broker accepted a missing sealed plan")
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 1})
	if err != nil {
		t.Fatal(err)
	}
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "host-run", Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	if broker.CapabilityPlanSHA256() != plan.Identity() {
		t.Fatalf("broker plan identity=%q want %q", broker.CapabilityPlanSHA256(), plan.Identity())
	}
	if _, err := broker.Call(context.Background(), []byte(`{"call_id":"one","capability":"workspace.read_text","arguments":{}}`)); err != nil {
		t.Fatal(err)
	}
	receipts := broker.SnapshotReceipts()
	if len(receipts) != 1 || receipts[0].CapabilityPlanSHA256 != plan.Identity() {
		t.Fatalf("receipt did not bind capability plan: %#v", receipts)
	}
}

var noopHandler = capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
})

func mustRegister(t *testing.T, registry *capability.Registry, name, handlerIdentity string) {
	t.Helper()
	if err := registry.Register(name, handlerIdentity, noopHandler); err != nil {
		t.Fatal(err)
	}
}

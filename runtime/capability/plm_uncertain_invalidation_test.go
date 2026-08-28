package capability_test

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

func TestPLMInvalidatedUncertainProviderOutcomeIsNeverReplayed(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var physical atomic.Uint32
	adapter := &plmVerticalAdapter{
		handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			if physical.Add(1) == 1 {
				close(started)
				<-release
				return nil, uncertainProviderError{}
			}
			return json.RawMessage(`{"text":"canonical"}`), nil
		},
		temporal:      capability.TemporalEvidence{Mode: capability.TemporalVersioned, Version: "v2"},
		temporalValid: true,
		providerValid: true,
	}
	plan, contract := plmVerticalPlan(t, capability.TemporalVersioned, adapter)
	broker, _ := capability.NewBroker(capability.Config{RunIdentity: "uncertain-invalid", Plan: plan})
	table, _ := capability.NewSplitPhaseTable(broker, ownerLimits())
	request := []byte(`{"call_id":"uncertain-invalid-call","capability":"workspace.read_text","arguments":{"path":"a.txt"}}`)
	certificate := plmVerticalCertificate(broker, plan, "a.txt", "", capability.TemporalVersioned)
	certificate.Temporal.Version = "v1"
	adapter.temporal.ResourceIdentity = certificate.Temporal.ResourceIdentity
	if err := table.PrepareOrReuse(context.Background(), "slot-uncertain-invalid", request, contract, certificate); err != nil {
		t.Fatal(err)
	}
	<-started
	type result struct {
		response []byte
		err      error
	}
	resultCh := make(chan result, 1)
	go func() {
		response, err := table.LinearizeAndMaterialize(context.Background(), "slot-uncertain-invalid", plmVerticalLogical(certificate, "a.txt"))
		resultCh <- result{response: response, err: err}
	}()
	select {
	case got := <-resultCh:
		t.Fatalf("canonical replay started before uncertain attempt settled: %+v", got)
	case <-time.After(10 * time.Millisecond):
	}
	close(release)
	got := <-resultCh
	if got.err != nil || physical.Load() != 1 || !containsCode(got.response, capability.PLMProviderOutcomeUncertainCode) {
		t.Fatalf("physical=%d response=%s snapshot=%+v err=%v", physical.Load(), got.response, table.Snapshot(), got.err)
	}
	if snapshot := table.Snapshot(); snapshot.CanonicalStarts != 0 || snapshot.Consumed != 1 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

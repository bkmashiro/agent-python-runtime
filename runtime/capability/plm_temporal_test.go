package capability_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

func TestPLMTemporalModesAdoptOnlyUnderSoundEvidence(t *testing.T) {
	tests := []struct {
		name      string
		mode      capability.TemporalMode
		candidate capability.TemporalEvidence
		current   capability.TemporalEvidence
		adopt     bool
	}{
		{"snapshot valid", capability.TemporalSnapshot,
			capability.TemporalEvidence{Mode: capability.TemporalSnapshot, ResourceIdentity: "db:orders", SnapshotIdentity: "snapshot-7"},
			capability.TemporalEvidence{Mode: capability.TemporalSnapshot, ResourceIdentity: "db:orders", SnapshotIdentity: "snapshot-7"}, true},
		{"snapshot changed", capability.TemporalSnapshot,
			capability.TemporalEvidence{Mode: capability.TemporalSnapshot, ResourceIdentity: "db:orders", SnapshotIdentity: "snapshot-7"},
			capability.TemporalEvidence{Mode: capability.TemporalSnapshot, ResourceIdentity: "db:orders", SnapshotIdentity: "snapshot-8"}, false},
		{"version valid", capability.TemporalVersioned,
			capability.TemporalEvidence{Mode: capability.TemporalVersioned, ResourceIdentity: "market:AAPL", Version: "seq-42"},
			capability.TemporalEvidence{Mode: capability.TemporalVersioned, ResourceIdentity: "market:AAPL", Version: "seq-42"}, true},
		{"version changed", capability.TemporalVersioned,
			capability.TemporalEvidence{Mode: capability.TemporalVersioned, ResourceIdentity: "market:AAPL", Version: "seq-42"},
			capability.TemporalEvidence{Mode: capability.TemporalVersioned, ResourceIdentity: "market:AAPL", Version: "seq-43"}, false},
		{"lease valid", capability.TemporalLeased,
			capability.TemporalEvidence{Mode: capability.TemporalLeased, ResourceIdentity: "tax:UK", ClockEpoch: "clock-1", ValidUntilTick: 100},
			capability.TemporalEvidence{Mode: capability.TemporalLeased, ResourceIdentity: "tax:UK", ClockEpoch: "clock-1", NowTick: 100}, true},
		{"lease expired", capability.TemporalLeased,
			capability.TemporalEvidence{Mode: capability.TemporalLeased, ResourceIdentity: "tax:UK", ClockEpoch: "clock-1", ValidUntilTick: 100},
			capability.TemporalEvidence{Mode: capability.TemporalLeased, ResourceIdentity: "tax:UK", ClockEpoch: "clock-1", NowTick: 101}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var physical atomic.Uint32
			adapter := &plmVerticalAdapter{handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
				attempt := physical.Add(1)
				return json.RawMessage(fmt.Sprintf(`{"text":"attempt-%d"}`, attempt)), nil
			}, temporal: test.current, temporalValid: true, providerValid: true}
			plan, contract := plmVerticalPlan(t, test.mode, adapter)
			broker, _ := capability.NewBroker(capability.Config{RunIdentity: "temporal-run", Plan: plan})
			table, _ := capability.NewSplitPhaseTable(broker, ownerLimits())
			request := []byte(`{"call_id":"temporal-call","capability":"workspace.read_text","arguments":{"path":"a.txt"}}`)
			certificate := plmVerticalCertificate(broker, plan, "a.txt", test.candidate.ResourceIdentity, test.mode)
			resourceIdentity := certificate.Temporal.ResourceIdentity
			certificate.Temporal = test.candidate
			certificate.Temporal.ResourceIdentity = resourceIdentity
			adapter.temporal.ResourceIdentity = resourceIdentity
			if err := table.PrepareOrReuse(context.Background(), "slot-temporal", request, contract, certificate); err != nil {
				t.Fatal(err)
			}
			response, err := table.LinearizeAndMaterialize(context.Background(), "slot-temporal", plmVerticalLogical(certificate, "a.txt"))
			if err != nil {
				t.Fatal(err)
			}
			if test.adopt {
				if physical.Load() != 1 || !containsResult(response, "attempt-1") {
					t.Fatalf("physical=%d response=%s", physical.Load(), response)
				}
			} else if physical.Load() == 0 || physical.Load() > 2 {
				t.Fatalf("physical=%d response=%s", physical.Load(), response)
			}
			if err := broker.Finalize(true); err != nil {
				t.Fatal(err)
			}
			snapshot := table.Snapshot()
			if snapshot.Validations != 1 || snapshot.ValidationCostUnits != 1 || snapshot.ProviderValidationPhysicalEvents != 1 {
				t.Fatalf("validation evidence=%+v", snapshot)
			}
			if test.adopt && (snapshot.CandidatesAdopted != 1 || snapshot.CanonicalStarts != 0) {
				t.Fatalf("adopt snapshot=%+v", snapshot)
			}
			if !test.adopt && (snapshot.CandidatesRejected != 1 || snapshot.CanonicalStarts != 1) {
				t.Fatalf("restart snapshot=%+v", snapshot)
			}
		})
	}
}

func TestPLMCurrentPreparesTransportButReadsFinalValueOnlyAtLinearization(t *testing.T) {
	var finalReads atomic.Uint32
	transportReady := make(chan struct{})
	adapter := &plmVerticalAdapter{handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
		finalReads.Add(1)
		return json.RawMessage(`{"text":"current-at-L"}`), nil
	}, temporal: capability.TemporalEvidence{Mode: capability.TemporalCurrent, ResourceIdentity: "market:AAPL"}, temporalValid: true, providerValid: true, transportReady: transportReady}
	plan, contract := plmVerticalPlan(t, capability.TemporalCurrent, adapter)
	broker, _ := capability.NewBroker(capability.Config{RunIdentity: "current-mode", Plan: plan})
	table, _ := capability.NewSplitPhaseTable(broker, ownerLimits())
	request := []byte(`{"call_id":"current-call","capability":"workspace.read_text","arguments":{"path":"a.txt"}}`)
	certificate := plmVerticalCertificate(broker, plan, "a.txt", "market:AAPL", capability.TemporalCurrent)
	adapter.temporal.ResourceIdentity = certificate.Temporal.ResourceIdentity
	if err := table.PrepareOrReuse(context.Background(), "slot-current", request, contract, certificate); err != nil {
		t.Fatal(err)
	}
	<-transportReady
	if adapter.transportRuns.Load() != 1 || finalReads.Load() != 0 || broker.Calls() != 0 {
		t.Fatalf("transport=%d reads=%d logical=%d", adapter.transportRuns.Load(), finalReads.Load(), broker.Calls())
	}
	response, err := table.LinearizeAndMaterialize(context.Background(), "slot-current", plmVerticalLogical(certificate, "a.txt"))
	if err != nil || !containsResult(response, "current-at-L") {
		t.Fatalf("response=%s err=%v", response, err)
	}
	if finalReads.Load() != 1 || broker.Calls() != 1 {
		t.Fatalf("reads=%d logical=%d", finalReads.Load(), broker.Calls())
	}
	if err := broker.Finalize(true); err != nil {
		t.Fatal(err)
	}
	if snapshot := table.Snapshot(); snapshot.CandidatesAdopted != 0 || snapshot.CanonicalStarts != 1 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

func TestPLMWallclockObservingRejectsPreparationAndRunsSequentially(t *testing.T) {
	var calls atomic.Uint32
	adapter := &plmVerticalAdapter{handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
		calls.Add(1)
		return json.RawMessage(`{"text":"sequential"}`), nil
	}}
	plan, contract := plmVerticalPlan(t, capability.TemporalWallclockObserving, adapter)
	broker, _ := capability.NewBroker(capability.Config{RunIdentity: "wallclock-mode", Plan: plan})
	table, _ := capability.NewSplitPhaseTable(broker, ownerLimits())
	request := []byte(`{"call_id":"wallclock-call","capability":"workspace.read_text","arguments":{"path":"a.txt"}}`)
	certificate := plmVerticalCertificate(broker, plan, "a.txt", "clock", capability.TemporalWallclockObserving)
	if err := table.PrepareOrReuse(context.Background(), "slot-wallclock", request, contract, certificate); err == nil {
		t.Fatal("wallclock operation prepared")
	}
	response, err := broker.Call(context.Background(), request)
	if err != nil || !containsResult(response, "sequential") || calls.Load() != 1 {
		t.Fatalf("response=%s calls=%d err=%v", response, calls.Load(), err)
	}
}

func TestPLMStableFailureRequiresValidatorProof(t *testing.T) {
	for _, stable := range []bool{true, false} {
		t.Run(fmt.Sprintf("validated=%t", stable), func(t *testing.T) {
			var physical atomic.Uint32
			preparedFailed := make(chan struct{})
			adapter := &plmVerticalAdapter{handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
				if physical.Add(1) == 1 {
					close(preparedFailed)
					return nil, fmt.Errorf("stable prepared failure")
				}
				return json.RawMessage(`{"text":"canonical"}`), nil
			}, temporal: capability.TemporalEvidence{Mode: capability.TemporalImmutable, ResourceIdentity: "immutable:a"}, temporalValid: true, providerValid: true,
				stableValidator: true, stableValid: stable}
			plan, contract := plmVerticalPlan(t, capability.TemporalImmutable, adapter)
			broker, _ := capability.NewBroker(capability.Config{RunIdentity: fmt.Sprintf("stable-%t", stable), Plan: plan})
			table, _ := capability.NewSplitPhaseTable(broker, ownerLimits())
			request := []byte(`{"call_id":"stable-call","capability":"workspace.read_text","arguments":{"path":"a.txt"}}`)
			certificate := plmVerticalCertificate(broker, plan, "a.txt", "immutable:a", capability.TemporalImmutable)
			adapter.temporal.ResourceIdentity = certificate.Temporal.ResourceIdentity
			if err := table.PrepareOrReuse(context.Background(), "slot-stable", request, contract, certificate); err != nil {
				t.Fatal(err)
			}
			<-preparedFailed
			response, err := table.LinearizeAndMaterialize(context.Background(), "slot-stable", plmVerticalLogical(certificate, "a.txt"))
			if err != nil {
				t.Fatal(err)
			}
			if stable {
				if physical.Load() != 1 || !containsCode(response, "handler_error") {
					t.Fatalf("physical=%d response=%s", physical.Load(), response)
				}
			} else if physical.Load() != 2 || !containsResult(response, "canonical") {
				t.Fatalf("physical=%d response=%s", physical.Load(), response)
			}
			if err := broker.Finalize(true); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPLMPrepareRejectsResourceAndSealedContractDrift(t *testing.T) {
	var physical atomic.Uint32
	adapter := &plmVerticalAdapter{handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
		physical.Add(1)
		return json.RawMessage(`{"text":"value"}`), nil
	}, temporal: capability.TemporalEvidence{Mode: capability.TemporalImmutable}, temporalValid: true, providerValid: true}
	plan, contract := plmVerticalPlan(t, capability.TemporalImmutable, adapter)
	broker, _ := capability.NewBroker(capability.Config{RunIdentity: "prepare-drift", Plan: plan})
	table, _ := capability.NewSplitPhaseTable(broker, ownerLimits())
	request := []byte(`{"call_id":"prepare-drift-call","capability":"workspace.read_text","arguments":{"path":"a.txt"}}`)
	certificate := plmVerticalCertificate(broker, plan, "a.txt", "ignored", capability.TemporalImmutable)
	wrongResource := certificate
	wrongResource.Temporal.ResourceIdentity = "workspace:sha256:wrong"
	if err := table.PrepareOrReuse(context.Background(), "slot-wrong-resource", request, contract, wrongResource); err == nil {
		t.Fatal("wrong resource identity prepared")
	}
	drifted := contract
	drifted.CostUnits++
	if err := table.PrepareOrReuse(context.Background(), "slot-drifted-contract", request, drifted, certificate); err == nil {
		t.Fatal("unsealed contract prepared")
	}
	if physical.Load() != 0 || broker.Calls() != 0 {
		t.Fatalf("physical=%d logical=%d", physical.Load(), broker.Calls())
	}
}

func TestPLMLinearizationRejectsChangedHostContextAndProviderQuota(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*capability.PLMLogicalContext, *plmVerticalAdapter)
	}{
		{"authority", func(logical *capability.PLMLogicalContext, _ *plmVerticalAdapter) {
			logical.AuthorityEpoch = "authority-revoked"
		}},
		{"provider session", func(logical *capability.PLMLogicalContext, _ *plmVerticalAdapter) {
			logical.ProviderSessionIdentity = "provider-new"
		}},
		{"source seal", func(logical *capability.PLMLogicalContext, _ *plmVerticalAdapter) {
			logical.SourceSealIdentity = "seal-new"
		}},
		{"actual arguments", func(logical *capability.PLMLogicalContext, _ *plmVerticalAdapter) {
			logical.ActualArguments = json.RawMessage(`{"path":"b.txt"}`)
		}},
		{"provider quota", func(_ *capability.PLMLogicalContext, adapter *plmVerticalAdapter) { adapter.providerValid = false }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var physical atomic.Uint32
			adapter := &plmVerticalAdapter{handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
				attempt := physical.Add(1)
				return json.RawMessage(fmt.Sprintf(`{"text":"attempt-%d"}`, attempt)), nil
			}, temporal: capability.TemporalEvidence{Mode: capability.TemporalImmutable, ResourceIdentity: "immutable:a"}, temporalValid: true, providerValid: true}
			plan, contract := plmVerticalPlan(t, capability.TemporalImmutable, adapter)
			broker, _ := capability.NewBroker(capability.Config{RunIdentity: "context-run", Plan: plan})
			table, _ := capability.NewSplitPhaseTable(broker, ownerLimits())
			request := []byte(`{"call_id":"context-call","capability":"workspace.read_text","arguments":{"path":"a.txt"}}`)
			certificate := plmVerticalCertificate(broker, plan, "a.txt", "immutable:a", capability.TemporalImmutable)
			adapter.temporal.ResourceIdentity = certificate.Temporal.ResourceIdentity
			if err := table.PrepareOrReuse(context.Background(), "slot-context", request, contract, certificate); err != nil {
				t.Fatal(err)
			}
			logical := plmVerticalLogical(certificate, "a.txt")
			test.mutate(&logical, adapter)
			response, err := table.LinearizeAndMaterialize(context.Background(), "slot-context", logical)
			if err != nil || physical.Load() == 0 || physical.Load() > 2 {
				t.Fatalf("physical=%d response=%s err=%v", physical.Load(), response, err)
			}
			if err := broker.Finalize(true); err != nil {
				t.Fatal(err)
			}
			if snapshot := table.Snapshot(); snapshot.CandidatesRejected != 1 || snapshot.CanonicalStarts != 1 {
				t.Fatalf("snapshot=%+v", snapshot)
			}
		})
	}
}

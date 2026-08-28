package capability_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

type uncertainProviderError struct{}

func (uncertainProviderError) Error() string                  { return "provider outcome unknown" }
func (uncertainProviderError) ProviderOutcomeUncertain() bool { return true }

func TestPLMUncertainProviderOutcomeIsNeverReplayed(t *testing.T) {
	var classified capability.PLMProviderOutcomeUncertain
	if !errors.As(error(uncertainProviderError{}), &classified) {
		t.Fatal("test uncertainty does not satisfy the runtime interface")
	}
	var physical atomic.Uint32
	finished := make(chan struct{})
	var finishedOnce sync.Once
	adapter := &plmVerticalAdapter{handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
		physical.Add(1)
		finishedOnce.Do(func() { close(finished) })
		return nil, uncertainProviderError{}
	}, temporal: capability.TemporalEvidence{Mode: capability.TemporalImmutable}, temporalValid: true, providerValid: true}
	plan, contract := plmVerticalPlan(t, capability.TemporalImmutable, adapter)
	broker, _ := capability.NewBroker(capability.Config{RunIdentity: "uncertain-outcome", Plan: plan})
	table, _ := capability.NewSplitPhaseTable(broker, ownerLimits())
	request := []byte(`{"call_id":"uncertain-call","capability":"workspace.read_text","arguments":{"path":"a.txt"}}`)
	certificate := plmVerticalCertificate(broker, plan, "a.txt", "", capability.TemporalImmutable)
	adapter.temporal.ResourceIdentity = certificate.Temporal.ResourceIdentity
	if err := table.PrepareOrReuse(context.Background(), "slot-uncertain", request, contract, certificate); err != nil {
		t.Fatal(err)
	}
	<-finished
	response, err := table.LinearizeAndMaterialize(context.Background(), "slot-uncertain", plmVerticalLogical(certificate, "a.txt"))
	if err != nil || physical.Load() != 1 || broker.CallCount() != 1 || !containsCode(response, capability.PLMProviderOutcomeUncertainCode) {
		t.Fatalf("physical=%d calls=%d response=%s snapshot=%+v err=%v", physical.Load(), broker.CallCount(), response, table.Snapshot(), err)
	}
	if err := broker.Finalize(false); err != nil {
		t.Fatal(err)
	}
	if snapshot := table.Snapshot(); snapshot.CanonicalStarts != 0 || snapshot.Consumed != 1 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

func TestPLMImmutableCandidateMayCompleteAfterLinearizationStarts(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	adapter := &plmVerticalAdapter{handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
		close(started)
		<-release
		return json.RawMessage(`{"text":"late-ready"}`), nil
	}, temporal: capability.TemporalEvidence{Mode: capability.TemporalImmutable}, temporalValid: true, providerValid: true}
	plan, contract := plmVerticalPlan(t, capability.TemporalImmutable, adapter)
	broker, _ := capability.NewBroker(capability.Config{RunIdentity: "late-ready", Plan: plan})
	table, _ := capability.NewSplitPhaseTable(broker, ownerLimits())
	request := []byte(`{"call_id":"late-ready-call","capability":"workspace.read_text","arguments":{"path":"a.txt"}}`)
	certificate := plmVerticalCertificate(broker, plan, "a.txt", "", capability.TemporalImmutable)
	adapter.temporal.ResourceIdentity = certificate.Temporal.ResourceIdentity
	if err := table.PrepareOrReuse(context.Background(), "slot-late-ready", request, contract, certificate); err != nil {
		t.Fatal(err)
	}
	<-started
	type result struct {
		response []byte
		err      error
	}
	resultCh := make(chan result, 1)
	go func() {
		response, err := table.LinearizeAndMaterialize(context.Background(), "slot-late-ready", plmVerticalLogical(certificate, "a.txt"))
		resultCh <- result{response: response, err: err}
	}()
	select {
	case got := <-resultCh:
		t.Fatalf("materialized before physical completion: %+v", got)
	case <-time.After(10 * time.Millisecond):
	}
	close(release)
	got := <-resultCh
	if got.err != nil || !containsResult(got.response, "late-ready") {
		t.Fatalf("response=%s err=%v", got.response, got.err)
	}
	if snapshot := table.Snapshot(); snapshot.CandidatesAdopted != 1 || snapshot.MaterializationNanos == 0 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

func TestPLMVersionChangeAfterValidationDoesNotMoveTheLogicalPoint(t *testing.T) {
	physicalStarted := make(chan struct{})
	releasePhysical := make(chan struct{})
	validated := make(chan struct{})
	var once sync.Once
	adapter := &plmVerticalAdapter{handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
		close(physicalStarted)
		<-releasePhysical
		return json.RawMessage(`{"text":"version-at-L"}`), nil
	}, temporal: capability.TemporalEvidence{Mode: capability.TemporalVersioned, Version: "v1"}, temporalValid: true, providerValid: true}
	adapter.validate = func(context.Context, capability.PLMValidationRequest) (capability.PLMValidationResult, error) {
		result := capability.PLMValidationResult{Temporal: adapter.temporal, TemporalValid: true, ProviderNonInterferenceValid: true, ValidationCostUnits: 1}
		once.Do(func() { close(validated) })
		return result, nil
	}
	plan, contract := plmVerticalPlan(t, capability.TemporalVersioned, adapter)
	broker, _ := capability.NewBroker(capability.Config{RunIdentity: "version-after-L", Plan: plan})
	table, _ := capability.NewSplitPhaseTable(broker, ownerLimits())
	request := []byte(`{"call_id":"version-after-L-call","capability":"workspace.read_text","arguments":{"path":"a.txt"}}`)
	certificate := plmVerticalCertificate(broker, plan, "a.txt", "", capability.TemporalVersioned)
	certificate.Temporal.Version = "v1"
	adapter.temporal.ResourceIdentity = certificate.Temporal.ResourceIdentity
	if err := table.PrepareOrReuse(context.Background(), "slot-version-after-L", request, contract, certificate); err != nil {
		t.Fatal(err)
	}
	<-physicalStarted
	resultCh := make(chan []byte, 1)
	go func() {
		response, _ := table.LinearizeAndMaterialize(context.Background(), "slot-version-after-L", plmVerticalLogical(certificate, "a.txt"))
		resultCh <- response
	}()
	<-validated
	adapter.temporal.Version = "v2"
	close(releasePhysical)
	response := <-resultCh
	if !containsResult(response, "version-at-L") {
		t.Fatalf("response=%s", response)
	}
	if snapshot := table.Snapshot(); snapshot.CandidatesAdopted != 1 || snapshot.CanonicalStarts != 0 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

func TestPLMCurrentReadsRemainOrderedAndStartOnlyAtTheirLogicalPoints(t *testing.T) {
	var reads atomic.Uint32
	adapter := &plmVerticalAdapter{handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
		read := reads.Add(1)
		return json.RawMessage(fmt.Sprintf(`{"text":"current-%d"}`, read)), nil
	}, temporal: capability.TemporalEvidence{Mode: capability.TemporalCurrent}, temporalValid: true, providerValid: true, maxCalls: 2}
	plan, contract := plmVerticalPlan(t, capability.TemporalCurrent, adapter)
	broker, _ := capability.NewBroker(capability.Config{RunIdentity: "current-order", Plan: plan})
	table, _ := capability.NewSplitPhaseTable(broker, capability.SplitPhaseLimits{MaxCalls: 2, MaxCostUnits: 2, MaxResultBytes: 2 << 20})
	certificates := []capability.CandidateCertificate{
		plmVerticalCertificate(broker, plan, "a.txt", "", capability.TemporalCurrent),
		plmVerticalCertificate(broker, plan, "a.txt", "", capability.TemporalCurrent),
	}
	certificates[1].Binding.Occurrence = 2
	for index := range certificates {
		adapter.temporal.ResourceIdentity = certificates[index].Temporal.ResourceIdentity
		request := []byte(fmt.Sprintf(`{"call_id":"current-%d","capability":"workspace.read_text","arguments":{"path":"a.txt"}}`, index+1))
		if err := table.PrepareOrReuse(context.Background(), fmt.Sprintf("slot-current-%d", index+1), request, contract, certificates[index]); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(100 * time.Millisecond)
	for adapter.transportRuns.Load() != 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if reads.Load() != 0 || adapter.transportRuns.Load() != 2 {
		t.Fatalf("reads=%d transports=%d", reads.Load(), adapter.transportRuns.Load())
	}
	for index := range certificates {
		response, err := table.LinearizeAndMaterialize(context.Background(), fmt.Sprintf("slot-current-%d", index+1), plmVerticalLogical(certificates[index], "a.txt"))
		if err != nil || !containsResult(response, fmt.Sprintf("current-%d", index+1)) {
			t.Fatalf("index=%d response=%s err=%v", index, response, err)
		}
	}
	receipts := broker.Receipts()
	if len(receipts) != 2 || receipts[0].CallID != "current-1" || receipts[1].CallID != "current-2" {
		t.Fatalf("receipts=%+v", receipts)
	}
}

func TestPLMLeaseRevocationAndClockEpochMismatchRestartCanonically(t *testing.T) {
	for _, test := range []struct {
		name          string
		runID         string
		current       capability.TemporalEvidence
		temporalValid bool
	}{
		{name: "revoked", runID: "lease-revoked", current: capability.TemporalEvidence{Mode: capability.TemporalLeased, ClockEpoch: "clock-1", NowTick: 1}, temporalValid: false},
		{name: "clock epoch mismatch", runID: "lease-clock-mismatch", current: capability.TemporalEvidence{Mode: capability.TemporalLeased, ClockEpoch: "clock-2", NowTick: 1}, temporalValid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var physical atomic.Uint32
			adapter := &plmVerticalAdapter{handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
				attempt := physical.Add(1)
				return json.RawMessage(fmt.Sprintf(`{"text":"attempt-%d"}`, attempt)), nil
			}, temporal: test.current, temporalValid: test.temporalValid, providerValid: true}
			plan, contract := plmVerticalPlan(t, capability.TemporalLeased, adapter)
			broker, _ := capability.NewBroker(capability.Config{RunIdentity: test.runID, Plan: plan})
			table, _ := capability.NewSplitPhaseTable(broker, ownerLimits())
			request := []byte(`{"call_id":"lease-call","capability":"workspace.read_text","arguments":{"path":"a.txt"}}`)
			certificate := plmVerticalCertificate(broker, plan, "a.txt", "", capability.TemporalLeased)
			certificate.Temporal.ClockEpoch = "clock-1"
			certificate.Temporal.ValidUntilTick = 10
			adapter.temporal.ResourceIdentity = certificate.Temporal.ResourceIdentity
			if err := table.PrepareOrReuse(context.Background(), "slot-lease", request, contract, certificate); err != nil {
				t.Fatal(err)
			}
			if _, err := table.LinearizeAndMaterialize(context.Background(), "slot-lease", plmVerticalLogical(certificate, "a.txt")); err != nil {
				t.Fatal(err)
			}
			if snapshot := table.Snapshot(); snapshot.CandidatesRejected != 1 || snapshot.CanonicalStarts != 1 {
				t.Fatalf("snapshot=%+v", snapshot)
			}
		})
	}
}

func TestPLMSeededTwoCallProgramsRefineSequentialVisibleOrder(t *testing.T) {
	for seed := int64(0); seed < 16; seed++ {
		t.Run(fmt.Sprintf("seed-%02d", seed), func(t *testing.T) {
			random := rand.New(rand.NewSource(seed))
			paths := []string{"a.txt", "b.txt"}
			gates := map[string]chan struct{}{"a.txt": make(chan struct{}), "b.txt": make(chan struct{})}
			started := map[string]chan struct{}{"a.txt": make(chan struct{}), "b.txt": make(chan struct{})}
			attempts := map[string]int{}
			resources := map[string]string{}
			invalid := map[string]bool{"a.txt": random.Intn(2) == 1, "b.txt": random.Intn(2) == 1}
			var mu sync.Mutex
			adapter := &plmVerticalAdapter{maxCalls: 2, temporal: capability.TemporalEvidence{Mode: capability.TemporalVersioned}, temporalValid: true, providerValid: true}
			adapter.handler = func(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
				var arguments struct {
					Path string `json:"path"`
				}
				if err := json.Unmarshal(raw, &arguments); err != nil {
					return nil, err
				}
				mu.Lock()
				attempts[arguments.Path]++
				attempt := attempts[arguments.Path]
				mu.Unlock()
				if attempt == 1 {
					close(started[arguments.Path])
					<-gates[arguments.Path]
					return json.RawMessage(fmt.Sprintf(`{"text":%q}`, arguments.Path+"-candidate")), nil
				}
				return json.RawMessage(fmt.Sprintf(`{"text":%q}`, arguments.Path+"-canonical")), nil
			}
			adapter.validate = func(_ context.Context, request capability.PLMValidationRequest) (capability.PLMValidationResult, error) {
				var arguments struct {
					Path string `json:"path"`
				}
				if err := json.Unmarshal(request.Logical.ActualArguments, &arguments); err != nil {
					return capability.PLMValidationResult{}, err
				}
				version := "v0"
				if invalid[arguments.Path] {
					version = "v1"
				}
				return capability.PLMValidationResult{
					Temporal:      capability.TemporalEvidence{Mode: capability.TemporalVersioned, ResourceIdentity: resources[arguments.Path], Version: version},
					TemporalValid: true, ProviderNonInterferenceValid: true, ValidationCostUnits: 1,
				}, nil
			}
			plan, contract := plmVerticalPlan(t, capability.TemporalVersioned, adapter)
			broker, err := capability.NewBroker(capability.Config{RunIdentity: fmt.Sprintf("seeded-%02d", seed), Plan: plan})
			if err != nil {
				t.Fatal(err)
			}
			table, err := capability.NewSplitPhaseTable(broker, capability.SplitPhaseLimits{MaxCalls: 2, MaxCostUnits: 2, MaxResultBytes: 2 << 20})
			if err != nil {
				t.Fatal(err)
			}
			certificates := make([]capability.CandidateCertificate, 2)
			for index, path := range paths {
				certificate := plmVerticalCertificate(broker, plan, path, "", capability.TemporalVersioned)
				certificate.Binding.SiteID = fmt.Sprintf("site-seed-%d", index)
				certificate.Binding.Occurrence = uint32(index + 1)
				certificate.Temporal.Version = "v0"
				resources[path] = certificate.Temporal.ResourceIdentity
				certificates[index] = certificate
				request := []byte(fmt.Sprintf(`{"call_id":"seed-call-%d","capability":"workspace.read_text","arguments":{"path":%q}}`, index, path))
				if err := table.PrepareOrReuse(context.Background(), fmt.Sprintf("slot-%d", index), request, contract, certificate); err != nil {
					t.Fatalf("index=%d path=%s snapshot=%+v: %v", index, path, table.Snapshot(), err)
				}
			}
			for _, path := range paths {
				<-started[path]
			}
			for _, index := range random.Perm(len(paths)) {
				close(gates[paths[index]])
			}
			for index, path := range paths {
				response, err := table.LinearizeAndMaterialize(context.Background(), fmt.Sprintf("slot-%d", index), plmVerticalLogical(certificates[index], path))
				if err != nil {
					t.Fatal(err)
				}
				expected := path + "-candidate"
				if invalid[path] {
					expected = path + "-canonical"
				}
				if !containsResult(response, expected) {
					t.Fatalf("seed=%d path=%s expected=%s response=%s invalid=%v snapshot=%+v", seed, path, expected, response, invalid, table.Snapshot())
				}
			}
			receipts := broker.Receipts()
			invalidCount := 0
			for _, path := range paths {
				if invalid[path] {
					invalidCount++
				}
			}
			snapshot := table.Snapshot()
			if len(receipts) != 2 || receipts[0].CallID != "seed-call-0" || receipts[1].CallID != "seed-call-1" ||
				broker.CallCount() != 2 || int(snapshot.CanonicalStarts) != invalidCount || snapshot.PhysicalStarts != 2 {
				t.Fatalf("receipts=%+v snapshot=%+v invalid=%v", receipts, snapshot, invalid)
			}
			if err := broker.Finalize(true); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPLMCancellationAndLateCompletionRemainRunOwned(t *testing.T) {
	t.Run("during validation", func(t *testing.T) {
		entered := make(chan struct{})
		adapter := &plmVerticalAdapter{handler: func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return json.RawMessage(`{"text":"ready"}`), nil
		}, temporal: capability.TemporalEvidence{Mode: capability.TemporalImmutable}, temporalValid: true, providerValid: true}
		adapter.validate = func(ctx context.Context, _ capability.PLMValidationRequest) (capability.PLMValidationResult, error) {
			close(entered)
			<-ctx.Done()
			return capability.PLMValidationResult{}, ctx.Err()
		}
		plan, contract := plmVerticalPlan(t, capability.TemporalImmutable, adapter)
		broker, _ := capability.NewBroker(capability.Config{RunIdentity: "cancel-validation", Plan: plan})
		table, _ := capability.NewSplitPhaseTable(broker, ownerLimits())
		request := []byte(`{"call_id":"cancel-call","capability":"workspace.read_text","arguments":{"path":"a.txt"}}`)
		certificate := plmVerticalCertificate(broker, plan, "a.txt", "", capability.TemporalImmutable)
		adapter.temporal.ResourceIdentity = certificate.Temporal.ResourceIdentity
		if err := table.PrepareOrReuse(context.Background(), "slot-cancel", request, contract, certificate); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		responseCh := make(chan []byte, 1)
		go func() {
			response, _ := table.LinearizeAndMaterialize(ctx, "slot-cancel", plmVerticalLogical(certificate, "a.txt"))
			responseCh <- response
		}()
		<-entered
		cancel()
		if response := <-responseCh; !containsCode(response, "handler_error") {
			t.Fatalf("response=%s", response)
		}
		if err := broker.Finalize(false); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("late completion after finalize starts", func(t *testing.T) {
		started := make(chan struct{})
		release := make(chan struct{})
		adapter := &plmVerticalAdapter{handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
			close(started)
			<-release
			return json.RawMessage(`{"text":"late"}`), nil
		}, temporal: capability.TemporalEvidence{Mode: capability.TemporalImmutable}, temporalValid: true, providerValid: true}
		plan, contract := plmVerticalPlan(t, capability.TemporalImmutable, adapter)
		broker, _ := capability.NewBroker(capability.Config{RunIdentity: "late-finalize", Plan: plan})
		table, _ := capability.NewSplitPhaseTable(broker, ownerLimits())
		request := []byte(`{"call_id":"late-call","capability":"workspace.read_text","arguments":{"path":"a.txt"}}`)
		certificate := plmVerticalCertificate(broker, plan, "a.txt", "", capability.TemporalImmutable)
		adapter.temporal.ResourceIdentity = certificate.Temporal.ResourceIdentity
		if err := table.PrepareOrReuse(context.Background(), "slot-late", request, contract, certificate); err != nil {
			t.Fatal(err)
		}
		<-started
		finalized := make(chan error, 1)
		go func() { finalized <- broker.Finalize(false) }()
		select {
		case err := <-finalized:
			t.Fatalf("finalize returned before physical completion: %v", err)
		case <-time.After(10 * time.Millisecond):
		}
		close(release)
		if err := <-finalized; err != nil {
			t.Fatal(err)
		}
		if snapshot := table.Snapshot(); snapshot.Discarded != 1 || snapshot.Consumed != 0 {
			t.Fatalf("snapshot=%+v", snapshot)
		}
	})
}

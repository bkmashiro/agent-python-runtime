package capability_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

func TestPLMImmutableCandidateAdoptsAtOriginalLinearizationPoint(t *testing.T) {
	var physical atomic.Uint32
	adapter := &plmVerticalAdapter{handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
		physical.Add(1)
		return json.RawMessage(`{"text":"prepared"}`), nil
	}, temporal: capability.TemporalEvidence{Mode: capability.TemporalImmutable, ResourceIdentity: "immutable:a"}, temporalValid: true, providerValid: true}
	plan, contract := plmVerticalPlan(t, capability.TemporalImmutable, adapter)
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "plm-adopt", Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	table, err := capability.NewSplitPhaseTable(broker, ownerLimits())
	if err != nil {
		t.Fatal(err)
	}
	request := []byte(`{"call_id":"plm-adopt-call","capability":"workspace.read_text","arguments":{"path":"a.txt"}}`)
	certificate := plmVerticalCertificate(broker, plan, "a.txt", "immutable:a", capability.TemporalImmutable)
	adapter.temporal.ResourceIdentity = certificate.Temporal.ResourceIdentity
	if err := table.PrepareOrReuse(context.Background(), "slot-plm-adopt", request, contract, certificate); err != nil {
		t.Fatal(err)
	}
	if broker.Calls() != 0 || len(broker.Receipts()) != 0 {
		t.Fatalf("prepare leaked logical evidence: calls=%d receipts=%d", broker.Calls(), len(broker.Receipts()))
	}
	if snapshot := table.Snapshot(); snapshot.JobsLinearized != 0 || snapshot.JobsMaterialized != 0 {
		t.Fatalf("prepare created job state: %+v", snapshot)
	}
	logical := plmVerticalLogical(certificate, "a.txt")
	response, err := table.LinearizeAndMaterialize(context.Background(), "slot-plm-adopt", logical)
	if err != nil || !containsResult(response, "prepared") {
		t.Fatalf("response=%s err=%v", response, err)
	}
	if physical.Load() != 1 || broker.Calls() != 1 || len(broker.Receipts()) != 1 {
		t.Fatalf("physical=%d calls=%d receipts=%d", physical.Load(), broker.Calls(), len(broker.Receipts()))
	}
	if broker.Receipts()[0].CallID != "plm-adopt-call" {
		t.Fatalf("receipt=%+v", broker.Receipts()[0])
	}
	if _, err := table.LinearizeAndMaterialize(context.Background(), "slot-plm-adopt", logical); err == nil {
		t.Fatal("second linearization unexpectedly succeeded")
	}
	if err := broker.Finalize(true); err != nil {
		t.Fatal(err)
	}
	snapshot := table.Snapshot()
	if snapshot.CandidatesPrepared != 1 || snapshot.CandidatesAdopted != 1 || snapshot.CanonicalStarts != 0 || snapshot.JobsLinearized != 1 || snapshot.JobsMaterialized != 1 || snapshot.Validations != 1 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

func TestPLMPrepareFailureIsHiddenAndRetriesAtLinearization(t *testing.T) {
	var physical atomic.Uint32
	preparedFailed := make(chan struct{})
	adapter := &plmVerticalAdapter{handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
		if physical.Add(1) == 1 {
			close(preparedFailed)
			return nil, fmt.Errorf("early provider failure")
		}
		return json.RawMessage(`{"text":"canonical-after-failure"}`), nil
	}, temporal: capability.TemporalEvidence{Mode: capability.TemporalImmutable, ResourceIdentity: "immutable:a"}, temporalValid: true, providerValid: true}
	plan, contract := plmVerticalPlan(t, capability.TemporalImmutable, adapter)
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "plm-failure-retry", Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	table, err := capability.NewSplitPhaseTable(broker, ownerLimits())
	if err != nil {
		t.Fatal(err)
	}
	request := []byte(`{"call_id":"plm-failure-call","capability":"workspace.read_text","arguments":{"path":"a.txt"}}`)
	certificate := plmVerticalCertificate(broker, plan, "a.txt", "immutable:a", capability.TemporalImmutable)
	adapter.temporal.ResourceIdentity = certificate.Temporal.ResourceIdentity
	if err := table.PrepareOrReuse(context.Background(), "slot-plm-failure", request, contract, certificate); err != nil {
		t.Fatal(err)
	}
	<-preparedFailed
	if broker.Calls() != 0 || len(broker.Receipts()) != 0 {
		t.Fatalf("prepare failure leaked: calls=%d receipts=%d", broker.Calls(), len(broker.Receipts()))
	}
	response, err := table.LinearizeAndMaterialize(context.Background(), "slot-plm-failure", plmVerticalLogical(certificate, "a.txt"))
	if err != nil || !containsResult(response, "canonical-after-failure") {
		t.Fatalf("response=%s err=%v", response, err)
	}
	if physical.Load() != 2 || broker.Calls() != 1 || len(broker.Receipts()) != 1 {
		t.Fatalf("physical=%d calls=%d receipts=%d", physical.Load(), broker.Calls(), len(broker.Receipts()))
	}
	if err := broker.Finalize(true); err != nil {
		t.Fatal(err)
	}
	snapshot := table.Snapshot()
	if snapshot.CandidatesRejected != 1 || snapshot.CanonicalStarts != 1 || snapshot.CandidatesAdopted != 0 || snapshot.JobsMaterialized != 1 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

func TestPLMRuntimeBridgeBuildsCertificateAndLogicalContextFromHostState(t *testing.T) {
	var physical atomic.Uint32
	adapter := &plmVerticalAdapter{handler: func(context.Context, json.RawMessage) (json.RawMessage, error) {
		physical.Add(1)
		return json.RawMessage(`{"text":"runtime-bridge"}`), nil
	}, temporal: capability.TemporalEvidence{Mode: capability.TemporalImmutable}, temporalValid: true, providerValid: true}
	plan, _ := plmVerticalPlan(t, capability.TemporalImmutable, adapter)
	broker, _ := capability.NewBroker(capability.Config{RunIdentity: "plm-runtime-bridge", Plan: plan})
	table, _ := capability.NewSplitPhaseTable(broker, ownerLimits())
	sourceSeal := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	request := []byte(`{"call_id":"plm-s1c0-e1c19-1","capability":"workspace.read_text","arguments":{"path":"a.txt"}}`)
	if err := table.PrepareRuntimePLM(context.Background(), "slot-s1c0-e1c19-1", request, sourceSeal); err != nil {
		t.Fatal(err)
	}
	resourceIdentity, _ := plan.PLMResourceIdentity("workspace.read_text", json.RawMessage(`{"path":"a.txt"}`))
	adapter.temporal.ResourceIdentity = resourceIdentity
	response, err := table.LinearizeRuntimePLM(context.Background(), "slot-s1c0-e1c19-1", request, sourceSeal)
	if err != nil || !containsResult(response, "runtime-bridge") || physical.Load() != 1 || broker.Calls() != 1 {
		t.Fatalf("physical=%d logical=%d response=%s err=%v", physical.Load(), broker.Calls(), response, err)
	}
	malformed := []byte(`{"call_id":"split-s1c0-e1c19-2","capability":"workspace.read_text","arguments":{"path":"a.txt"}}`)
	if err := table.PrepareRuntimePLM(context.Background(), "slot-s1c0-e1c19-2", malformed, sourceSeal); err == nil {
		t.Fatal("mismatched PLM call identity admitted")
	}
	if err := broker.Finalize(true); err != nil {
		t.Fatal(err)
	}
}

func TestPLMInvalidCandidateRestartsCanonicallyInsideOneLogicalCall(t *testing.T) {
	var physical atomic.Uint32
	candidateStarted := make(chan struct{})
	candidateCancelled := make(chan struct{})
	adapter := &plmVerticalAdapter{handler: func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		attempt := physical.Add(1)
		if attempt == 1 {
			close(candidateStarted)
			<-ctx.Done()
			close(candidateCancelled)
			return nil, ctx.Err()
		}
		return json.RawMessage(`{"text":"canonical"}`), nil
	}, temporal: capability.TemporalEvidence{Mode: capability.TemporalImmutable, ResourceIdentity: "immutable:a"}, temporalValid: true, providerValid: true}
	plan, contract := plmVerticalPlan(t, capability.TemporalImmutable, adapter)
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "plm-restart", Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	table, err := capability.NewSplitPhaseTable(broker, ownerLimits())
	if err != nil {
		t.Fatal(err)
	}
	request := []byte(`{"call_id":"plm-restart-call","capability":"workspace.read_text","arguments":{"path":"a.txt"}}`)
	certificate := plmVerticalCertificate(broker, plan, "a.txt", "immutable:a", capability.TemporalImmutable)
	adapter.temporal.ResourceIdentity = certificate.Temporal.ResourceIdentity
	if err := table.PrepareOrReuse(context.Background(), "slot-plm-restart", request, contract, certificate); err != nil {
		t.Fatal(err)
	}
	<-candidateStarted
	adapter.temporal.ResourceIdentity = "immutable:b"
	response, err := table.LinearizeAndMaterialize(context.Background(), "slot-plm-restart", plmVerticalLogical(certificate, "a.txt"))
	if err != nil || !containsResult(response, "canonical") {
		t.Fatalf("response=%s err=%v", response, err)
	}
	<-candidateCancelled
	if physical.Load() != 2 || broker.Calls() != 1 || len(broker.Receipts()) != 1 {
		t.Fatalf("physical=%d calls=%d receipts=%d", physical.Load(), broker.Calls(), len(broker.Receipts()))
	}
	if err := broker.Finalize(true); err != nil {
		t.Fatal(err)
	}
	snapshot := table.Snapshot()
	if snapshot.CandidatesPrepared != 1 || snapshot.CandidatesAdopted != 0 || snapshot.CandidatesRejected != 1 || snapshot.CanonicalStarts != 1 || snapshot.JobsLinearized != 1 || snapshot.JobsMaterialized != 1 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

type plmVerticalAdapter struct {
	handler         capability.HandlerFunc
	temporal        capability.TemporalEvidence
	temporalValid   bool
	providerValid   bool
	stableValidator bool
	stableValid     bool
	providerSession string
	transportRuns   atomic.Uint32
	transportReady  chan struct{}
	validate        func(context.Context, capability.PLMValidationRequest) (capability.PLMValidationResult, error)
	maxCalls        uint32
}

func (adapter *plmVerticalAdapter) Call(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return adapter.handler(ctx, raw)
}

func (adapter *plmVerticalAdapter) PLMValidatorIdentities() capability.PLMValidatorIdentities {
	temporalIdentity := "pysolate.test.temporal-validator.v1"
	if adapter.temporal.Mode == capability.TemporalCurrent || adapter.temporal.Mode == capability.TemporalWallclockObserving {
		temporalIdentity = ""
	}
	return capability.PLMValidatorIdentities{
		Temporal: temporalIdentity, ProviderNonInterference: "pysolate.test.provider-noninterference.v1",
		StableFailure: func() string {
			if adapter.stableValidator {
				return "pysolate.test.stable-failure.v1"
			}
			return ""
		}(),
	}
}

func (adapter *plmVerticalAdapter) ValidatePLM(ctx context.Context, request capability.PLMValidationRequest) (capability.PLMValidationResult, error) {
	if adapter.validate != nil {
		return adapter.validate(ctx, request)
	}
	return capability.PLMValidationResult{
		Temporal: adapter.temporal, TemporalValid: adapter.temporalValid,
		ProviderNonInterferenceValid: adapter.providerValid, StableFailureValid: adapter.stableValid,
		ValidationCostUnits: 1, ProviderValidationPhysicalEvents: 1,
	}, nil
}

func (adapter *plmVerticalAdapter) PLMProviderSessionIdentity(context.Context) string {
	if adapter.providerSession != "" {
		return adapter.providerSession
	}
	return "provider-plm-v1"
}

func (adapter *plmVerticalAdapter) PreparePLMTransport(context.Context, json.RawMessage) error {
	adapter.transportRuns.Add(1)
	if adapter.transportReady != nil {
		close(adapter.transportReady)
	}
	return nil
}

func plmVerticalPlan(t *testing.T, mode capability.TemporalMode, adapter *plmVerticalAdapter) (*capability.Plan, capability.PLMContract) {
	t.Helper()
	spec := stagedTestSpec()
	spec.PreDispatch = nil
	contract := plmValueContract(mode)
	switch mode {
	case capability.TemporalCurrent:
		contract.PrepareEffect = capability.PrepareTransportOnly
		contract.Speculation = capability.SpeculationNever
		contract.TemporalValidator = ""
		contract.MaxResultBytes = 0
	case capability.TemporalWallclockObserving:
		contract.PrepareEffect = capability.PrepareNone
		contract.Speculation = capability.SpeculationNever
		contract.TemporalValidator = ""
		contract.ProviderNonInterferenceValidator = ""
		contract.MaxResultBytes = 0
		contract.CostUnits = 0
		contract.Resource = capability.ResourceReference{}
	}
	if adapter.stableValidator {
		contract.Failure = capability.FailureStable
		contract.StableFailureValidator = "pysolate.test.stable-failure.v1"
	}
	if mode != capability.TemporalWallclockObserving {
		contract.Resource = capability.ResourceReference{Namespace: "workspace", Argument: "path"}
	}
	spec.PLM = &contract
	registry := capability.NewRegistry()
	if err := registry.Register(spec, basicGrant(t), adapter); err != nil {
		t.Fatal(err)
	}
	maxCalls := adapter.maxCalls
	if maxCalls == 0 {
		maxCalls = 1
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: maxCalls})
	if err != nil {
		t.Fatal(err)
	}
	return plan, contract
}

func plmVerticalCertificate(broker *capability.Broker, plan *capability.Plan, path, _ string, mode capability.TemporalMode) capability.CandidateCertificate {
	arguments := []byte(fmt.Sprintf(`{"path":%q}`, path))
	digest := sha256.Sum256(arguments)
	resourceIdentity, _ := plan.PLMResourceIdentity("workspace.read_text", json.RawMessage(arguments))
	return capability.CandidateCertificate{
		Binding: capability.CandidateBinding{
			RunIdentity: broker.RunIdentity(), PlanIdentity: plan.Identity(), SourceSealIdentity: "seal-plm-v1",
			SiteID: "site-plm-v1", Occurrence: 1, Capability: "workspace.read_text", HandlerIdentity: "test.workspace.read-text.v1",
			ArgumentsSHA256: fmt.Sprintf("sha256:%x", digest[:]), AuthorityEpoch: "authority-plm-v1", ProviderSessionIdentity: "provider-plm-v1",
		},
		Temporal: capability.TemporalEvidence{Mode: mode, ResourceIdentity: resourceIdentity},
	}
}

func plmVerticalLogical(certificate capability.CandidateCertificate, path string) capability.PLMLogicalContext {
	return capability.PLMLogicalContext{
		SourceSealIdentity: certificate.Binding.SourceSealIdentity, SiteID: certificate.Binding.SiteID,
		Occurrence: certificate.Binding.Occurrence, AuthorityEpoch: certificate.Binding.AuthorityEpoch,
		ActualArguments: json.RawMessage(fmt.Sprintf(`{"path":%q}`, path)),
	}
}

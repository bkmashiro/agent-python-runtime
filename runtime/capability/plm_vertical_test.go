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
	plan := splitPhasePlan(t, 1, func(context.Context, json.RawMessage) (json.RawMessage, error) {
		physical.Add(1)
		return json.RawMessage(`{"text":"prepared"}`), nil
	})
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "plm-adopt", Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	table, err := capability.NewSplitPhaseTable(broker, ownerLimits())
	if err != nil {
		t.Fatal(err)
	}
	request := []byte(`{"call_id":"plm-adopt-call","capability":"workspace.read_text","arguments":{"path":"a.txt"}}`)
	contract := plmValueContract(capability.TemporalImmutable)
	certificate := plmVerticalCertificate(broker, plan, "a.txt", "immutable:a")
	if err := table.PrepareOrReuse(context.Background(), "slot-plm-adopt", request, contract, certificate); err != nil {
		t.Fatal(err)
	}
	if broker.Calls() != 0 || len(broker.Receipts()) != 0 {
		t.Fatalf("prepare leaked logical evidence: calls=%d receipts=%d", broker.Calls(), len(broker.Receipts()))
	}
	if snapshot := table.Snapshot(); snapshot.JobsLinearized != 0 || snapshot.JobsMaterialized != 0 {
		t.Fatalf("prepare created job state: %+v", snapshot)
	}
	current := plmVerticalContext(certificate)
	response, err := table.LinearizeAndMaterialize(context.Background(), "slot-plm-adopt", current)
	if err != nil || !containsResult(response, "prepared") {
		t.Fatalf("response=%s err=%v", response, err)
	}
	if physical.Load() != 1 || broker.Calls() != 1 || len(broker.Receipts()) != 1 {
		t.Fatalf("physical=%d calls=%d receipts=%d", physical.Load(), broker.Calls(), len(broker.Receipts()))
	}
	if broker.Receipts()[0].CallID != "plm-adopt-call" {
		t.Fatalf("receipt=%+v", broker.Receipts()[0])
	}
	if _, err := table.LinearizeAndMaterialize(context.Background(), "slot-plm-adopt", current); err == nil {
		t.Fatal("second linearization unexpectedly succeeded")
	}
	if err := broker.Finalize(true); err != nil {
		t.Fatal(err)
	}
	snapshot := table.Snapshot()
	if snapshot.CandidatesPrepared != 1 || snapshot.CandidatesAdopted != 1 || snapshot.CanonicalStarts != 0 || snapshot.JobsLinearized != 1 || snapshot.JobsMaterialized != 1 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

func TestPLMPrepareFailureIsHiddenAndRetriesAtLinearization(t *testing.T) {
	var physical atomic.Uint32
	preparedFailed := make(chan struct{})
	plan := splitPhasePlan(t, 1, func(context.Context, json.RawMessage) (json.RawMessage, error) {
		if physical.Add(1) == 1 {
			close(preparedFailed)
			return nil, fmt.Errorf("early provider failure")
		}
		return json.RawMessage(`{"text":"canonical-after-failure"}`), nil
	})
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "plm-failure-retry", Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	table, err := capability.NewSplitPhaseTable(broker, ownerLimits())
	if err != nil {
		t.Fatal(err)
	}
	request := []byte(`{"call_id":"plm-failure-call","capability":"workspace.read_text","arguments":{"path":"a.txt"}}`)
	contract := plmValueContract(capability.TemporalImmutable)
	certificate := plmVerticalCertificate(broker, plan, "a.txt", "immutable:a")
	if err := table.PrepareOrReuse(context.Background(), "slot-plm-failure", request, contract, certificate); err != nil {
		t.Fatal(err)
	}
	<-preparedFailed
	if broker.Calls() != 0 || len(broker.Receipts()) != 0 {
		t.Fatalf("prepare failure leaked: calls=%d receipts=%d", broker.Calls(), len(broker.Receipts()))
	}
	response, err := table.LinearizeAndMaterialize(context.Background(), "slot-plm-failure", plmVerticalContext(certificate))
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

func TestPLMInvalidCandidateRestartsCanonicallyInsideOneLogicalCall(t *testing.T) {
	var physical atomic.Uint32
	candidateStarted := make(chan struct{})
	candidateCancelled := make(chan struct{})
	plan := splitPhasePlan(t, 1, func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		attempt := physical.Add(1)
		if attempt == 1 {
			close(candidateStarted)
			<-ctx.Done()
			close(candidateCancelled)
			return nil, ctx.Err()
		}
		return json.RawMessage(`{"text":"canonical"}`), nil
	})
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "plm-restart", Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	table, err := capability.NewSplitPhaseTable(broker, ownerLimits())
	if err != nil {
		t.Fatal(err)
	}
	request := []byte(`{"call_id":"plm-restart-call","capability":"workspace.read_text","arguments":{"path":"a.txt"}}`)
	contract := plmValueContract(capability.TemporalImmutable)
	certificate := plmVerticalCertificate(broker, plan, "a.txt", "immutable:a")
	if err := table.PrepareOrReuse(context.Background(), "slot-plm-restart", request, contract, certificate); err != nil {
		t.Fatal(err)
	}
	<-candidateStarted
	current := plmVerticalContext(certificate)
	current.Temporal.ResourceIdentity = "immutable:b"
	response, err := table.LinearizeAndMaterialize(context.Background(), "slot-plm-restart", current)
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

func plmVerticalCertificate(broker *capability.Broker, plan *capability.Plan, path, resource string) capability.CandidateCertificate {
	arguments := []byte(fmt.Sprintf(`{"path":%q}`, path))
	digest := sha256.Sum256(arguments)
	return capability.CandidateCertificate{
		Binding: capability.CandidateBinding{
			RunIdentity: broker.RunIdentity(), PlanIdentity: plan.Identity(), SourceSealIdentity: "seal-plm-v1",
			SiteID: "site-plm-v1", Occurrence: 1, Capability: "workspace.read_text", HandlerIdentity: "test.workspace.read-text.v1",
			ArgumentsSHA256: fmt.Sprintf("sha256:%x", digest[:]), AuthorityEpoch: "authority-plm-v1", ProviderSessionIdentity: "provider-plm-v1",
		},
		Temporal: capability.TemporalEvidence{Mode: capability.TemporalImmutable, ResourceIdentity: resource},
	}
}

func plmVerticalContext(certificate capability.CandidateCertificate) capability.LinearizationContext {
	return capability.LinearizationContext{
		Binding: certificate.Binding, Temporal: certificate.Temporal,
		TemporalValidated: true, ProviderNonInterferenceValidated: true,
	}
}

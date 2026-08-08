package evidencebundle_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/agenttrace"
	"github.com/bkmashiro/agent-python-runtime/claimmanifest"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/transaction"
	"github.com/bkmashiro/agent-python-runtime/verification/evidencebundle"
)

func TestCurrentCrossPlaneBundleVerifiesAndMissingEdgesStayInsufficient(t *testing.T) {
	sources := completeSources(t)
	bundle, err := evidencebundle.Build(sources)
	if err != nil {
		t.Fatal(err)
	}
	report, err := evidencebundle.Verify(bundle, sources, evidencebundle.ProfileCurrentCrossPlane)
	if err != nil || report.Status != evidencebundle.StatusVerified || len(report.Missing) != 0 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	if len(bundle.Edges) != 5 {
		t.Fatalf("edges=%+v", bundle.Edges)
	}

	for index, removed := range bundle.Edges {
		candidate := bundle
		candidate.Edges = append(append([]evidencebundle.Edge(nil), bundle.Edges[:index]...), bundle.Edges[index+1:]...)
		report, err := evidencebundle.Verify(candidate, sources, evidencebundle.ProfileCurrentCrossPlane)
		if err != nil || report.Status != evidencebundle.StatusInsufficient || len(report.Missing) == 0 {
			t.Fatalf("removed=%+v report=%+v err=%v", removed, report, err)
		}
	}
}

func TestPartialBundleStillVerifiesStructuralProfileAndReportsCrossPlaneGaps(t *testing.T) {
	sources := completeSources(t)
	sources.Transaction = nil
	sources.CheckpointEventID = ""
	bundle, err := evidencebundle.Build(sources)
	if err != nil {
		t.Fatal(err)
	}
	structural, err := evidencebundle.Verify(bundle, sources, evidencebundle.ProfileStructuralExecution)
	if err != nil || structural.Status != evidencebundle.StatusVerified {
		t.Fatalf("structural=%+v err=%v", structural, err)
	}
	crossPlane, err := evidencebundle.Verify(bundle, sources, evidencebundle.ProfileCurrentCrossPlane)
	if err != nil || crossPlane.Status != evidencebundle.StatusInsufficient || len(crossPlane.Missing) != 3 {
		t.Fatalf("cross_plane=%+v err=%v", crossPlane, err)
	}
}

func TestFullOutcomeProfileReportsTheUnimplementedOracleBoundary(t *testing.T) {
	sources := completeSources(t)
	bundle, err := evidencebundle.Build(sources)
	if err != nil {
		t.Fatal(err)
	}
	report, err := evidencebundle.Verify(bundle, sources, evidencebundle.ProfileFullOutcome)
	if err != nil || report.Status != evidencebundle.StatusInsufficient {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	if len(report.Missing) != 1 || report.Missing[0] != evidencebundle.RequirementFinalStateOracle {
		t.Fatalf("missing=%v", report.Missing)
	}
}

func TestBundleRejectsUnsupportedEdgesAndCrossExecutionTransactionBinding(t *testing.T) {
	sources := completeSources(t)
	bundle, err := evidencebundle.Build(sources)
	if err != nil {
		t.Fatal(err)
	}
	candidate := bundle
	candidate.Edges = append([]evidencebundle.Edge(nil), bundle.Edges...)
	candidate.Edges[0].Kind = "asserts-semantic-outcome"
	if _, err := evidencebundle.Verify(candidate, sources, evidencebundle.ProfileCurrentCrossPlane); !errors.Is(err, evidencebundle.ErrInvalidBundle) {
		t.Fatalf("err=%v", err)
	}

	crossExecution := sources
	transactionEvidence := *sources.Transaction
	transactionEvidence.Transaction.RunID = "other-execution"
	transactionEvidence.EvidenceDigest, err = transaction.ComputeTransactionEvidenceDigest(transactionEvidence)
	if err != nil {
		t.Fatal(err)
	}
	crossExecution.Transaction = &transactionEvidence
	if _, err := evidencebundle.Build(crossExecution); !errors.Is(err, evidencebundle.ErrContradictedSource) {
		t.Fatalf("err=%v", err)
	}
}

func TestReconciledEffectRequiresDistinctReceiptAndReadbackEvidence(t *testing.T) {
	sources := completeSources(t)
	transactionEvidence := *sources.Transaction
	transactionEvidence.Attempts = append([]transaction.EvidenceAttempt(nil), sources.Transaction.Attempts...)
	transactionEvidence.Attempts[0].ReconciliationDigest = transactionEvidence.Attempts[0].ProviderReceiptDigest
	var err error
	transactionEvidence.EvidenceDigest, err = transaction.ComputeTransactionEvidenceDigest(transactionEvidence)
	if err != nil {
		t.Fatal(err)
	}
	sources.Transaction = &transactionEvidence
	if _, err := evidencebundle.Build(sources); !errors.Is(err, evidencebundle.ErrContradictedSource) {
		t.Fatalf("err=%v", err)
	}

	sources = completeSources(t)
	transactionEvidence = *sources.Transaction
	transactionEvidence.Operations = append([]transaction.EvidenceOperation(nil), sources.Transaction.Operations...)
	transactionEvidence.Operations[0].EffectClass = transaction.EffectReversible
	transactionEvidence.EvidenceDigest, err = transaction.ComputeTransactionEvidenceDigest(transactionEvidence)
	if err != nil {
		t.Fatal(err)
	}
	sources.Transaction = &transactionEvidence
	if _, err := evidencebundle.Build(sources); !errors.Is(err, evidencebundle.ErrContradictedSource) {
		t.Fatalf("reversible effect err=%v", err)
	}
}

func TestReconciledEffectRequiresProviderRequestToMatchOperationManifest(t *testing.T) {
	sources := completeSources(t)
	transactionEvidence := *sources.Transaction
	transactionEvidence.Attempts = append([]transaction.EvidenceAttempt(nil), sources.Transaction.Attempts...)
	transactionEvidence.Attempts[0].ProviderRequestDigest = digest("4")
	var err error
	transactionEvidence.EvidenceDigest, err = transaction.ComputeTransactionEvidenceDigest(transactionEvidence)
	if err != nil {
		t.Fatal(err)
	}
	sources.Transaction = &transactionEvidence
	if _, err := evidencebundle.Build(sources); !errors.Is(err, evidencebundle.ErrContradictedSource) {
		t.Fatalf("provider request/operation mismatch err=%v", err)
	}
}

func TestReconciledEffectRequiresManifestBoundConsumedApproval(t *testing.T) {
	t.Run("wrong manifest", func(t *testing.T) {
		sources := completeSources(t)
		value := *sources.Transaction
		value.Approvals = append([]transaction.EvidenceApproval(nil), sources.Transaction.Approvals...)
		value.Approvals[0].ManifestDigest = digest("9")
		var err error
		value.EvidenceDigest, err = transaction.ComputeTransactionEvidenceDigest(value)
		if err != nil {
			t.Fatal(err)
		}
		sources.Transaction = &value
		if _, err := evidencebundle.Build(sources); !errors.Is(err, evidencebundle.ErrContradictedSource) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("expired before consumption", func(t *testing.T) {
		sources := completeSources(t)
		value := *sources.Transaction
		value.Approvals = append([]transaction.EvidenceApproval(nil), sources.Transaction.Approvals...)
		if value.Approvals[0].ConsumedAt == nil {
			t.Fatal("fixture approval was not consumed")
		}
		value.Approvals[0].ExpiresAt = value.Approvals[0].ConsumedAt.Add(-time.Second)
		var err error
		value.EvidenceDigest, err = transaction.ComputeTransactionEvidenceDigest(value)
		if err != nil {
			t.Fatal(err)
		}
		sources.Transaction = &value
		if _, err := evidencebundle.Build(sources); !errors.Is(err, evidencebundle.ErrContradictedSource) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("consumed after dispatch", func(t *testing.T) {
		sources := completeSources(t)
		value := *sources.Transaction
		value.Approvals = append([]transaction.EvidenceApproval(nil), sources.Transaction.Approvals...)
		consumedAt := value.Transaction.UpdatedAt.Add(time.Second)
		value.Approvals[0].ConsumedAt = &consumedAt
		value.Approvals[0].ExpiresAt = consumedAt.Add(time.Hour)
		var err error
		value.EvidenceDigest, err = transaction.ComputeTransactionEvidenceDigest(value)
		if err != nil {
			t.Fatal(err)
		}
		sources.Transaction = &value
		if _, err := evidencebundle.Build(sources); !errors.Is(err, evidencebundle.ErrContradictedSource) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("missing approval", func(t *testing.T) {
		sources := completeSources(t)
		value := *sources.Transaction
		value.Approvals = []transaction.EvidenceApproval{}
		value.Metrics.ApprovalTotal = 0
		value.Metrics.ConsumedApprovals = 0
		var err error
		value.EvidenceDigest, err = transaction.ComputeTransactionEvidenceDigest(value)
		if err != nil {
			t.Fatal(err)
		}
		sources.Transaction = &value
		if _, err := evidencebundle.Build(sources); !errors.Is(err, evidencebundle.ErrContradictedSource) {
			t.Fatalf("err=%v", err)
		}
	})
}

func TestReconciliationDigestRequiresRecordedReconciliationPath(t *testing.T) {
	sources := completeSources(t)
	value := *sources.Transaction
	value.Transitions = append([]transaction.EvidenceTransition(nil), sources.Transaction.Transitions...)

	for index := range value.Transitions {
		transition := &value.Transitions[index]
		if transition.EntityType == "attempt" && transition.From == string(transaction.AttemptDispatching) && transition.To == string(transaction.AttemptAmbiguous) {
			transition.To = string(transaction.AttemptSucceeded)
		}
	}
	filtered := value.Transitions[:0]
	for _, transition := range value.Transitions {
		if transition.EntityType == "attempt" && transition.From == string(transaction.AttemptAmbiguous) {
			continue
		}
		if transition.EntityType == "operation" && transition.To == string(transaction.OperationReconciliationRequired) {
			continue
		}
		if transition.EntityType == "operation" && transition.From == string(transaction.OperationReconciliationRequired) {
			transition.From = string(transaction.OperationApplying)
		}
		filtered = append(filtered, transition)
	}
	value.Transitions = filtered
	for index := range value.Transitions {
		value.Transitions[index].Sequence = uint64(index + 1)
	}
	value.Metrics.TransitionTotal = uint32(len(value.Transitions))
	var err error
	value.EvidenceDigest, err = transaction.ComputeTransactionEvidenceDigest(value)
	if err != nil {
		t.Fatal(err)
	}
	sources.Transaction = &value
	if _, err := evidencebundle.Build(sources); !errors.Is(err, evidencebundle.ErrContradictedSource) {
		t.Fatalf("err=%v", err)
	}
}

func TestTransactionEvidenceRejectsCrossEntityCausalReordering(t *testing.T) {
	t.Run("terminal transaction before effect", func(t *testing.T) {
		sources := completeSources(t)
		value := *sources.Transaction
		original := append([]transaction.EvidenceTransition(nil), sources.Transaction.Transitions...)
		commit := original[len(original)-1]
		insertAt := len(original) - 3
		reordered := append([]transaction.EvidenceTransition{}, original[:insertAt]...)
		reordered = append(reordered, commit)
		reordered = append(reordered, original[insertAt:len(original)-1]...)
		for index := range reordered {
			reordered[index].Sequence = uint64(index + 1)
			reordered[index].ObservedAt = original[index].ObservedAt
		}
		value.Transitions = reordered
		var err error
		value.EvidenceDigest, err = transaction.ComputeTransactionEvidenceDigest(value)
		if err != nil {
			t.Fatal(err)
		}
		sources.Transaction = &value
		if _, err := evidencebundle.Build(sources); !errors.Is(err, evidencebundle.ErrContradictedSource) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("operation completion before attempt completion", func(t *testing.T) {
		sources := completeSources(t)
		value := *sources.Transaction
		transitions := append([]transaction.EvidenceTransition(nil), sources.Transaction.Transitions...)
		attemptIndex, operationIndex := -1, -1
		for index, transition := range transitions {
			if transition.EntityType == "attempt" && transition.To == string(transaction.AttemptSucceeded) {
				attemptIndex = index
			}
			if transition.EntityType == "operation" && transition.To == string(transaction.OperationApplied) {
				operationIndex = index
			}
		}
		if attemptIndex < 0 || operationIndex < 0 || attemptIndex >= operationIndex {
			t.Fatalf("unexpected transition order attempt=%d operation=%d", attemptIndex, operationIndex)
		}
		originalTimes := make([]time.Time, len(transitions))
		for index := range transitions {
			originalTimes[index] = transitions[index].ObservedAt
		}
		transitions[attemptIndex], transitions[operationIndex] = transitions[operationIndex], transitions[attemptIndex]
		for index := range transitions {
			transitions[index].Sequence = uint64(index + 1)
			transitions[index].ObservedAt = originalTimes[index]
		}
		value.Transitions = transitions
		var err error
		value.EvidenceDigest, err = transaction.ComputeTransactionEvidenceDigest(value)
		if err != nil {
			t.Fatal(err)
		}
		sources.Transaction = &value
		if _, err := evidencebundle.Build(sources); !errors.Is(err, evidencebundle.ErrContradictedSource) {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("terminal attempt before operation start", func(t *testing.T) {
		sources := completeSources(t)
		value := *sources.Transaction
		value.Attempts = append([]transaction.EvidenceAttempt(nil), sources.Transaction.Attempts...)
		value.Attempts[0].ReconciliationDigest = ""
		value.Metrics.ReconciledAttempts--

		transitions := append([]transaction.EvidenceTransition(nil), sources.Transaction.Transitions...)
		for index := range transitions {
			transition := &transitions[index]
			if transition.EntityType == "attempt" && transition.From == string(transaction.AttemptDispatching) && transition.To == string(transaction.AttemptAmbiguous) {
				transition.To = string(transaction.AttemptSucceeded)
			}
			if transition.EntityType == "operation" && transition.From == string(transaction.OperationReconciliationRequired) {
				transition.From = string(transaction.OperationApplying)
			}
		}
		filtered := transitions[:0]
		for _, transition := range transitions {
			if transition.EntityType == "attempt" && transition.From == string(transaction.AttemptAmbiguous) {
				continue
			}
			if transition.EntityType == "operation" && transition.To == string(transaction.OperationReconciliationRequired) {
				continue
			}
			filtered = append(filtered, transition)
		}
		transitions = filtered
		attemptSucceeded := -1
		operationApplying := -1
		for index, transition := range transitions {
			if transition.EntityType == "attempt" && transition.To == string(transaction.AttemptSucceeded) {
				attemptSucceeded = index
			}
			if transition.EntityType == "operation" && transition.To == string(transaction.OperationApplying) {
				operationApplying = index
			}
		}
		if attemptSucceeded < 0 || operationApplying < 0 || attemptSucceeded <= operationApplying {
			t.Fatalf("unexpected transition indexes attempt=%d operation=%d", attemptSucceeded, operationApplying)
		}
		times := make([]time.Time, len(transitions))
		for index := range transitions {
			times[index] = transitions[index].ObservedAt
		}
		transitions[attemptSucceeded], transitions[operationApplying] = transitions[operationApplying], transitions[attemptSucceeded]
		for index := range transitions {
			transitions[index].Sequence = uint64(index + 1)
			transitions[index].ObservedAt = times[index]
		}
		value.Transitions = transitions
		value.Metrics.TransitionTotal = uint32(len(transitions))
		var err error
		value.EvidenceDigest, err = transaction.ComputeTransactionEvidenceDigest(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := transaction.VerifyTransactionEvidenceDigest(value); !errors.Is(err, transaction.ErrInvalidEvidence) {
			t.Fatalf("transaction verifier err=%v", err)
		}
		sources.Transaction = &value
		if _, err := evidencebundle.Build(sources); !errors.Is(err, evidencebundle.ErrContradictedSource) {
			t.Fatalf("bundle err=%v", err)
		}
	})
}

func TestCommittedTransactionRequiresEveryOperationApplied(t *testing.T) {
	sources := completeSources(t)
	value := *sources.Transaction
	value.Operations = append([]transaction.EvidenceOperation(nil), sources.Transaction.Operations...)
	commit := value.Transitions[len(value.Transitions)-1]
	prior := value.Transitions[len(value.Transitions)-2].ObservedAt
	observed := prior.Add(commit.ObservedAt.Sub(prior) / 2)
	value.Operations = append(value.Operations, transaction.EvidenceOperation{
		ID: "operation-extra", TransactionID: value.Transaction.ID, Index: 2,
		ToolID: "fixture.read", HandlerVersion: "v1", EffectClass: transaction.EffectReadOnly,
		Policy: transaction.PolicyAutoCommit, PolicyVersion: "v1", State: transaction.OperationReady,
		ArgumentDigest: digest("6"), ManifestDigest: digest("7"), Version: 1,
		CreatedAt: observed, UpdatedAt: observed,
	})
	value.Transitions = append([]transaction.EvidenceTransition(nil), sources.Transaction.Transitions[:len(sources.Transaction.Transitions)-1]...)
	value.Transitions = append(value.Transitions,
		transaction.EvidenceTransition{
			TransactionID: value.Transaction.ID, EntityType: "operation", EntityID: "operation-extra",
			From: "", To: string(transaction.OperationReady), ObservedAt: observed,
		},
		commit,
	)
	for index := range value.Transitions {
		value.Transitions[index].Sequence = uint64(index + 1)
	}
	value.Metrics.OperationTotal++
	value.Metrics.TransitionTotal++
	var err error
	value.EvidenceDigest, err = transaction.ComputeTransactionEvidenceDigest(value)
	if err != nil {
		t.Fatal(err)
	}
	sources.Transaction = &value
	if _, err := evidencebundle.Build(sources); !errors.Is(err, evidencebundle.ErrContradictedSource) {
		t.Fatalf("err=%v", err)
	}
}

func TestCheckpointMustBeAnAncestorOfTheCompletedExecution(t *testing.T) {
	sources := completeSources(t)
	sources.CheckpointEventID = sources.Playback.Events[0].EventID
	sources.Playback.Events[0].EventType = agenttrace.EventFinalStateObserved
	if _, err := evidencebundle.Build(sources); !errors.Is(err, evidencebundle.ErrContradictedSource) {
		t.Fatalf("err=%v", err)
	}
}

func completeSources(t *testing.T) evidencebundle.Sources {
	t.Helper()
	ref := runtimeconfig.ExecutionRef{
		InvocationRef: runtimeconfig.InvocationRef{
			AgentRunID: "agent-run-1", TurnSeq: 2, OutputItemSeq: 3, SegmentSeq: 4,
			InvocationID: "invocation-1", InvocationAttempt: 1, ExecutionID: "execution-1",
		},
		ExecutedCodeSHA256: digest("a"),
	}
	sink := agenttrace.NewMemorySink()
	recorder, err := (agenttrace.Plugin{Mode: agenttrace.ModeRequired, Sink: sink}).Begin(ref.AgentRunID, func() time.Time {
		return time.Unix(123, 0).UTC()
	})
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := recorder.Record(context.Background(), agenttrace.EventCheckpointCreated, "", json.RawMessage(`{"checkpoint_kind":"fixture"}`), digest("c"))
	if err != nil {
		t.Fatal(err)
	}
	started, err := recorder.Record(context.Background(), agenttrace.EventRuntimeStarted, checkpoint.EventID, json.RawMessage(`{"execution_id":"execution-1"}`), "")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{
		"invocation_id": ref.InvocationID, "invocation_attempt": ref.InvocationAttempt,
		"execution_id": ref.ExecutionID, "executed_code_sha256": ref.ExecutedCodeSHA256,
		"status": "ok", "result_digest": digest("d"),
		"turn_seq": ref.TurnSeq, "output_item_seq": ref.OutputItemSeq, "segment_seq": ref.SegmentSeq,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recorder.Record(context.Background(), agenttrace.EventRuntimeCompleted, started.EventID, payload, ""); err != nil {
		t.Fatal(err)
	}
	playback := agenttrace.Playback{AgentRunID: ref.AgentRunID, Events: sink.Events()}
	manifest, err := claimmanifest.FromMetadataPlayback(ref, playback)
	if err != nil {
		t.Fatal(err)
	}
	created := time.Unix(124, 0).UTC()
	now := created
	clock := func() time.Time {
		now = now.Add(time.Second)
		return now
	}
	ledger := transaction.NewMemoryLedger()
	coordinator := transaction.NewCoordinator(ledger, &bundleSequenceIDs{}, clock, nil)
	tx, err := coordinator.Begin(transaction.BeginRequest{
		RunID: ref.ExecutionID, CatalogDigest: digest("1"), Mode: transaction.TransactionModeWorkflow,
	})
	if err != nil {
		t.Fatal(err)
	}
	operation, err := coordinator.Propose(transaction.ProposeRequest{
		TransactionID: tx.ID, ToolID: "fixture.tool", HandlerVersion: "v1",
		EffectClass: transaction.EffectIrreversible, Policy: transaction.PolicyUserApprovalRequired,
		PolicyVersion: "v1", ArgumentDigest: digest("2"),
	})
	if err != nil {
		t.Fatal(err)
	}
	credential := transaction.CommitCredential{Token: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}
	if _, err := coordinator.RegisterApproval(credential, transaction.AuthorityClaims{
		AuthorityID: "approval-1", TransactionID: tx.ID, OperationID: operation.ID,
		ManifestDigest: operation.ManifestDigest, Source: transaction.CommitSourceUser,
		ActorID: "owner", ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Authorize(credential); err != nil {
		t.Fatal(err)
	}
	dispatch, err := coordinator.BeginDispatch(transaction.DispatchRequest{
		OperationID: operation.ID, Kind: transaction.AttemptApply, Ordinal: 1,
		LeaseDuration: time.Minute, ProviderRequestDigest: operation.ManifestDigest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.CompleteDispatch(transaction.CompleteDispatchRequest{
		OperationID: operation.ID, AttemptID: dispatch.Attempt.ID, Outcome: transaction.DispatchAmbiguous,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.ReconcileAuthorizedDispatch(credential, transaction.ReconcileDispatchRequest{
		OperationID: operation.ID, AttemptID: dispatch.Attempt.ID, Outcome: transaction.DispatchSucceeded,
		ProviderReceiptDigest: digest("e"), ObservationDigest: digest("f"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.FinalizeWorkflow(tx.ID); err != nil {
		t.Fatal(err)
	}
	transactionEvidence, err := transaction.BuildTransactionEvidence(ledger, tx.ID, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	return evidencebundle.Sources{
		Manifest: manifest, Playback: playback, Transaction: &transactionEvidence, CheckpointEventID: checkpoint.EventID,
	}
}

type bundleSequenceIDs struct {
	next uint64
}

func (ids *bundleSequenceIDs) New(prefix string) (string, error) {
	ids.next++
	return fmt.Sprintf("%s-fixture-%d", prefix, ids.next), nil
}

func digest(character string) string {
	value := ""
	for len(value) < 64 {
		value += character
	}
	return "sha256:" + value
}

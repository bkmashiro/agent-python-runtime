package trajectory_test

import (
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/trajectory"
	"github.com/bkmashiro/agent-python-runtime/runtime/receipt"
)

func TestSourceEvidenceCrossBindsVerifiedOccurrenceDynamicCallAndReceipt(t *testing.T) {
	builder := newEvidenceBuilder(t)
	document := appendEvidence(t, builder, trajectory.EvidenceInput{
		Type: trajectory.EventSourceDocument, ActorID: "actor-host-0001",
		Payload: trajectory.SourceDocumentPayload{
			DocumentID: testDigest('a'), SourceSHA256: testDigest('b'), Availability: trajectory.Available,
		},
	})
	occurrence := appendEvidence(t, builder, trajectory.EvidenceInput{
		Type: trajectory.EventSourceOccurrence, ActorID: "actor-host-0001", ParentEventIDs: []string{document.EventID},
		Payload: trajectory.SourceOccurrencePayload{
			DocumentID: testDigest('a'), SourceSHA256: testDigest('b'), OccurrenceID: testDigest('c'),
			StartLine: 1, StartColumn: 9, EndLine: 1, EndColumn: 27,
			Capability: "workspace.read_text", DynamicOccurrence: 1,
		},
	})
	authority := appendEvidence(t, builder, trajectory.EvidenceInput{Type: trajectory.EventAuthoritySnapshot, ActorID: "actor-host-0001", Payload: trajectory.AuthoritySnapshotPayload{RunID: "run-source-decision-0001", CapabilityPlanSHA256: testDigest('e'), PolicySHA256: testDigest('1'), FreshnessSHA256: testDigest('2'), GrantsSHA256: testDigest('3')}})
	baseReceipt := receipt.NewAuthorized("run-source-decision-0001", testDigest('e'), "call-source-decision-0001", "", "", "workspace.read_text", 0, "request-source-decision", "ok", []byte("result"))
	boundReceipt, err := receipt.BindSource(baseReceipt, receipt.SourceBinding{SchemaVersion: receipt.SourceBindingSchemaVersion, ClaimLevel: receipt.SourceClaimBound, DocumentID: testDigest('a'), SourceSHA256: testDigest('b'), OccurrenceID: testDigest('c'), Capability: "workspace.read_text", DynamicOccurrence: 1, StartLine: 1, StartColumn: 9, EndLine: 1, EndColumn: 27})
	if err != nil {
		t.Fatal(err)
	}
	intent := appendEvidence(t, builder, trajectory.EvidenceInput{Type: trajectory.EventEffectTransition, ActorID: "actor-host-0001", ParentEventIDs: []string{authority.EventID}, Payload: trajectory.EffectTransitionPayload{CallID: "call-source-decision-0001", State: trajectory.EffectIntent}})
	started := appendEvidence(t, builder, trajectory.EvidenceInput{Type: trajectory.EventEffectTransition, ActorID: "actor-host-0001", ParentEventIDs: []string{intent.EventID}, Payload: trajectory.EffectTransitionPayload{CallID: "call-source-decision-0001", State: trajectory.EffectStarted}})
	effect := appendEvidence(t, builder, trajectory.EvidenceInput{
		Type: trajectory.EventEffectTransition, ActorID: "actor-host-0001", ParentEventIDs: []string{started.EventID},
		Payload: trajectory.EffectTransitionPayload{CallID: "call-source-decision-0001", ReceiptID: boundReceipt.ReceiptID, State: trajectory.EffectCommitted},
	})
	tool := appendEvidence(t, builder, trajectory.EvidenceInput{Type: trajectory.EventToolDecision, ActorID: "actor-host-0001", ParentEventIDs: []string{authority.EventID, effect.EventID}, Payload: trajectory.ToolDecisionPayload{ApprovalDisposition: "not_required", ArgumentsSHA256: "sha256:" + boundReceipt.RequestSHA256, BrokerOutcome: "ok", CallID: boundReceipt.CallID, Capability: boundReceipt.Capability, CapabilityPlanSHA256: boundReceipt.CapabilityPlanSHA256, Mechanism: "direct", OperationIndex: boundReceipt.OperationIndex, ReceiptID: boundReceipt.ReceiptID, ResultSHA256: "sha256:" + boundReceipt.ResponseSHA256, RunID: boundReceipt.RunID, SourceBound: true}})
	decision := appendEvidence(t, builder, trajectory.EvidenceInput{
		Type: trajectory.EventSourceDecision, ActorID: "actor-host-0001", ParentEventIDs: []string{occurrence.EventID, effect.EventID, tool.EventID},
		Payload: trajectory.SourceDecisionPayload{
			DecisionID: testDigest('d'), CapabilityPlanSHA256: testDigest('e'), OccurrenceID: testDigest('c'),
			DynamicOccurrence: 1, ClaimLevel: trajectory.ClaimSourceBound, Admitted: true,
			ReceiptID: boundReceipt.ReceiptID,
		},
	})
	if decision.EventID == "" {
		t.Fatal("source decision has no canonical identity")
	}
}

func TestSourceDecisionReasonsAreStableAndExecutedLineRequiresInstrumentation(t *testing.T) {
	builder := newEvidenceBuilder(t)
	if _, err := builder.Append(trajectory.EvidenceInput{
		Type: trajectory.EventSourceDecision, ActorID: "actor-host-0001",
		Payload: trajectory.SourceDecisionPayload{
			DecisionID: testDigest('a'), CapabilityPlanSHA256: testDigest('b'), OccurrenceID: testDigest('c'),
			DynamicOccurrence: 1, ClaimLevel: trajectory.ClaimSourceBound, Admitted: false,
			Reasons: []string{"z_reason", "a_reason"},
		},
	}); err == nil {
		t.Fatal("unsorted rejection reasons accepted")
	}
	if _, err := builder.Append(trajectory.EvidenceInput{
		Type: trajectory.EventExecutedLine, ActorID: "actor-runtime-0001",
		Payload: trajectory.ExecutedLinePayload{
			SourceSHA256: testDigest('d'), Availability: trajectory.Available,
			StartLine: 8, StartColumn: 13, EndLine: 8, EndColumn: 23,
		},
	}); err == nil {
		t.Fatal("executed-line claim without instrumentation accepted")
	}
	if _, err := builder.Append(trajectory.EvidenceInput{
		Type: trajectory.EventExecutedLine, ActorID: "actor-runtime-0001",
		Payload: trajectory.ExecutedLinePayload{
			SourceSHA256: testDigest('d'), Availability: trajectory.Available, Instrumentation: "sys.monitoring",
			InstructionOffset: 14, StartLine: 8, StartColumn: 13, EndLine: 8, EndColumn: 23,
		},
	}); err != nil {
		t.Fatalf("instrumented executed line rejected: %v", err)
	}
}

func TestCrossPlanePayloadIdentityMismatchFailsClosed(t *testing.T) {
	builder := newEvidenceBuilder(t)
	document := appendEvidence(t, builder, trajectory.EvidenceInput{
		Type: trajectory.EventSourceDocument, ActorID: "actor-host-0001",
		Payload: trajectory.SourceDocumentPayload{DocumentID: testDigest('a'), SourceSHA256: testDigest('b'), Availability: trajectory.Available},
	})
	if _, err := builder.Append(trajectory.EvidenceInput{
		Type: trajectory.EventSourceOccurrence, ActorID: "actor-host-0001", ParentEventIDs: []string{document.EventID},
		Payload: trajectory.SourceOccurrencePayload{
			DocumentID: testDigest('c'), SourceSHA256: testDigest('b'), OccurrenceID: testDigest('d'),
			StartLine: 1, EndLine: 1, EndColumn: 1, Capability: "workspace.read_text", DynamicOccurrence: 1,
		},
	}); err == nil {
		t.Fatal("source occurrence with mismatched document parent accepted")
	}
	context := appendEvidence(t, builder, trajectory.EvidenceInput{
		Type: trajectory.EventSubagentContext, ActorID: "actor-harness-0001",
		Payload: trajectory.SubagentContextPayload{ChildID: "child-cross-plane-0001", ContextSHA256: testDigest('e'), BriefSHA256: testDigest('f'), Availability: trajectory.Available},
	})
	if _, err := builder.Append(trajectory.EvidenceInput{
		Type: trajectory.EventSubagentRuntime, ActorID: "actor-host-0001", ParentEventIDs: []string{context.EventID, document.EventID},
		Payload: testSubagentRuntimePayload(t, "child-cross-plane-0002", testDigest('e'), testDigest('f'), testDigest('b'), testDigest('5')),
	}); err == nil {
		t.Fatal("subagent runtime with mismatched child context accepted")
	}
}

func TestNonSourceToolDecisionRecomputesReceiptIdentity(t *testing.T) {
	for name, mutate := range map[string]func(*trajectory.ToolDecisionPayload){
		"valid":            func(*trajectory.ToolDecisionPayload) {},
		"tampered request": func(payload *trajectory.ToolDecisionPayload) { payload.ArgumentsSHA256 = testDigest('9') },
	} {
		t.Run(name, func(t *testing.T) {
			builder := newEvidenceBuilder(t)
			authorized := receipt.NewAuthorized("execution-contract-0001", testDigest('1'), "call-non-source-0001", "", "", "workspace.read_text", 0, "non-source-request", "ok", []byte("result"))
			authority := appendEvidence(t, builder, trajectory.EvidenceInput{Type: trajectory.EventAuthoritySnapshot, ActorID: "actor-host-0001", Payload: trajectory.AuthoritySnapshotPayload{RunID: authorized.RunID, CapabilityPlanSHA256: authorized.CapabilityPlanSHA256, PolicySHA256: testDigest('2'), FreshnessSHA256: testDigest('3'), GrantsSHA256: testDigest('4')}})
			intent := appendEvidence(t, builder, trajectory.EvidenceInput{Type: trajectory.EventEffectTransition, ActorID: "actor-host-0001", ParentEventIDs: []string{authority.EventID}, Payload: trajectory.EffectTransitionPayload{CallID: authorized.CallID, State: trajectory.EffectIntent}})
			started := appendEvidence(t, builder, trajectory.EvidenceInput{Type: trajectory.EventEffectTransition, ActorID: "actor-host-0001", ParentEventIDs: []string{intent.EventID}, Payload: trajectory.EffectTransitionPayload{CallID: authorized.CallID, State: trajectory.EffectStarted}})
			effect := appendEvidence(t, builder, trajectory.EvidenceInput{Type: trajectory.EventEffectTransition, ActorID: "actor-host-0001", ParentEventIDs: []string{started.EventID}, Payload: trajectory.EffectTransitionPayload{CallID: authorized.CallID, ReceiptID: authorized.ReceiptID, State: trajectory.EffectCommitted}})
			payload := trajectory.ToolDecisionPayload{ApprovalDisposition: "not_required", ArgumentsSHA256: "sha256:" + authorized.RequestSHA256, BrokerOutcome: authorized.Outcome, CallID: authorized.CallID, Capability: authorized.Capability, CapabilityPlanSHA256: authorized.CapabilityPlanSHA256, Mechanism: "direct", OperationIndex: authorized.OperationIndex, ReceiptID: authorized.ReceiptID, ResultSHA256: "sha256:" + authorized.ResponseSHA256, RunID: authorized.RunID}
			mutate(&payload)
			_, err := builder.Append(trajectory.EvidenceInput{Type: trajectory.EventToolDecision, ActorID: "actor-host-0001", ParentEventIDs: []string{authority.EventID, effect.EventID}, Payload: payload})
			if (name == "valid") != (err == nil) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestRejectedSourceDecisionCanTerminateAtHostAuthorityBeforeBrokerDispatch(t *testing.T) {
	builder := newEvidenceBuilder(t)
	document := appendEvidence(t, builder, trajectory.EvidenceInput{Type: trajectory.EventSourceDocument, ActorID: "actor-host-0001", Payload: trajectory.SourceDocumentPayload{DocumentID: testDigest('a'), SourceSHA256: testDigest('b'), Availability: trajectory.Available}})
	occurrence := appendEvidence(t, builder, trajectory.EvidenceInput{Type: trajectory.EventSourceOccurrence, ActorID: "actor-host-0001", ParentEventIDs: []string{document.EventID}, Payload: trajectory.SourceOccurrencePayload{DocumentID: testDigest('a'), SourceSHA256: testDigest('b'), OccurrenceID: testDigest('c'), StartLine: 1, EndLine: 1, EndColumn: 4, Capability: "workspace.read_text", DynamicOccurrence: 1}})
	authority := appendEvidence(t, builder, trajectory.EvidenceInput{Type: trajectory.EventAuthoritySnapshot, ActorID: "actor-host-0001", Payload: trajectory.AuthoritySnapshotPayload{RunID: "execution-contract-0001", CapabilityPlanSHA256: testDigest('d'), PolicySHA256: testDigest('1'), FreshnessSHA256: testDigest('2'), GrantsSHA256: testDigest('3')}})
	if _, err := builder.Append(trajectory.EvidenceInput{Type: trajectory.EventSourceDecision, ActorID: "actor-host-0001", ParentEventIDs: []string{occurrence.EventID, authority.EventID}, Payload: trajectory.SourceDecisionPayload{DecisionID: testDigest('e'), CapabilityPlanSHA256: testDigest('d'), OccurrenceID: testDigest('c'), DynamicOccurrence: 1, ClaimLevel: trajectory.ClaimSourceBound, Admitted: false, Reasons: []string{"static_rejection"}}}); err != nil {
		t.Fatalf("Host-side rejected decision was not representable: %v", err)
	}
}

func TestSubagentContextRuntimeAndWorkspacePlanesRemainIndependent(t *testing.T) {
	builder := newEvidenceBuilder(t)
	context := appendEvidence(t, builder, trajectory.EvidenceInput{
		Type: trajectory.EventSubagentContext, ActorID: "actor-harness-0001",
		Payload: trajectory.SubagentContextPayload{
			ChildID: "child-research-0001", ContextSHA256: testDigest('a'), BriefSHA256: testDigest('b'), Availability: trajectory.Available,
		},
	})
	source := appendEvidence(t, builder, trajectory.EvidenceInput{Type: trajectory.EventSourceDocument, ActorID: "actor-harness-0001", Payload: trajectory.SourceDocumentPayload{DocumentID: testDigest('1'), SourceSHA256: testDigest('2'), Availability: trajectory.Available}})
	runtimeEvent := appendEvidence(t, builder, trajectory.EvidenceInput{
		Type: trajectory.EventSubagentRuntime, ActorID: "actor-host-0001", ParentEventIDs: []string{context.EventID, source.EventID},
		Payload: testSubagentRuntimePayload(t, "child-research-0001", testDigest('a'), testDigest('b'), testDigest('2'), testDigest('e')),
	})
	appendEvidence(t, builder, trajectory.EvidenceInput{
		Type: trajectory.EventSubagentWorkspace, ActorID: "actor-host-0001", ParentEventIDs: []string{runtimeEvent.EventID},
		Payload: testSubagentWorkspacePayload(t, "child-research-0001", testDigest('e')),
	})
	inherited := testSubagentRuntimePayload(t, "child-research-0002", testDigest('a'), testDigest('b'), testDigest('2'), testDigest('e'))
	inherited.ParentLiveStateInherited = true
	if _, err := builder.Append(trajectory.EvidenceInput{
		Type: trajectory.EventSubagentRuntime, ActorID: "actor-host-0001",
		Payload: inherited,
	}); err == nil {
		t.Fatal("parent live runtime inheritance accepted")
	}
}

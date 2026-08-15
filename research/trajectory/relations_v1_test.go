package trajectory_test

import (
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/trajectory"
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
	intent := appendEvidence(t, builder, trajectory.EvidenceInput{Type: trajectory.EventEffectTransition, ActorID: "actor-host-0001", ParentEventIDs: []string{authority.EventID}, Payload: trajectory.EffectTransitionPayload{CallID: "call-source-decision-0001", State: trajectory.EffectIntent}})
	started := appendEvidence(t, builder, trajectory.EvidenceInput{Type: trajectory.EventEffectTransition, ActorID: "actor-host-0001", ParentEventIDs: []string{intent.EventID}, Payload: trajectory.EffectTransitionPayload{CallID: "call-source-decision-0001", State: trajectory.EffectStarted}})
	effect := appendEvidence(t, builder, trajectory.EvidenceInput{
		Type: trajectory.EventEffectTransition, ActorID: "actor-host-0001", ParentEventIDs: []string{started.EventID},
		Payload: trajectory.EffectTransitionPayload{CallID: "call-source-decision-0001", ReceiptID: "rcpt_source_decision_0001", State: trajectory.EffectCommitted},
	})
	tool := appendEvidence(t, builder, trajectory.EvidenceInput{Type: trajectory.EventToolDecision, ActorID: "actor-host-0001", ParentEventIDs: []string{authority.EventID, effect.EventID}, Payload: trajectory.ToolDecisionPayload{ApprovalDisposition: "not_required", ArgumentsSHA256: testDigest('4'), BrokerOutcome: "ok", CallID: "call-source-decision-0001", Capability: "workspace.read_text", CapabilityPlanSHA256: testDigest('e'), Mechanism: "direct", ReceiptID: "rcpt_source_decision_0001", ResultSHA256: testDigest('5')}})
	decision := appendEvidence(t, builder, trajectory.EvidenceInput{
		Type: trajectory.EventSourceDecision, ActorID: "actor-host-0001", ParentEventIDs: []string{occurrence.EventID, effect.EventID, tool.EventID},
		Payload: trajectory.SourceDecisionPayload{
			DecisionID: testDigest('d'), CapabilityPlanSHA256: testDigest('e'), OccurrenceID: testDigest('c'),
			DynamicOccurrence: 1, ClaimLevel: trajectory.ClaimSourceBound, Admitted: true,
			ReceiptID: "rcpt_source_decision_0001",
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
		Payload: trajectory.SubagentRuntimePayload{
			BriefSHA256: testDigest('f'), ChildID: "child-cross-plane-0002", ChildPlanSHA256: testDigest('2'), ContextSHA256: testDigest('e'), DescriptorSHA256: testDigest('3'),
			ExecutionProfileSHA256: testDigest('4'), FreshRunID: "run-cross-plane-0002", ParentWorkspaceRootSHA256: testDigest('5'), PreparedImageSHA256: testDigest('1'), SourceSHA256: testDigest('b'),
		},
	}); err == nil {
		t.Fatal("subagent runtime with mismatched child context accepted")
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
		Payload: trajectory.SubagentRuntimePayload{
			BriefSHA256: testDigest('b'), ChildID: "child-research-0001", ChildPlanSHA256: testDigest('d'), ContextSHA256: testDigest('a'), DescriptorSHA256: testDigest('3'),
			ExecutionProfileSHA256: testDigest('4'), FreshRunID: "run-child-research-0001", ParentWorkspaceRootSHA256: testDigest('e'), PreparedImageSHA256: testDigest('c'), SourceSHA256: testDigest('2'), ParentLiveStateInherited: false,
		},
	})
	appendEvidence(t, builder, trajectory.EvidenceInput{
		Type: trajectory.EventSubagentWorkspace, ActorID: "actor-host-0001", ParentEventIDs: []string{runtimeEvent.EventID},
		Payload: trajectory.SubagentWorkspacePayload{
			ChildID: "child-research-0001", BaseRootSHA256: testDigest('e'), ResultRootSHA256: testDigest('f'),
			ChangedEntries: 2, ChangedBytes: 128, Disposition: "selected",
		},
	})
	if _, err := builder.Append(trajectory.EvidenceInput{
		Type: trajectory.EventSubagentRuntime, ActorID: "actor-host-0001",
		Payload: trajectory.SubagentRuntimePayload{
			BriefSHA256: testDigest('b'), ChildID: "child-research-0002", ChildPlanSHA256: testDigest('d'), ContextSHA256: testDigest('a'), DescriptorSHA256: testDigest('3'),
			ExecutionProfileSHA256: testDigest('4'), FreshRunID: "run-child-research-0002", ParentWorkspaceRootSHA256: testDigest('e'), PreparedImageSHA256: testDigest('c'), SourceSHA256: testDigest('2'), ParentLiveStateInherited: true,
		},
	}); err == nil {
		t.Fatal("parent live runtime inheritance accepted")
	}
}

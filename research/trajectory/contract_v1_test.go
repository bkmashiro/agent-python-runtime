package trajectory_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/labstore"
	"github.com/bkmashiro/agent-python-runtime/research/trajectory"
)

func TestDualProfileProjectionPreservesCanonicalEventIdentityAndRemovesExperimentTelemetry(t *testing.T) {
	builder, err := trajectory.NewBuilder(trajectory.TraceHeader{
		TraceID: "trace-dual-profile-0001", SourceCommit: "0123456789abcdef0123456789abcdef01234567",
		RootExecutionID: "execution-dual-profile-0001",
	})
	if err != nil {
		t.Fatal(err)
	}
	started := appendEvidence(t, builder, trajectory.EvidenceInput{
		Type: trajectory.EventTraceStarted, ActorID: "actor-host-0001",
		Payload: trajectory.TraceStartedPayload{Status: "running"},
	})
	authority := appendEvidence(t, builder, trajectory.EvidenceInput{
		Type: trajectory.EventAuthoritySnapshot, ActorID: "actor-host-0001", ParentEventIDs: []string{started.EventID},
		Payload: trajectory.AuthoritySnapshotPayload{
			RunID: "run-dual-profile-0001", CapabilityPlanSHA256: testDigest('a'), PolicySHA256: testDigest('b'),
			FreshnessSHA256: testDigest('c'), GrantsSHA256: testDigest('d'),
		},
	})
	appendEvidence(t, builder, trajectory.EvidenceInput{
		Type: trajectory.EventModelContext, ActorID: "actor-harness-0001", ParentEventIDs: []string{started.EventID},
		Payload: trajectory.ModelContextPayload{ContextSHA256: testDigest('e'), BriefSHA256: testDigest('f'), Availability: trajectory.Available},
	})
	intent := appendEvidence(t, builder, trajectory.EvidenceInput{
		Type: trajectory.EventEffectTransition, ActorID: "actor-host-0001", ParentEventIDs: []string{authority.EventID},
		Payload: trajectory.EffectTransitionPayload{CallID: "call-effect-0001", State: trajectory.EffectIntent},
	})
	effect := appendEvidence(t, builder, trajectory.EvidenceInput{
		Type: trajectory.EventEffectTransition, ActorID: "actor-host-0001", ParentEventIDs: []string{intent.EventID},
		Payload: trajectory.EffectTransitionPayload{
			CallID: "call-effect-0001", State: trajectory.EffectReconciliationRequired,
			ReconciliationReason: "provider_outcome_ambiguous",
		},
	})
	appendEvidence(t, builder, trajectory.EvidenceInput{
		Type: trajectory.EventTraceEnded, ActorID: "actor-host-0001", ParentEventIDs: []string{effect.EventID},
		Payload: trajectory.TraceEndedPayload{Status: "completed", EvidenceComplete: true},
	})

	full, err := builder.Export(trajectory.ProfileExperimentFull, labstore.PrivacyPrivate)
	if err != nil {
		t.Fatal(err)
	}
	production, err := builder.Export(trajectory.ProfileProductionRollback, labstore.PrivacyPortable)
	if err != nil {
		t.Fatal(err)
	}
	if full.TraceID != production.TraceID || full.HeaderSHA256 != production.HeaderSHA256 {
		t.Fatalf("profile identity drift full=%+v production=%+v", full, production)
	}
	if len(full.Events) != 6 || len(production.Events) != 5 {
		t.Fatalf("full=%d production=%d", len(full.Events), len(production.Events))
	}
	fullIDs := map[string]bool{}
	for _, event := range full.Events {
		fullIDs[event.EventID] = true
	}
	for _, event := range production.Events {
		if !fullIDs[event.EventID] || event.Type == trajectory.EventModelContext || event.Body != nil {
			t.Fatalf("production leaked or rewrote event: %+v", event)
		}
	}
	if err := trajectory.ValidateEvidenceExport(full); err != nil {
		t.Fatal(err)
	}
	if err := trajectory.ValidateEvidenceExport(production); err != nil {
		t.Fatal(err)
	}
}

func TestEffectRollbackRequiresTypedCompensatorAndAmbiguityRequiresReconciliation(t *testing.T) {
	for name, payload := range map[string]trajectory.EffectTransitionPayload{
		"rollback without compensator":  {CallID: "call-effect-0001", State: trajectory.EffectCompensated},
		"ambiguous called rolled back":  {CallID: "call-effect-0001", State: trajectory.EffectCompensated, Compensator: "provider.rollback", ReconciliationReason: "provider_outcome_ambiguous"},
		"reconciliation without reason": {CallID: "call-effect-0001", State: trajectory.EffectReconciliationRequired},
	} {
		t.Run(name, func(t *testing.T) {
			builder := newEvidenceBuilder(t)
			if _, err := builder.Append(trajectory.EvidenceInput{Type: trajectory.EventEffectTransition, ActorID: "actor-host-0001", Payload: payload}); err == nil {
				t.Fatalf("invalid effect transition accepted: %+v", payload)
			}
		})
	}
	builder := newEvidenceBuilder(t)
	authority := appendEvidence(t, builder, trajectory.EvidenceInput{Type: trajectory.EventAuthoritySnapshot, ActorID: "actor-host-0001", Payload: trajectory.AuthoritySnapshotPayload{RunID: "run-effect-0001", CapabilityPlanSHA256: testDigest('1'), PolicySHA256: testDigest('2'), FreshnessSHA256: testDigest('3'), GrantsSHA256: testDigest('4')}})
	intent := appendEvidence(t, builder, trajectory.EvidenceInput{Type: trajectory.EventEffectTransition, ActorID: "actor-host-0001", ParentEventIDs: []string{authority.EventID}, Payload: trajectory.EffectTransitionPayload{CallID: "call-effect-0001", State: trajectory.EffectIntent}})
	started := appendEvidence(t, builder, trajectory.EvidenceInput{Type: trajectory.EventEffectTransition, ActorID: "actor-host-0001", ParentEventIDs: []string{intent.EventID}, Payload: trajectory.EffectTransitionPayload{CallID: "call-effect-0001", State: trajectory.EffectStarted}})
	committed := appendEvidence(t, builder, trajectory.EvidenceInput{Type: trajectory.EventEffectTransition, ActorID: "actor-host-0001", ParentEventIDs: []string{started.EventID}, Payload: trajectory.EffectTransitionPayload{CallID: "call-effect-0001", ReceiptID: "rcpt_effect_0001", State: trajectory.EffectCommitted}})
	if _, err := builder.Append(trajectory.EvidenceInput{Type: trajectory.EventEffectTransition, ActorID: "actor-host-0001", ParentEventIDs: []string{committed.EventID}, Payload: trajectory.EffectTransitionPayload{
		CallID: "call-effect-0001", State: trajectory.EffectCompensated, Compensator: "provider.rollback",
	}}); err != nil {
		t.Fatalf("typed compensation rejected: %v", err)
	}
}

func TestEvidenceRejectsFutureParentsUnknownPayloadsAndTamperedExports(t *testing.T) {
	builder := newEvidenceBuilder(t)
	if _, err := builder.Append(trajectory.EvidenceInput{
		Type: trajectory.EventTraceStarted, ActorID: "actor-host-0001", ParentEventIDs: []string{testDigest('9')},
		Payload: trajectory.TraceStartedPayload{Status: "running"},
	}); err == nil {
		t.Fatal("future parent accepted")
	}
	if _, err := builder.Append(trajectory.EvidenceInput{Type: trajectory.EvidenceType("unknown.event"), ActorID: "actor-host-0001", Payload: struct{}{}}); err == nil {
		t.Fatal("unknown event accepted")
	}
	appendEvidence(t, builder, trajectory.EvidenceInput{Type: trajectory.EventTraceStarted, ActorID: "actor-host-0001", Payload: trajectory.TraceStartedPayload{Status: "running"}})
	appendEvidence(t, builder, trajectory.EvidenceInput{Type: trajectory.EventTraceEnded, ActorID: "actor-host-0001", Payload: trajectory.TraceEndedPayload{Status: "completed", EvidenceComplete: true}})
	exported, err := builder.Export(trajectory.ProfileExperimentFull, labstore.PrivacyPrivate)
	if err != nil {
		t.Fatal(err)
	}
	tampered := exported
	tampered.Events = append([]trajectory.EvidenceEvent(nil), exported.Events...)
	tampered.Events[0].Payload = json.RawMessage(`{"status":"tampered"}`)
	if err := trajectory.ValidateEvidenceExport(tampered); err == nil {
		t.Fatal("tampered export accepted")
	}
	encoded, err := json.Marshal(exported)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip trajectory.Export
	if json.Unmarshal(encoded, &roundTrip) != nil || !reflect.DeepEqual(exported, roundTrip) {
		t.Fatal("export does not round trip exactly")
	}
}

func TestProductionProjectionRejectsCausalDependencyOnExperimentOnlyEvent(t *testing.T) {
	builder := newEvidenceBuilder(t)
	context := appendEvidence(t, builder, trajectory.EvidenceInput{
		Type: trajectory.EventModelContext, ActorID: "actor-harness-0001",
		Payload: trajectory.ModelContextPayload{ContextSHA256: testDigest('a'), BriefSHA256: testDigest('b'), Availability: trajectory.Available},
	})
	appendEvidence(t, builder, trajectory.EvidenceInput{
		Type: trajectory.EventExecutionAttempt, ActorID: "actor-host-0001", ParentEventIDs: []string{context.EventID},
		Payload: trajectory.ExecutionAttemptPayload{RunID: "run-contract-0001", AttemptID: "attempt-contract-0001", Status: "started"},
	})
	if _, err := builder.Export(trajectory.ProfileProductionRollback, labstore.PrivacyPortable); err == nil {
		t.Fatal("production projection accepted dependency on omitted experiment event")
	}
}

func TestEvidenceExportDecodeRequiresCanonicalExactDocument(t *testing.T) {
	builder := newEvidenceBuilder(t)
	appendEvidence(t, builder, trajectory.EvidenceInput{Type: trajectory.EventTraceStarted, ActorID: "actor-host-0001", Payload: trajectory.TraceStartedPayload{Status: "running"}})
	appendEvidence(t, builder, trajectory.EvidenceInput{Type: trajectory.EventTraceEnded, ActorID: "actor-host-0001", Payload: trajectory.TraceEndedPayload{Status: "completed", EvidenceComplete: true}})
	exported, err := builder.Export(trajectory.ProfileExperimentFull, labstore.PrivacyPrivate)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := trajectory.EncodeEvidenceExport(exported)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := trajectory.DecodeEvidenceExport(encoded)
	if err != nil || !reflect.DeepEqual(exported, decoded) {
		t.Fatalf("decoded=%+v err=%v", decoded, err)
	}
	withUnknown := append([]byte(nil), encoded[:len(encoded)-1]...)
	withUnknown = append(withUnknown, []byte(`,"unknown":true}`)...)
	if _, err := trajectory.DecodeEvidenceExport(withUnknown); err == nil {
		t.Fatal("unknown export field accepted")
	}
	indented, err := json.MarshalIndent(exported, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := trajectory.DecodeEvidenceExport(indented); err == nil {
		t.Fatal("noncanonical export accepted")
	}
}

func TestEvidenceLifecycleIsSingleAndTerminal(t *testing.T) {
	builder := newEvidenceBuilder(t)
	appendEvidence(t, builder, trajectory.EvidenceInput{
		Type: trajectory.EventTraceStarted, ActorID: "actor-host-0001",
		Payload: trajectory.TraceStartedPayload{Status: "running"},
	})
	appendEvidence(t, builder, trajectory.EvidenceInput{
		Type: trajectory.EventTraceEnded, ActorID: "actor-host-0001",
		Payload: trajectory.TraceEndedPayload{Status: "completed", EvidenceComplete: true},
	})
	_, err := builder.Append(trajectory.EvidenceInput{
		Type: trajectory.EventModelContext, ActorID: "actor-host-0001",
		Payload: trajectory.ModelContextPayload{ContextSHA256: testDigest('a'), BriefSHA256: testDigest('b'), Availability: trajectory.Available},
	})
	if err == nil {
		t.Fatal("post-terminal evidence accepted")
	}
}

func appendEvidence(t *testing.T, builder *trajectory.Builder, input trajectory.EvidenceInput) trajectory.EvidenceEvent {
	t.Helper()
	event, err := builder.Append(input)
	if err != nil {
		t.Fatal(err)
	}
	return event
}

func newEvidenceBuilder(t *testing.T) *trajectory.Builder {
	t.Helper()
	builder, err := trajectory.NewBuilder(trajectory.TraceHeader{
		TraceID: "trace-contract-0001", SourceCommit: "0123456789abcdef0123456789abcdef01234567",
		RootExecutionID: "execution-contract-0001",
	})
	if err != nil {
		t.Fatal(err)
	}
	return builder
}

func testDigest(value byte) string {
	return "sha256:" + string(make([]byte, 0)) + repeatByte(value, 64)
}

func repeatByte(value byte, count int) string {
	result := make([]byte, count)
	for index := range result {
		result[index] = value
	}
	return string(result)
}

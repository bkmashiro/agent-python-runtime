package trajectory_test

import (
	"path/filepath"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/labstore"
	"github.com/bkmashiro/agent-python-runtime/research/trajectory"
)

func TestPrivateBodyStaysInExperimentStoreAndPortableProjection(t *testing.T) {
	store, err := labstore.Open(filepath.Join(t.TempDir(), "store"), labstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	private, _, err := store.Put(labstore.KindPrompt, []byte("private model context"), labstore.PutOptions{
		Privacy: labstore.PrivacyPrivate, Credentials: labstore.CredentialsAbsent,
	})
	if err != nil {
		t.Fatal(err)
	}
	builder, err := trajectory.NewStoredBuilder(trajectory.TraceHeader{
		TraceID: "trace-body-policy-0001", SourceCommit: "0123456789abcdef0123456789abcdef01234567", RootExecutionID: "execution-body-policy-0001",
	}, store)
	if err != nil {
		t.Fatal(err)
	}
	appendEvidence(t, builder, trajectory.EvidenceInput{
		Type: trajectory.EventTraceStarted, ActorID: "actor-host-0001", Payload: trajectory.TraceStartedPayload{Status: "running"},
	})
	appendEvidence(t, builder, trajectory.EvidenceInput{
		Type: trajectory.EventModelContext, ActorID: "actor-harness-0001", Body: &private,
		Payload: trajectory.ModelContextPayload{ContextSHA256: testDigest('a'), BriefSHA256: testDigest('b'), Availability: trajectory.Available},
	})
	appendEvidence(t, builder, trajectory.EvidenceInput{
		Type: trajectory.EventTraceEnded, ActorID: "actor-host-0001", Payload: trajectory.TraceEndedPayload{Status: "completed", EvidenceComplete: true},
	})
	full, err := builder.Export(trajectory.ProfileExperimentFull, labstore.PrivacyPrivate)
	if err != nil || len(full.Events) != 3 || full.Events[1].Body == nil {
		t.Fatalf("full=%+v err=%v", full, err)
	}
	portable, err := builder.Export(trajectory.ProfileExperimentFull, labstore.PrivacyPortable)
	if err != nil || len(portable.Events) != 2 {
		t.Fatalf("portable=%+v err=%v", portable, err)
	}
	for _, event := range portable.Events {
		if event.Body != nil || event.Type == trajectory.EventModelContext {
			t.Fatalf("private body event leaked: %+v", event)
		}
	}
	production, err := builder.Export(trajectory.ProfileProductionRollback, labstore.PrivacyPortable)
	if err != nil || len(production.Events) != 2 {
		t.Fatalf("production=%+v err=%v", production, err)
	}
}

func TestBodyReferenceRequiresStoreAndExperimentOnlyEvent(t *testing.T) {
	builder := newEvidenceBuilder(t)
	ref := labstore.Ref{Kind: labstore.KindPrompt, SHA256: testDigest('a')}
	if _, err := builder.Append(trajectory.EvidenceInput{
		Type: trajectory.EventModelContext, ActorID: "actor-harness-0001", Body: &ref,
		Payload: trajectory.ModelContextPayload{ContextSHA256: testDigest('b'), BriefSHA256: testDigest('c'), Availability: trajectory.Available},
	}); err == nil {
		t.Fatal("unresolved body reference accepted")
	}
	if _, err := builder.Append(trajectory.EvidenceInput{
		Type: trajectory.EventEffectTransition, ActorID: "actor-host-0001", Body: &ref,
		Payload: trajectory.EffectTransitionPayload{CallID: "call-body-policy-0001", State: trajectory.EffectIntent},
	}); err == nil {
		t.Fatal("production event accepted body")
	}
}

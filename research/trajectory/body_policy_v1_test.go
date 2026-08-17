package trajectory_test

import (
	"crypto/sha256"
	"fmt"
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
	contextText, briefText := "private model context", "private model brief"
	contextDigest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(contextText)))
	briefDigest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(briefText)))
	private, _, err := store.PutJSON(labstore.KindMetadataEvent, []byte(`{"brief":"private model brief","context":"private model context"}`), labstore.PutOptions{
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
	context := appendEvidence(t, builder, trajectory.EvidenceInput{
		Type: trajectory.EventModelContext, ActorID: "actor-harness-0001",
		Payload: trajectory.ModelContextPayload{ContextSHA256: contextDigest, BriefSHA256: briefDigest, Availability: trajectory.Available},
	})
	appendEvidence(t, builder, trajectory.EvidenceInput{
		Type: trajectory.EventModelBody, ActorID: "actor-harness-0001", ParentEventIDs: []string{context.EventID}, Body: &private,
		Payload: trajectory.ModelContextPayload{ContextSHA256: contextDigest, BriefSHA256: briefDigest, Availability: trajectory.Available},
	})
	appendEvidence(t, builder, trajectory.EvidenceInput{
		Type: trajectory.EventTraceEnded, ActorID: "actor-host-0001", Payload: trajectory.TraceEndedPayload{Status: "completed", EvidenceComplete: true},
	})
	full, err := builder.Export(trajectory.ProfileExperimentFull, labstore.PrivacyPrivate)
	if err != nil || len(full.Events) != 4 || full.Events[2].Body == nil {
		t.Fatalf("full=%+v err=%v", full, err)
	}
	if err := trajectory.ValidateEvidenceExport(full); err == nil {
		t.Fatal("private body export validated without Labstore")
	}
	if err := trajectory.ValidateEvidenceExportWithStore(full, store); err != nil {
		t.Fatalf("store-bound private export rejected: %v", err)
	}
	encoded, err := trajectory.EncodeEvidenceExportWithStore(full, store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := trajectory.DecodeEvidenceExport(encoded); err == nil {
		t.Fatal("private body export decoded without Labstore")
	}
	if _, err := trajectory.DecodeEvidenceExportWithStore(encoded, store); err != nil {
		t.Fatalf("store-bound private decode rejected: %v", err)
	}
	portable, err := builder.Export(trajectory.ProfileExperimentFull, labstore.PrivacyPortable)
	if err != nil || len(portable.Events) != 3 {
		t.Fatalf("portable=%+v err=%v", portable, err)
	}
	for _, event := range portable.Events {
		if event.Body != nil || event.Type == trajectory.EventModelBody {
			t.Fatalf("private body event leaked: %+v", event)
		}
	}
	production, err := builder.Export(trajectory.ProfileProductionRollback, labstore.PrivacyPortable)
	if err != nil || len(production.Events) != 2 {
		t.Fatalf("production=%+v err=%v", production, err)
	}
}

func TestSourceBodyIsPrivateBoundAndBodySafe(t *testing.T) {
	store, err := labstore.Open(filepath.Join(t.TempDir(), "store"), labstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	source := []byte("print('captured')\n")
	sourceDigest := fmt.Sprintf("sha256:%x", sha256.Sum256(source))
	body, _, err := store.Put(labstore.KindCode, source, labstore.PutOptions{Privacy: labstore.PrivacyPrivate, Credentials: labstore.CredentialsAbsent})
	if err != nil {
		t.Fatal(err)
	}
	builder, err := trajectory.NewStoredBuilder(trajectory.TraceHeader{TraceID: "trace-source-body-0001", SourceCommit: "0123456789abcdef0123456789abcdef01234567", RootExecutionID: "execution-source-body-0001"}, store)
	if err != nil {
		t.Fatal(err)
	}
	appendEvidence(t, builder, trajectory.EvidenceInput{Type: trajectory.EventTraceStarted, ActorID: "actor-host-0001", Payload: trajectory.TraceStartedPayload{Status: "running"}})
	document := appendEvidence(t, builder, trajectory.EvidenceInput{Type: trajectory.EventSourceDocument, ActorID: "actor-harness-0001", Payload: trajectory.SourceDocumentPayload{DocumentID: testDigest('d'), SourceSHA256: sourceDigest, Availability: trajectory.Available}})
	if _, err := builder.Append(trajectory.EvidenceInput{Type: trajectory.EventSourceBody, ActorID: "actor-harness-0001", ParentEventIDs: []string{document.EventID}, Body: &body, Payload: trajectory.SourceBodyPayload{DocumentID: testDigest('d'), SourceSHA256: sourceDigest, DisplayPath: "../secret", Availability: trajectory.Available}}); err == nil {
		t.Fatal("unsafe source body path accepted")
	}
	wrongBody, _, err := store.Put(labstore.KindCode, []byte("print('wrong')\n"), labstore.PutOptions{Privacy: labstore.PrivacyPrivate, Credentials: labstore.CredentialsAbsent})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := builder.Append(trajectory.EvidenceInput{Type: trajectory.EventSourceBody, ActorID: "actor-harness-0001", ParentEventIDs: []string{document.EventID}, Body: &wrongBody, Payload: trajectory.SourceBodyPayload{DocumentID: testDigest('d'), SourceSHA256: sourceDigest, DisplayPath: "child_program.py", Availability: trajectory.Available}}); err == nil {
		t.Fatal("source body digest mismatch accepted")
	}
	appendEvidence(t, builder, trajectory.EvidenceInput{Type: trajectory.EventSourceBody, ActorID: "actor-harness-0001", ParentEventIDs: []string{document.EventID}, Body: &body, Payload: trajectory.SourceBodyPayload{DocumentID: testDigest('d'), SourceSHA256: sourceDigest, DisplayPath: "child_program.py", Availability: trajectory.Available}})
	appendEvidence(t, builder, trajectory.EvidenceInput{Type: trajectory.EventTraceEnded, ActorID: "actor-host-0001", Payload: trajectory.TraceEndedPayload{Status: "completed", EvidenceComplete: true}})
	private, err := builder.Export(trajectory.ProfileExperimentFull, labstore.PrivacyPrivate)
	if err != nil || len(private.Events) != 4 || private.Events[2].Body == nil {
		t.Fatalf("private=%+v err=%v", private, err)
	}
	portable, err := builder.Export(trajectory.ProfileExperimentFull, labstore.PrivacyPortable)
	if err != nil || len(portable.Events) != 3 {
		t.Fatalf("portable=%+v err=%v", portable, err)
	}
}

func TestAvailableModelOutputRequiresPrivateProviderBody(t *testing.T) {
	store, err := labstore.Open(filepath.Join(t.TempDir(), "store"), labstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	body, _, err := store.Put(labstore.KindProviderBody, []byte(`{"choices":[{"message":{"content":"public answer"}}]}`), labstore.PutOptions{Privacy: labstore.PrivacyPrivate, Credentials: labstore.CredentialsAbsent})
	if err != nil {
		t.Fatal(err)
	}
	builder, err := trajectory.NewStoredBuilder(trajectory.TraceHeader{TraceID: "trace-model-output-body-0001", SourceCommit: "0123456789abcdef0123456789abcdef01234567", RootExecutionID: "execution-model-output-body-0001"}, store)
	if err != nil {
		t.Fatal(err)
	}
	start := appendEvidence(t, builder, trajectory.EvidenceInput{Type: trajectory.EventTraceStarted, ActorID: "actor-host-0001", Payload: trajectory.TraceStartedPayload{Status: "running"}})
	outputDigest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte("public answer")))
	payload := trajectory.ModelOutputPayload{Availability: trajectory.Available, OutputSHA256: outputDigest}
	if _, err := builder.Append(trajectory.EvidenceInput{Type: trajectory.EventModelOutput, ActorID: "actor-harness-0001", ParentEventIDs: []string{start.EventID}, Payload: payload}); err == nil {
		t.Fatal("available model output without provider body accepted")
	}
	appendEvidence(t, builder, trajectory.EvidenceInput{Type: trajectory.EventModelOutput, ActorID: "actor-harness-0001", ParentEventIDs: []string{start.EventID}, Payload: payload, Body: &body})
	appendEvidence(t, builder, trajectory.EvidenceInput{Type: trajectory.EventTraceEnded, ActorID: "actor-host-0001", ParentEventIDs: []string{start.EventID}, Payload: trajectory.TraceEndedPayload{Status: "completed", EvidenceComplete: true}})
	private, err := builder.Export(trajectory.ProfileExperimentFull, labstore.PrivacyPrivate)
	if err != nil || len(private.Events) != 3 || private.Events[1].Body == nil {
		t.Fatalf("private=%+v err=%v", private, err)
	}
	portable, err := builder.Export(trajectory.ProfileExperimentFull, labstore.PrivacyPortable)
	if err != nil || len(portable.Events) != 2 {
		t.Fatalf("portable leaked provider body: %+v err=%v", portable, err)
	}
}

func TestBodyReferenceRequiresStoreAndExperimentOnlyEvent(t *testing.T) {
	builder := newEvidenceBuilder(t)
	ref := labstore.Ref{Kind: labstore.KindPrompt, SHA256: testDigest('a')}
	if _, err := builder.Append(trajectory.EvidenceInput{
		Type: trajectory.EventModelBody, ActorID: "actor-harness-0001", Body: &ref,
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

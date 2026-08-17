package labview

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bkmashiro/agent-python-runtime/research/labstore"
	"github.com/bkmashiro/agent-python-runtime/research/trajectory"
)

// RecordTaskExperiment materializes the body-complete, private experiment profile
// used by the debugger. Production evidence remains body-free.
func RecordTaskExperiment(root string, snapshot TaskSnapshot, sourceCommit string) (trajectory.Export, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return trajectory.Export{}, errors.New("task experiment root must be absolute and canonical")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return trajectory.Export{}, err
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return trajectory.Export{}, err
	}
	store, err := labstore.Open(filepath.Join(root, "store"), labstore.Options{})
	if err != nil {
		return trajectory.Export{}, err
	}
	defer store.Close()
	log, err := trajectory.CreateEvidenceLog(filepath.Join(root, "trace.jsonl"), store, trajectory.TraceHeader{
		TraceID: "trace:lab-release-readiness", RootExecutionID: "run:lab-release-readiness", SourceCommit: sourceCommit,
	}, trajectory.EvidenceLimits{})
	if err != nil {
		return trajectory.Export{}, err
	}
	defer log.Close()
	start, err := log.Append(trajectory.EvidenceInput{Type: trajectory.EventTraceStarted, ActorID: "actor:host-runtime", Payload: trajectory.TraceStartedPayload{Status: "running"}})
	if err != nil {
		return trajectory.Export{}, err
	}
	if _, err = log.Append(trajectory.EvidenceInput{Type: trajectory.EventModelOutput, ActorID: "actor:host-runtime", ParentEventIDs: []string{start.EventID}, Payload: trajectory.ModelOutputPayload{Availability: trajectory.NotRecorded}}); err != nil {
		return trajectory.Export{}, fmt.Errorf("record model availability: %w", err)
	}
	contextBody, err := json.Marshal(struct {
		Task    string      `json:"task"`
		Context TaskContext `json:"context"`
	}{Task: snapshot.Task, Context: snapshot.Context})
	if err != nil {
		return trajectory.Export{}, err
	}
	if _, err := appendTaskObservation(log, store, start.EventID, "actor:orchestrator", "task.context", 1, nil, contextBody); err != nil {
		return trajectory.Export{}, fmt.Errorf("record task context: %w", err)
	}

	// Preserve the complete Host/runtime flow as content-addressed observation
	// bodies while retaining the original parent-sequence relation.
	runtimeEvents := make(map[int]trajectory.EvidenceEvent, len(snapshot.Events))
	for _, runtimeEvent := range snapshot.Events {
		parentID := start.EventID
		var parentSequence *uint32
		if runtimeEvent.ParentSequence != 0 {
			parent, ok := runtimeEvents[runtimeEvent.ParentSequence]
			if !ok {
				return trajectory.Export{}, errors.New("task experiment runtime parent is unavailable")
			}
			parentID = parent.EventID
			value := uint32(runtimeEvent.ParentSequence)
			parentSequence = &value
		}
		body, marshalErr := json.Marshal(runtimeEvent)
		if marshalErr != nil {
			return trajectory.Export{}, marshalErr
		}
		recorded, recordErr := appendTaskObservation(log, store, parentID, "actor:"+runtimeEvent.AgentID, "runtime.event", uint32(runtimeEvent.Sequence), parentSequence, body)
		if recordErr != nil {
			return trajectory.Export{}, fmt.Errorf("record runtime event %d: %w", runtimeEvent.Sequence, recordErr)
		}
		runtimeEvents[runtimeEvent.Sequence] = recorded
	}

	for _, source := range snapshot.Sources {
		ref, _, putErr := store.Put(labstore.KindCode, []byte(source.Source), labstore.PutOptions{Privacy: labstore.PrivacyPrivate, Credentials: labstore.CredentialsAbsent})
		if putErr != nil {
			return trajectory.Export{}, putErr
		}
		documentID := latestSHA([]byte("document\x00" + source.ID + "\x00" + source.Source))
		document, appendErr := log.Append(trajectory.EvidenceInput{Type: trajectory.EventSourceDocument, ActorID: "actor:" + source.ID, ParentEventIDs: []string{start.EventID}, Payload: trajectory.SourceDocumentPayload{DocumentID: documentID, SourceSHA256: latestSHA([]byte(source.Source)), Availability: trajectory.Available}})
		if appendErr != nil {
			return trajectory.Export{}, fmt.Errorf("record source document %s: %w", source.ID, appendErr)
		}
		if _, appendErr = log.Append(trajectory.EvidenceInput{Type: trajectory.EventSourceBody, ActorID: "actor:" + source.ID, ParentEventIDs: []string{document.EventID}, Body: &ref, Payload: trajectory.SourceBodyPayload{DocumentID: documentID, SourceSHA256: latestSHA([]byte(source.Source)), DisplayPath: source.File, Availability: trajectory.Available}}); appendErr != nil {
			return trajectory.Export{}, fmt.Errorf("record source body %s: %w", source.ID, appendErr)
		}
	}
	for _, output := range snapshot.Outputs {
		parent := runtimeEvents[output.EventSequence]
		if parent.EventID == "" {
			return trajectory.Export{}, fmt.Errorf("record output %s: runtime event is unavailable", output.AgentID)
		}
		parentSequence := uint32(output.EventSequence)
		if _, err := appendTaskObservation(log, store, parent.EventID, "actor:"+output.AgentID, "agent.output", uint32(output.EventSequence+len(snapshot.Events)), &parentSequence, []byte(output.Body)); err != nil {
			return trajectory.Export{}, fmt.Errorf("record output %s: %w", output.AgentID, err)
		}
	}
	if _, err = log.Append(trajectory.EvidenceInput{Type: trajectory.EventTraceEnded, ActorID: "actor:host-runtime", ParentEventIDs: []string{start.EventID}, Payload: trajectory.TraceEndedPayload{Status: "completed", EvidenceComplete: true}}); err != nil {
		return trajectory.Export{}, err
	}
	exported, err := log.Export(trajectory.ProfileExperimentFull, labstore.PrivacyPrivate)
	if err != nil {
		return trajectory.Export{}, err
	}
	if err := trajectory.ValidateEvidenceExportWithStore(exported, store); err != nil {
		return trajectory.Export{}, err
	}
	bodyEvents := 0
	for _, event := range exported.Events {
		if event.Body != nil {
			bodyEvents++
		}
	}
	expectedBodies := len(snapshot.Events) + len(snapshot.Sources) + len(snapshot.Outputs) + 1
	if bodyEvents != expectedBodies {
		return trajectory.Export{}, fmt.Errorf("task experiment body coverage = %d, want %d", bodyEvents, expectedBodies)
	}
	return exported, nil
}

func appendTaskObservation(log *trajectory.EvidenceLog, store *labstore.Store, parentID, actorID, observationType string, sequence uint32, parentSequence *uint32, body []byte) (trajectory.EvidenceEvent, error) {
	envelope, err := json.Marshal(struct {
		Body string `json:"body"`
	}{Body: string(body)})
	if err != nil {
		return trajectory.EvidenceEvent{}, err
	}
	ref, _, err := store.PutJSON(labstore.KindMetadataEvent, envelope, labstore.PutOptions{Privacy: labstore.PrivacyPrivate, Credentials: labstore.CredentialsAbsent})
	if err != nil {
		return trajectory.EvidenceEvent{}, err
	}
	stored, err := store.Get(ref)
	if err != nil {
		return trajectory.EvidenceEvent{}, err
	}
	return log.Append(trajectory.EvidenceInput{Type: trajectory.EventRuntimeObservation, ActorID: actorID, ParentEventIDs: []string{parentID}, Body: &ref, Payload: trajectory.RuntimeObservationPayload{ObservationType: observationType, Sequence: sequence, ParentSequence: parentSequence, ObservationSHA256: latestSHA(stored.Body)}})
}

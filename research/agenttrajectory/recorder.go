package agenttrajectory

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/bkmashiro/agent-python-runtime/research/labstore"
	"github.com/bkmashiro/agent-python-runtime/research/trajectory"
)

type PrivateRecorder struct {
	mu       sync.Mutex
	store    *labstore.Store
	log      *trajectory.EvidenceLog
	startID  string
	complete bool
	closed   bool
}

func NewPrivateRecorder(root, sourceCommit string) (*PrivateRecorder, error) {
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return nil, errors.New("invalid private trajectory root")
	}
	if err := os.Mkdir(root, 0o700); err != nil {
		return nil, err
	}
	store, err := labstore.Open(filepath.Join(root, "store"), labstore.Options{})
	if err != nil {
		return nil, err
	}
	log, err := trajectory.CreateEvidenceLog(filepath.Join(root, "trajectory.jsonl"), store, trajectory.TraceHeader{
		TraceID: "trace-day-trip-agent-0001", SourceCommit: sourceCommit, RootExecutionID: "execution-day-trip-agent-0001",
	}, trajectory.EvidenceLimits{})
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	start, err := log.Append(trajectory.EvidenceInput{Type: trajectory.EventTraceStarted, ActorID: "actor-host-harness", Payload: trajectory.TraceStartedPayload{Status: "running"}})
	if err != nil {
		_ = log.Close()
		_ = store.Close()
		return nil, err
	}
	return &PrivateRecorder{store: store, log: log, startID: start.EventID}, nil
}

func (recorder *PrivateRecorder) RecordModelCall(_ context.Context, request ModelRequest, result ModelResult) error {
	if recorder == nil {
		return errors.New("private trajectory recorder is nil")
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if recorder.closed || recorder.complete || recorder.store == nil || recorder.log == nil || result.CallID != request.CallID || result.ActorID != request.ActorID || stringsTrim(result.Content) == "" || len(result.RawRequest) == 0 || len(result.RawResponse) == 0 || !validProviderIdentifier(request.CallID) || !validProviderIdentifier(request.ActorID) || !validMessages(request.Messages) {
		return errors.New("invalid private model call recording")
	}
	brief := request.Messages[len(request.Messages)-1].Content
	contextText := string(result.RawRequest)
	contextSHA := plainSHA256([]byte(contextText))
	briefSHA := plainSHA256([]byte(brief))
	metadataBody, err := json.Marshal(struct {
		Brief   string `json:"brief"`
		Context string `json:"context"`
	}{Brief: brief, Context: contextText})
	if err != nil {
		return err
	}
	contextRef, _, err := recorder.store.PutJSON(labstore.KindMetadataEvent, metadataBody, labstore.PutOptions{Privacy: labstore.PrivacyPrivate, Credentials: labstore.CredentialsAbsent})
	if err != nil {
		return err
	}
	providerRef, _, err := recorder.store.Put(labstore.KindProviderBody, result.RawResponse, labstore.PutOptions{Privacy: labstore.PrivacyPrivate, Credentials: labstore.CredentialsAbsent})
	if err != nil {
		return err
	}
	actor := "actor-" + request.ActorID
	contextEvent, err := recorder.log.Append(trajectory.EvidenceInput{
		Type: trajectory.EventModelContext, ActorID: actor, ParentEventIDs: []string{recorder.startID},
		Payload: trajectory.ModelContextPayload{ContextSHA256: contextSHA, BriefSHA256: briefSHA, Availability: trajectory.Available},
	})
	if err != nil {
		return err
	}
	bodyEvent, err := recorder.log.Append(trajectory.EvidenceInput{
		Type: trajectory.EventModelBody, ActorID: actor, ParentEventIDs: []string{contextEvent.EventID}, Body: &contextRef,
		Payload: trajectory.ModelContextPayload{ContextSHA256: contextSHA, BriefSHA256: briefSHA, Availability: trajectory.Available},
	})
	if err != nil {
		return err
	}
	_, err = recorder.log.Append(trajectory.EvidenceInput{
		Type: trajectory.EventModelOutput, ActorID: actor, ParentEventIDs: []string{bodyEvent.EventID}, Body: &providerRef,
		Payload: trajectory.ModelOutputPayload{Availability: trajectory.Available, OutputSHA256: plainSHA256([]byte(result.Content))},
	})
	return err
}

func (recorder *PrivateRecorder) Complete(_ context.Context) (trajectory.Export, error) {
	if recorder == nil {
		return trajectory.Export{}, errors.New("private trajectory recorder is nil")
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if recorder.closed || recorder.complete || recorder.log == nil {
		return trajectory.Export{}, errors.New("private trajectory recorder is terminal")
	}
	if _, err := recorder.log.Append(trajectory.EvidenceInput{Type: trajectory.EventTraceEnded, ActorID: "actor-host-harness", ParentEventIDs: []string{recorder.startID}, Payload: trajectory.TraceEndedPayload{Status: "completed", EvidenceComplete: true}}); err != nil {
		return trajectory.Export{}, err
	}
	recorder.complete = true
	return recorder.log.Export(trajectory.ProfileExperimentFull, labstore.PrivacyPrivate)
}

func (recorder *PrivateRecorder) Close() error {
	if recorder == nil {
		return nil
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	if recorder.closed {
		return nil
	}
	recorder.closed = true
	var logErr, storeErr error
	if recorder.log != nil {
		logErr = recorder.log.Close()
		recorder.log = nil
	}
	if recorder.store != nil {
		storeErr = recorder.store.Close()
		recorder.store = nil
	}
	return errors.Join(logErr, storeErr)
}

func plainSHA256(body []byte) string {
	return fmt.Sprintf("sha256:%x", sha256.Sum256(body))
}

func stringsTrim(value string) string {
	for len(value) > 0 && (value[0] == ' ' || value[0] == '\n' || value[0] == '\r' || value[0] == '\t') {
		value = value[1:]
	}
	for len(value) > 0 {
		last := value[len(value)-1]
		if last != ' ' && last != '\n' && last != '\r' && last != '\t' {
			break
		}
		value = value[:len(value)-1]
	}
	return value
}

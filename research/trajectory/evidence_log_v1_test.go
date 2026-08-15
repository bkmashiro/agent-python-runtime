package trajectory_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/labstore"
	"github.com/bkmashiro/agent-python-runtime/research/trajectory"
)

func TestEvidenceLogRoundTripAndTamperRejection(t *testing.T) {
	root := t.TempDir()
	store, err := labstore.Open(filepath.Join(root, "store"), labstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	path := filepath.Join(root, "evidence.jsonl")
	header := trajectory.TraceHeader{TraceID: "trace-log-0001", SourceCommit: "0123456789abcdef0123456789abcdef01234567", RootExecutionID: "execution-log-0001"}
	log, err := trajectory.CreateEvidenceLog(path, store, header, trajectory.EvidenceLimits{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := log.Append(trajectory.EvidenceInput{Type: trajectory.EventTraceStarted, ActorID: "actor-host-0001", Payload: trajectory.TraceStartedPayload{Status: "running"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := log.Append(trajectory.EvidenceInput{Type: trajectory.EventTraceEnded, ActorID: "actor-host-0001", Payload: trajectory.TraceEndedPayload{Status: "completed", EvidenceComplete: true}}); err != nil {
		t.Fatal(err)
	}
	want, err := log.Export(trajectory.ProfileExperimentFull, labstore.PrivacyPrivate)
	if err != nil {
		t.Fatal(err)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v err=%v", info.Mode().Perm(), err)
	}
	reopened, err := trajectory.OpenEvidenceLog(path, store, trajectory.EvidenceLimits{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := reopened.Export(trajectory.ProfileExperimentFull, labstore.PrivacyPrivate)
	if err != nil || !bytes.Equal(mustEncodeEvidence(t, want), mustEncodeEvidence(t, got)) {
		t.Fatalf("roundtrip err=%v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tampered := bytes.Replace(raw, []byte(`"running"`), []byte(`"failed"`), 1)
	if bytes.Equal(raw, tampered) {
		t.Fatal("fixture did not contain expected payload")
	}
	if err := os.WriteFile(path, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if opened, err := trajectory.OpenEvidenceLog(path, store, trajectory.EvidenceLimits{}); err == nil {
		opened.Close()
		t.Fatal("tampered evidence log accepted")
	}
}

func mustEncodeEvidence(t *testing.T, exported trajectory.Export) []byte {
	t.Helper()
	encoded, err := trajectory.EncodeEvidenceExport(exported)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

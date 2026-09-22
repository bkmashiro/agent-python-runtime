package workspace

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckpointHandsWorkspaceToNextAttempt(t *testing.T) {
	manager := newTestManager(t)
	ref, err := manager.Create([]InitialFile{{Path: "state.json", Data: []byte(`{"step":1}`)}}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	lease, err := manager.Acquire(ref, "attempt-1")
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := lease.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) == 0 || checkpoint.Workspace != ref || checkpoint.Revision == "" {
		t.Fatalf("checkpoint=%+v json=%s", checkpoint, encoded)
	}
	var persisted Checkpoint
	if err := json.Unmarshal(encoded, &persisted); err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}

	next, err := manager.AcquireCheckpoint(persisted, "attempt-2")
	if err != nil {
		t.Fatal(err)
	}
	defer next.Release()
	file, err := next.ReadFile("state.json", 64)
	if err != nil || string(file.Data) != `{"step":1}` {
		t.Fatalf("file=%q err=%v", file.Data, err)
	}
}

func TestAcquireCheckpointRejectsChangedWorkspaceAndReleasesLease(t *testing.T) {
	manager := newTestManager(t)
	ref, err := manager.Create([]InitialFile{{Path: "state.txt", Data: []byte("before")}}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	lease, err := manager.Acquire(ref, "attempt-1")
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := lease.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manager.entries[ref].root, "state.txt"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := manager.AcquireCheckpoint(checkpoint, "attempt-2"); !errors.Is(err, ErrCheckpointMismatch) {
		t.Fatalf("changed checkpoint err=%v", err)
	}
	// A failed handoff must not strand the workspace as busy.
	recovery, err := manager.Acquire(ref, "recovery")
	if err != nil {
		t.Fatal(err)
	}
	if err := recovery.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestCheckpointRejectsActiveGuest(t *testing.T) {
	manager := newTestManager(t)
	ref, err := manager.Create(nil, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	lease, err := manager.Acquire(ref, "attempt")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	if _, _, err := lease.BeginRun(); err != nil {
		t.Fatal(err)
	}
	if _, err := lease.Checkpoint(); !errors.Is(err, ErrWorkspaceBusy) {
		t.Fatalf("active checkpoint err=%v", err)
	}
	lease.EndRun()
}

func TestAcquireCheckpointRejectsMalformedToken(t *testing.T) {
	manager := newTestManager(t)
	for _, checkpoint := range []Checkpoint{
		{},
		{SchemaVersion: CheckpointSchemaVersion + 1, Workspace: Ref("ws-00000000000000000000000000000000"), Revision: Revision("sha256:invalid")},
	} {
		if _, err := manager.AcquireCheckpoint(checkpoint, "attempt"); !errors.Is(err, ErrInvalidCheckpoint) {
			t.Fatalf("checkpoint=%+v err=%v", checkpoint, err)
		}
	}
}

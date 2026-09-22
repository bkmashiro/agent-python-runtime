package workspace

import (
	"errors"
	"fmt"
)

const CheckpointSchemaVersion = 1

var (
	ErrInvalidCheckpoint  = errors.New("invalid workspace checkpoint")
	ErrCheckpointMismatch = errors.New("workspace checkpoint revision mismatch")
)

// Checkpoint is a bounded handoff token for a workspace managed by this Host.
// It identifies expected state but contains neither file contents nor a backup.
type Checkpoint struct {
	SchemaVersion int      `json:"schema_version"`
	Workspace     Ref      `json:"workspace"`
	Revision      Revision `json:"revision"`
}

// Checkpoint records the workspace revision between Guest runs while this lease
// still has exclusive ownership.
func (lease *Lease) Checkpoint() (Checkpoint, error) {
	if lease == nil {
		return Checkpoint{}, ErrInvalidWorkspace
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.released {
		return Checkpoint{}, ErrWorkspaceClosed
	}
	if lease.running {
		return Checkpoint{}, ErrWorkspaceBusy
	}
	snapshot, err := lease.snapshotLocked()
	if err != nil {
		return Checkpoint{}, err
	}
	return Checkpoint{
		SchemaVersion: CheckpointSchemaVersion,
		Workspace:     lease.ref,
		Revision:      snapshot.Revision,
	}, nil
}

// AcquireCheckpoint transfers a known workspace revision to a new owner. The
// workspace bytes remain local to this Manager; missing or changed state is
// rejected instead of being silently treated as a valid continuation.
func (manager *Manager) AcquireCheckpoint(checkpoint Checkpoint, owner string) (*Lease, error) {
	if checkpoint.SchemaVersion != CheckpointSchemaVersion || !validRef(checkpoint.Workspace) || !validRevision(checkpoint.Revision) {
		return nil, ErrInvalidCheckpoint
	}
	lease, err := manager.Acquire(checkpoint.Workspace, owner)
	if err != nil {
		return nil, err
	}
	snapshot, snapshotErr := lease.Snapshot()
	if snapshotErr == nil && snapshot.Revision == checkpoint.Revision {
		return lease, nil
	}
	releaseErr := lease.Release()
	if snapshotErr != nil {
		return nil, errors.Join(snapshotErr, releaseErr)
	}
	return nil, errors.Join(fmt.Errorf("%w: expected %s, found %s", ErrCheckpointMismatch, checkpoint.Revision, snapshot.Revision), releaseErr)
}

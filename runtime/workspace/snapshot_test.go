package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSnapshotRevisionIsStableAcrossRootsAndInputOrder(t *testing.T) {
	filesA := []InitialFile{
		{Path: "src/main.py", Data: []byte("print('ok')\n")},
		{Path: "README.md", Data: []byte("hello\n")},
		{Path: "bin/run", Data: []byte("#!/bin/sh\n"), Executable: true},
	}
	filesB := []InitialFile{filesA[2], filesA[0], filesA[1]}
	first := snapshotFixture(t, filesA)
	second := snapshotFixture(t, filesB)

	if first.Revision == "" || first.Revision != second.Revision {
		t.Fatalf("revisions differ: %q != %q", first.Revision, second.Revision)
	}
	if !reflect.DeepEqual(first.Files, second.Files) {
		t.Fatalf("files differ:\n%#v\n%#v", first.Files, second.Files)
	}
	if first.FileCount != 3 || first.Bytes != uint64(len("print('ok')\n")+len("hello\n")+len("#!/bin/sh\n")) {
		t.Fatalf("unexpected totals: %#v", first)
	}
	if got := first.Files[0]; got.Path != "README.md" || got.Size != 6 || got.SHA256 != digest("hello\n") || got.Executable {
		t.Fatalf("unexpected first file: %#v", got)
	}
	if got := first.Files[1]; got.Path != "bin/run" || !got.Executable {
		t.Fatalf("executable metadata lost: %#v", got)
	}
}

func TestDiffReportsAddedModifiedAndDeletedWithoutContents(t *testing.T) {
	manager, lease := newSnapshotLease(t, []InitialFile{
		{Path: "delete.txt", Data: []byte("old")},
		{Path: "keep.txt", Data: []byte("same")},
		{Path: "modify.txt", Data: []byte("before")},
	})
	defer manager.Close()
	defer lease.Release()

	before, err := lease.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(lease.root, "delete.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lease.root, "modify.txt"), []byte("after"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lease.root, "added.txt"), []byte("new"), 0o700); err != nil {
		t.Fatal(err)
	}
	after, err := lease.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	changes := Diff(before, after)
	want := []Change{
		{Path: "added.txt", Kind: ChangeAdded, After: &FileState{Path: "added.txt", Size: 3, SHA256: digest("new"), Executable: true}},
		{Path: "delete.txt", Kind: ChangeDeleted, Before: &FileState{Path: "delete.txt", Size: 3, SHA256: digest("old")}},
		{Path: "modify.txt", Kind: ChangeModified, Before: &FileState{Path: "modify.txt", Size: 6, SHA256: digest("before")}, After: &FileState{Path: "modify.txt", Size: 5, SHA256: digest("after")}},
	}
	if !reflect.DeepEqual(changes.Changes, want) || changes.Added != 1 || changes.Modified != 1 || changes.Deleted != 1 {
		t.Fatalf("changes=%#v want=%#v", changes, want)
	}
	if changes.Before != before.Revision || changes.After != after.Revision {
		t.Fatalf("revision boundary lost: %#v", changes)
	}
}

func TestSnapshotFailsWhileLeaseIsRunning(t *testing.T) {
	manager, lease := newSnapshotLease(t, []InitialFile{{Path: "a.txt", Data: []byte("a")}})
	defer manager.Close()
	defer lease.Release()
	_, _, err := lease.BeginRun()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.EndRun()
	if _, err := lease.Snapshot(); !errors.Is(err, ErrWorkspaceBusy) {
		t.Fatalf("snapshot error=%v, want ErrWorkspaceBusy", err)
	}
}

func snapshotFixture(t *testing.T, files []InitialFile) Snapshot {
	t.Helper()
	manager, lease := newSnapshotLease(t, files)
	t.Cleanup(func() {
		_ = lease.Release()
		_ = manager.Close()
	})
	snapshot, err := lease.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func newSnapshotLease(t *testing.T, files []InitialFile) (*Manager, *Lease) {
	t.Helper()
	base := filepath.Join(t.TempDir(), "workspaces")
	if err := os.Mkdir(base, 0o700); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(base)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := manager.Create(files, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	lease, err := manager.Acquire(ref, "snapshot-test")
	if err != nil {
		t.Fatal(err)
	}
	return manager, lease
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

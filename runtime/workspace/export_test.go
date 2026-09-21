package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestExportChangesIncludesOnlyChangedContents(t *testing.T) {
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

	bundle, err := lease.ExportChanges(before, 8)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.SchemaVersion != ChangeSetSchemaVersion || bundle.Before != before.Revision || bundle.After == before.Revision || bundle.Bytes != 8 {
		t.Fatalf("unexpected bundle metadata: %#v", bundle)
	}
	want := []ExportedChange{
		{Path: "added.txt", Kind: ChangeAdded, After: &FileState{Path: "added.txt", Size: 3, SHA256: digest("new"), Executable: true}, Data: []byte("new")},
		{Path: "delete.txt", Kind: ChangeDeleted, Before: &FileState{Path: "delete.txt", Size: 3, SHA256: digest("old")}},
		{Path: "modify.txt", Kind: ChangeModified, Before: &FileState{Path: "modify.txt", Size: 6, SHA256: digest("before")}, After: &FileState{Path: "modify.txt", Size: 5, SHA256: digest("after")}, Data: []byte("after")},
	}
	if !reflect.DeepEqual(bundle.Changes, want) {
		t.Fatalf("changes=%#v want=%#v", bundle.Changes, want)
	}
	if err := bundle.Validate(DefaultLimits(), 8); err != nil {
		t.Fatalf("validate exported bundle: %v", err)
	}
}

func TestExportChangesRejectsInvalidBaselineAndByteOverflow(t *testing.T) {
	manager, lease := newSnapshotLease(t, []InitialFile{{Path: "file.txt", Data: []byte("before")}})
	defer manager.Close()
	defer lease.Release()
	before, err := lease.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lease.root, "file.txt"), []byte("after"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := lease.ExportChanges(before, 4); !errors.Is(err, ErrExportTooLarge) {
		t.Fatalf("small export limit err=%v", err)
	}
	tampered := before
	tampered.Files[0].SHA256 = digest("other")
	if _, err := lease.ExportChanges(tampered, 16); !errors.Is(err, ErrInvalidChangeSet) {
		t.Fatalf("tampered baseline err=%v", err)
	}
}

func TestExportChangesFailsWhileLeaseIsRunning(t *testing.T) {
	manager, lease := newSnapshotLease(t, []InitialFile{{Path: "file.txt", Data: []byte("before")}})
	defer manager.Close()
	defer lease.Release()
	before, err := lease.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := lease.BeginRun(); err != nil {
		t.Fatal(err)
	}
	defer lease.EndRun()
	if _, err := lease.ExportChanges(before, 16); !errors.Is(err, ErrWorkspaceBusy) {
		t.Fatalf("running export err=%v", err)
	}
}

func TestChangeSetValidationRejectsTampering(t *testing.T) {
	limits := DefaultLimits()
	valid := ChangeSet{
		SchemaVersion: ChangeSetSchemaVersion,
		Before:        Revision("sha256:" + strings.Repeat("a", 64)),
		After:         Revision("sha256:" + strings.Repeat("b", 64)),
		Bytes:         3,
		Changes: []ExportedChange{{
			Path: "new.txt", Kind: ChangeAdded,
			After: &FileState{Path: "new.txt", Size: 3, SHA256: digest("new")},
			Data:  []byte("new"),
		}},
	}
	if err := valid.Validate(limits, 3); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*ChangeSet){
		"digest": func(bundle *ChangeSet) { bundle.Changes[0].After.SHA256 = digest("bad") },
		"path":   func(bundle *ChangeSet) { bundle.Changes[0].After.Path = "other.txt" },
		"bytes":  func(bundle *ChangeSet) { bundle.Bytes++ },
		"limit":  func(bundle *ChangeSet) { bundle.Changes[0].Data = []byte("long") },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := cloneChangeSet(valid)
			mutate(&candidate)
			if err := candidate.Validate(limits, 3); !errors.Is(err, ErrInvalidChangeSet) && !errors.Is(err, ErrExportTooLarge) {
				t.Fatalf("validation err=%v", err)
			}
		})
	}
}

func TestCheckDirectoryConflictsCleanAndUnrelatedChanges(t *testing.T) {
	target := t.TempDir()
	writeTargetFile(t, target, "delete.txt", "old", false)
	writeTargetFile(t, target, "keep.txt", "host changed independently", false)
	writeTargetFile(t, target, "modify.txt", "before", false)

	bundle := exportFixture(t)
	report, err := CheckDirectoryConflicts(target, bundle, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if !report.Clean || len(report.Conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %#v", report)
	}
}

func TestCheckDirectoryConflictsReportsChangedMissingAndExistingPaths(t *testing.T) {
	target := t.TempDir()
	writeTargetFile(t, target, "delete.txt", "changed", false)
	writeTargetFile(t, target, "added.txt", "occupied", false)
	// modify.txt is intentionally missing.

	report, err := CheckDirectoryConflicts(target, exportFixture(t), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	want := []Conflict{
		{Path: "added.txt", Kind: ConflictAlreadyExists},
		{Path: "delete.txt", Kind: ConflictChanged},
		{Path: "modify.txt", Kind: ConflictMissing},
	}
	if report.Clean || !reflect.DeepEqual(report.Conflicts, want) {
		t.Fatalf("conflicts=%#v want=%#v", report.Conflicts, want)
	}
}

func TestCheckDirectoryConflictsRejectsUnsafeTarget(t *testing.T) {
	target := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(target, "modify.txt")); err != nil {
		t.Fatal(err)
	}
	writeTargetFile(t, target, "delete.txt", "old", false)

	report, err := CheckDirectoryConflicts(target, exportFixture(t), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	want := []Conflict{
		{Path: "modify.txt", Kind: ConflictUnsupported},
	}
	if report.Clean || !reflect.DeepEqual(report.Conflicts, want) {
		t.Fatalf("conflicts=%#v want=%#v", report.Conflicts, want)
	}
}

func exportFixture(t *testing.T) ChangeSet {
	t.Helper()
	manager, lease := newSnapshotLease(t, []InitialFile{
		{Path: "delete.txt", Data: []byte("old")},
		{Path: "keep.txt", Data: []byte("same")},
		{Path: "modify.txt", Data: []byte("before")},
	})
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
	if err := os.WriteFile(filepath.Join(lease.root, "added.txt"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	bundle, err := lease.ExportChanges(before, 8)
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	return bundle
}

func cloneChangeSet(source ChangeSet) ChangeSet {
	clone := source
	clone.Changes = make([]ExportedChange, len(source.Changes))
	for index, change := range source.Changes {
		clone.Changes[index] = change
		clone.Changes[index].Data = append([]byte(nil), change.Data...)
		if change.Before != nil {
			value := *change.Before
			clone.Changes[index].Before = &value
		}
		if change.After != nil {
			value := *change.After
			clone.Changes[index].After = &value
		}
	}
	return clone
}

func writeTargetFile(t *testing.T, root, name, contents string, executable bool) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatal(err)
	}
	mode := os.FileMode(0o600)
	if executable {
		mode = 0o700
	}
	if err := os.WriteFile(full, []byte(contents), mode); err != nil {
		t.Fatal(err)
	}
}

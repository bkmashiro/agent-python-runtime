package workspace

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
)

func capsuleFixture(t *testing.T, manager *Manager, reverse bool) Ref {
	t.Helper()
	files := []InitialFile{
		{Path: "bin/tool.py", Data: []byte("print('ok')\n"), Executable: true},
		{Path: "data/blob.bin", Data: []byte{0, 1, 2, 0xff}},
		{Path: "README.md", Data: []byte("capsule\n")},
	}
	if reverse {
		files[0], files[2] = files[2], files[0]
	}
	ref, err := manager.Create(files, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	lease, err := manager.Acquire(ref, "capsule-fixture")
	if err != nil {
		t.Fatal(err)
	}
	if errno := lease.FS().Mkdir("empty", 0o755); errno != 0 {
		t.Fatalf("mkdir empty: %v", errno)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	return ref
}

func exportCapsule(t *testing.T, manager *Manager, ref Ref) ([]byte, CapsuleInfo) {
	t.Helper()
	var output bytes.Buffer
	info, err := manager.ExportCapsule(ref, &output)
	if err != nil {
		t.Fatal(err)
	}
	return output.Bytes(), info
}

func TestWorkspaceCapsuleIsDeterministicSelfContainedAndRoundTrips(t *testing.T) {
	firstManager := newTestManager(t)
	secondManager := newTestManager(t)
	firstRef := capsuleFixture(t, firstManager, false)
	secondRef := capsuleFixture(t, secondManager, true)

	first, firstInfo := exportCapsule(t, firstManager, firstRef)
	second, secondInfo := exportCapsule(t, secondManager, secondRef)
	if !bytes.Equal(first, second) || firstInfo != secondInfo {
		t.Fatalf("equivalent trees produced different capsules\nfirst=%+v\nsecond=%+v", firstInfo, secondInfo)
	}
	if !bytes.HasPrefix(first, []byte(CapsuleMagic)) {
		t.Fatalf("capsule missing magic: %q", first[:min(len(first), 32)])
	}
	if bytes.Contains(first, []byte(firstManager.base)) || bytes.Contains(first, []byte(firstRef)) {
		t.Fatal("capsule leaked Host path or local workspace reference")
	}
	if firstInfo.SchemaVersion != CapsuleSchemaVersion || !strings.HasPrefix(firstInfo.WorkspaceSHA256, "sha256:") || !strings.HasPrefix(firstInfo.TreeSHA256, "sha256:") || firstInfo.EntryCount != 6 || firstInfo.TotalBytes != 24 {
		t.Fatalf("capsule info=%+v", firstInfo)
	}

	destination := newTestManager(t)
	importedRef, importedInfo, err := destination.ImportCapsule(bytes.NewReader(first), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if importedInfo != firstInfo || importedRef == firstRef {
		t.Fatalf("imported ref/info=%q %+v", importedRef, importedInfo)
	}
	if err := firstManager.Destroy(firstRef); err != nil {
		t.Fatal(err)
	}

	lease, err := destination.Acquire(importedRef, "roundtrip")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	if got := readAll(t, lease.FS(), "data/blob.bin"); !bytes.Equal(got, []byte{0, 1, 2, 0xff}) {
		t.Fatalf("binary payload=%v", got)
	}
	for name, want := range map[string]fs.FileMode{
		"bin/tool.py": 0o700,
		"README.md":   0o600,
		"empty":       fs.ModeDir | 0o700,
	} {
		stat, errno := lease.FS().Stat(name)
		if errno != 0 || stat.Mode != want {
			t.Fatalf("stat %s: mode=%v want=%v errno=%v", name, stat.Mode, want, errno)
		}
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	reexported, reexportedInfo := exportCapsule(t, destination, importedRef)
	if !bytes.Equal(first, reexported) || reexportedInfo != firstInfo {
		t.Fatal("import/export changed canonical capsule bytes")
	}
}

func TestWorkspaceCapsuleExportRejectsBusyOrInvalidTrees(t *testing.T) {
	manager := newTestManager(t)
	ref := capsuleFixture(t, manager, false)
	lease, err := manager.Acquire(ref, "busy")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ExportCapsule(ref, &bytes.Buffer{}); !errors.Is(err, ErrWorkspaceBusy) {
		t.Fatalf("busy export err=%v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("README.md", manager.entries[ref].root+"/link"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ExportCapsule(ref, &bytes.Buffer{}); !errors.Is(err, ErrInvalidWorkspace) {
		t.Fatalf("unsafe export err=%v", err)
	}
}

func TestWorkspaceCapsuleImportRejectsTamperingTrailingDataAndHostQuota(t *testing.T) {
	source := newTestManager(t)
	capsule, _ := exportCapsule(t, source, capsuleFixture(t, source, false))

	cases := map[string][]byte{
		"payload-tamper":  append([]byte(nil), capsule...),
		"manifest-tamper": bytes.Replace(append([]byte(nil), capsule...), []byte(`"max_depth":32`), []byte(`"max_depth":31`), 1),
		"trailing-data":   append(append([]byte(nil), capsule...), 0),
		"bad-magic":       append([]byte(nil), capsule...),
	}
	cases["payload-tamper"][len(cases["payload-tamper"])-1] ^= 0xff
	cases["bad-magic"][0] ^= 0xff
	for name, candidate := range cases {
		t.Run(name, func(t *testing.T) {
			manager := newTestManager(t)
			if _, _, err := manager.ImportCapsule(bytes.NewReader(candidate), DefaultLimits()); !errors.Is(err, ErrInvalidWorkspace) {
				t.Fatalf("import err=%v", err)
			}
			if len(manager.entries) != 0 {
				t.Fatal("failed import registered a workspace")
			}
			children, err := os.ReadDir(manager.base)
			if err != nil || len(children) != 0 {
				t.Fatalf("failed import left roots=%d err=%v", len(children), err)
			}
		})
	}

	manager := newTestManager(t)
	tiny := Limits{MaxFiles: 32, MaxBytes: 22, MaxFileBytes: 16, MaxDepth: 8}
	if _, _, err := manager.ImportCapsule(bytes.NewReader(capsule), tiny); !errors.Is(err, ErrInvalidWorkspace) {
		t.Fatalf("quota import err=%v", err)
	}
}

func TestWorkspaceCapsuleImportRejectsNilIO(t *testing.T) {
	manager := newTestManager(t)
	if _, err := manager.ExportCapsule("ws-00000000000000000000000000000000", nil); !errors.Is(err, ErrInvalidWorkspace) {
		t.Fatalf("nil writer err=%v", err)
	}
	if _, _, err := manager.ImportCapsule(nil, DefaultLimits()); !errors.Is(err, ErrInvalidWorkspace) {
		t.Fatalf("nil reader err=%v", err)
	}
}

func sealedCapsuleManifestForTest(t *testing.T, entries []capsuleEntry, total uint64) capsuleManifest {
	t.Helper()
	manifest := capsuleManifest{
		SchemaVersion: CapsuleSchemaVersion, EntryCount: uint32(len(entries)), TotalBytes: total,
		Limits: limitsForCapsule(DefaultLimits()), Entries: entries,
	}
	var err error
	manifest.TreeSHA256, err = capsuleTreeDigest(entries)
	if err != nil {
		t.Fatal(err)
	}
	manifest.WorkspaceSHA256, err = capsuleWorkspaceDigest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func TestWorkspaceCapsuleManifestRejectsUnsafeSerializedTrees(t *testing.T) {
	emptyDigest := "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	file := func(name string) capsuleEntry {
		return capsuleEntry{Path: name, Kind: "file", SHA256: emptyDigest}
	}
	cases := map[string]capsuleManifest{
		"traversal":      sealedCapsuleManifestForTest(t, []capsuleEntry{file("../escape")}, 0),
		"git metadata":   sealedCapsuleManifestForTest(t, []capsuleEntry{{Path: ".git", Kind: "directory"}}, 0),
		"missing parent": sealedCapsuleManifestForTest(t, []capsuleEntry{file("a/b")}, 0),
		"unsorted":       sealedCapsuleManifestForTest(t, []capsuleEntry{file("b"), file("a")}, 0),
		"duplicate":      sealedCapsuleManifestForTest(t, []capsuleEntry{file("a"), file("a")}, 0),
		"unknown kind":   sealedCapsuleManifestForTest(t, []capsuleEntry{{Path: "a", Kind: "symlink"}}, 0),
		"directory data": sealedCapsuleManifestForTest(t, []capsuleEntry{{Path: "a", Kind: "directory", Size: 1}}, 0),
	}
	for name, manifest := range cases {
		t.Run(name, func(t *testing.T) {
			if err := validateCapsuleManifest(manifest, DefaultLimits()); !errors.Is(err, ErrInvalidWorkspace) {
				t.Fatalf("validation err=%v", err)
			}
		})
	}
}

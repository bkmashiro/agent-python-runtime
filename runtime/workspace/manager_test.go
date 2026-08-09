package workspace

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	experimentalsys "github.com/tetratelabs/wazero/experimental/sys"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	base := t.TempDir()
	if err := os.Chmod(base, 0o700); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := manager.Close(); err != nil {
			t.Errorf("close manager: %v", err)
		}
	})
	return manager
}

func readAll(t *testing.T, filesystem experimentalsys.FS, name string) []byte {
	t.Helper()
	file, errno := filesystem.OpenFile(name, experimentalsys.O_RDONLY, 0)
	if errno != 0 {
		t.Fatalf("open %q: %v", name, errno)
	}
	defer file.Close()
	var out bytes.Buffer
	buffer := make([]byte, 16)
	for {
		n, errno := file.Read(buffer)
		if errno != 0 {
			t.Fatalf("read %q: %v", name, errno)
		}
		if n == 0 {
			break
		}
		out.Write(buffer[:n])
	}
	return out.Bytes()
}

func TestWorkspaceContinuesOrdinaryFilesAcrossExclusiveLeasesWithoutCopy(t *testing.T) {
	manager := newTestManager(t)
	ref, err := manager.Create([]InitialFile{{Path: "input/data.txt", Data: []byte("seed")}}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ref), manager.base) || !strings.HasPrefix(string(ref), "ws-") {
		t.Fatalf("workspace reference leaked Host identity: %q", ref)
	}

	first, err := manager.Acquire(ref, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Acquire(ref, "run-b"); !errors.Is(err, ErrWorkspaceBusy) {
		t.Fatalf("second writer lease: %v", err)
	}
	if got := readAll(t, first.FS(), "input/data.txt"); !bytes.Equal(got, []byte("seed")) {
		t.Fatalf("initial file=%q", got)
	}
	if errno := first.FS().Mkdir("state", 0o777); errno != 0 {
		t.Fatalf("mkdir: %v", errno)
	}
	output, errno := first.FS().OpenFile("state/result.json", experimentalsys.O_WRONLY|experimentalsys.O_CREAT|experimentalsys.O_EXCL, 0o777)
	if errno != 0 {
		t.Fatalf("create output: %v", errno)
	}
	if n, errno := output.Write([]byte(`{"ok":true}`)); errno != 0 || n != 11 {
		t.Fatalf("write output n=%d errno=%v", n, errno)
	}
	if errno := output.Close(); errno != 0 {
		t.Fatalf("close output: %v", errno)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}

	second, err := manager.Acquire(ref, "run-b")
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, second.FS(), "state/result.json"); !bytes.Equal(got, []byte(`{"ok":true}`)) {
		t.Fatalf("continued output=%q", got)
	}
	if err := manager.Destroy(ref); !errors.Is(err, ErrWorkspaceBusy) {
		t.Fatalf("destroy active workspace: %v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatal(err)
	}
	if err := manager.Destroy(ref); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Acquire(ref, "run-c"); !errors.Is(err, ErrWorkspaceNotFound) {
		t.Fatalf("acquire destroyed workspace: %v", err)
	}
}

func TestWorkspaceFilesystemRejectsEscapeLinksAndSpecialEntries(t *testing.T) {
	manager := newTestManager(t)
	ref, err := manager.Create(nil, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	lease, err := manager.Acquire(ref, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	filesystem := lease.FS()
	for _, name := range []string{"../escape", "/absolute", "a/../../escape", "a\\windows", "..namedfork/rsrc", string([]byte{0xff})} {
		if _, errno := filesystem.OpenFile(name, experimentalsys.O_WRONLY|experimentalsys.O_CREAT, 0o600); errno != experimentalsys.EPERM {
			t.Fatalf("path %q errno=%v, want EPERM", name, errno)
		}
	}
	if errno := filesystem.Symlink("target", "link"); errno != experimentalsys.EPERM {
		t.Fatalf("symlink errno=%v", errno)
	}
	if errno := filesystem.Link("old", "new"); errno != experimentalsys.EPERM {
		t.Fatalf("hard link errno=%v", errno)
	}

	entry := manager.entries[ref]
	if err := os.Symlink("../outside", filepath.Join(entry.root, "host-link")); err != nil {
		t.Fatal(err)
	}
	if _, errno := filesystem.OpenFile("host-link", experimentalsys.O_RDONLY, 0); errno != experimentalsys.EPERM {
		t.Fatalf("Host-injected symlink errno=%v", errno)
	}
	if err := os.WriteFile(filepath.Join(entry.root, "hard-a"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(entry.root, "hard-a"), filepath.Join(entry.root, "hard-b")); err != nil {
		t.Fatal(err)
	}
	if _, errno := filesystem.OpenFile("hard-a", experimentalsys.O_RDONLY, 0); errno != experimentalsys.EPERM {
		t.Fatalf("Host-injected hard link errno=%v", errno)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Acquire(ref, "run-b"); !errors.Is(err, ErrInvalidWorkspace) {
		t.Fatalf("reacquired invalid tree: %v", err)
	}
}

func TestWorkspaceFilesystemRejectsOutOfBandRegularFileInjection(t *testing.T) {
	manager := newTestManager(t)
	ref, err := manager.Create([]InitialFile{{Path: "tracked.txt", Data: []byte("ok")}}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	lease, err := manager.Acquire(ref, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	if err := os.WriteFile(filepath.Join(manager.entries[ref].root, "injected.txt"), []byte("untracked"), 0o600); err != nil {
		t.Fatal(err)
	}
	filesystem := lease.FS()
	if _, errno := filesystem.OpenFile("injected.txt", experimentalsys.O_RDONLY, 0); errno != experimentalsys.EPERM {
		t.Fatalf("opened untracked regular file: errno=%v", errno)
	}
	root, errno := filesystem.OpenFile(".", experimentalsys.O_RDONLY|experimentalsys.O_DIRECTORY, 0)
	if errno != 0 {
		t.Fatal(errno)
	}
	defer root.Close()
	if _, errno := root.Readdir(100); errno != experimentalsys.EPERM {
		t.Fatalf("listed untracked regular file: errno=%v", errno)
	}
}

func TestWorkspaceFilesystemEnforcesQuotasAndMasksHostStatIdentity(t *testing.T) {
	manager := newTestManager(t)
	limits := Limits{MaxFiles: 2, MaxBytes: 8, MaxFileBytes: 6, MaxDepth: 3}
	ref, err := manager.Create([]InitialFile{{Path: "a", Data: []byte("12")}}, limits)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := manager.Acquire(ref, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	filesystem := lease.FS()

	info, errno := filesystem.Stat("a")
	if errno != 0 {
		t.Fatal(errno)
	}
	if info.Dev != 0 || info.Ino != 0 || info.Nlink != 1 || !info.Mode.IsRegular() {
		t.Fatalf("Host stat identity leaked: %#v", info)
	}
	file, errno := filesystem.OpenFile("a", experimentalsys.O_RDWR, 0)
	if errno != 0 {
		t.Fatal(errno)
	}
	if _, errno := file.Seek(0, io.SeekEnd); errno != 0 {
		t.Fatal(errno)
	}
	if n, errno := file.Write([]byte("3456")); errno != 0 || n != 4 {
		t.Fatalf("bounded write n=%d errno=%v", n, errno)
	}
	if n, errno := file.Write([]byte("7")); errno != experimentalsys.EACCES || n != 0 {
		t.Fatalf("per-file overflow n=%d errno=%v", n, errno)
	}
	if errno := file.Truncate(7); errno != experimentalsys.EACCES {
		t.Fatalf("growing truncate errno=%v", errno)
	}
	if errno := file.Close(); errno != 0 {
		t.Fatal(errno)
	}

	second, errno := filesystem.OpenFile("b", experimentalsys.O_WRONLY|experimentalsys.O_CREAT|experimentalsys.O_EXCL, 0o600)
	if errno != 0 {
		t.Fatal(errno)
	}
	if n, errno := second.Write([]byte("xx")); errno != 0 || n != 2 {
		t.Fatalf("second write n=%d errno=%v", n, errno)
	}
	_ = second.Close()
	third, errno := filesystem.OpenFile("c", experimentalsys.O_WRONLY|experimentalsys.O_CREAT|experimentalsys.O_EXCL, 0o600)
	if errno != experimentalsys.EACCES || third != nil {
		t.Fatalf("workspace byte/file quota errno=%v file=%v", errno, third)
	}
}

func TestWorkspaceCreateRejectsNonCanonicalAndDuplicateInitialFiles(t *testing.T) {
	manager := newTestManager(t)
	for name, files := range map[string][]InitialFile{
		"traversal":      {{Path: "../x", Data: []byte("x")}},
		"absolute":       {{Path: "/x", Data: []byte("x")}},
		"resource-fork":  {{Path: "..namedfork/rsrc", Data: []byte("x")}},
		"invalid-utf8":   {{Path: string([]byte{0xff}), Data: []byte("x")}},
		"long-component": {{Path: strings.Repeat("x", 256), Data: []byte("x")}},
		"duplicate":      {{Path: "x", Data: []byte("x")}, {Path: "x", Data: []byte("y")}},
		"file-parent":    {{Path: "x", Data: []byte("x")}, {Path: "x/y", Data: []byte("y")}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := manager.Create(files, DefaultLimits()); !errors.Is(err, ErrInvalidWorkspace) {
				t.Fatalf("create error=%v", err)
			}
		})
	}
}

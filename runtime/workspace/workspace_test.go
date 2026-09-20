package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	experimentalsys "github.com/tetratelabs/wazero/experimental/sys"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	base := filepath.Join(t.TempDir(), "workspaces")
	if err := os.Mkdir(base, 0o700); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := manager.Close(); err != nil {
			t.Error(err)
		}
	})
	return manager
}

func TestManagerCreatesPrivateBoundedWorkspace(t *testing.T) {
	base := filepath.Join(t.TempDir(), "workspaces")
	if err := os.Mkdir(base, 0o700); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(base)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	ref, err := manager.Create([]InitialFile{{Path: "src/main.py", Data: []byte("result = 1\n")}}, Limits{MaxFiles: 16, MaxBytes: 1024, MaxFileBytes: 512, MaxDepth: 8})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := manager.Acquire(ref, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := lease.BeginRun(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := lease.BeginRun(); !errors.Is(err, ErrWorkspaceBusy) {
		t.Fatalf("concurrent run err=%v", err)
	}
	lease.EndRun()
	files, err := lease.Files()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Path != "src/main.py" || string(files[0].Data) != "result = 1\n" {
		t.Fatalf("files=%#v", files)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestManagerRejectsTraversalAndOversizedInput(t *testing.T) {
	base := filepath.Join(t.TempDir(), "workspaces")
	if err := os.Mkdir(base, 0o700); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(base)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	limits := Limits{MaxFiles: 4, MaxBytes: 4, MaxFileBytes: 4, MaxDepth: 2}
	for _, file := range []InitialFile{{Path: "../escape", Data: []byte("x")}, {Path: "large", Data: []byte("12345")}} {
		if _, err := manager.Create([]InitialFile{file}, limits); !errors.Is(err, ErrInvalidWorkspace) {
			t.Fatalf("file=%#v err=%v", file, err)
		}
	}
}

func TestMountedFilesystemRejectsEscapeLinksAndQuotaOverflow(t *testing.T) {
	base := filepath.Join(t.TempDir(), "workspaces")
	if err := os.Mkdir(base, 0o700); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManager(base)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	ref, err := manager.Create(nil, Limits{MaxFiles: 4, MaxBytes: 4, MaxFileBytes: 4, MaxDepth: 2})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := manager.Acquire(ref, "security-test")
	if err != nil {
		t.Fatal(err)
	}
	filesystem, _, err := lease.BeginRun()
	if err != nil {
		t.Fatal(err)
	}
	if _, errno := filesystem.OpenFile("../escape", experimentalsys.O_CREAT|experimentalsys.O_RDWR, 0o600); errno != experimentalsys.EPERM {
		t.Fatalf("escape errno=%v", errno)
	}
	if errno := filesystem.Symlink("outside", "link"); errno != experimentalsys.EPERM {
		t.Fatalf("symlink errno=%v", errno)
	}
	file, errno := filesystem.OpenFile("bounded", experimentalsys.O_CREAT|experimentalsys.O_RDWR, 0o600)
	if errno != 0 {
		t.Fatal(errno)
	}
	if _, errno = file.Write([]byte("12345")); errno != experimentalsys.EACCES {
		t.Fatalf("quota errno=%v", errno)
	}
	if errno = file.Close(); errno != 0 {
		t.Fatal(errno)
	}
	lease.EndRun()
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
}

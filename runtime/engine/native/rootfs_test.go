package native

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestVerifyOCIImageConfigBindsExactJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	content := []byte(`{"architecture":"arm64","os":"linux"}`)
	if err := os.WriteFile(path, content, 0o444); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	expected := "sha256:" + hex.EncodeToString(digest[:])
	if err := VerifyOCIImageConfig(path, expected); err != nil {
		t.Fatal(err)
	}
	if err := VerifyOCIImageConfig(path, "sha256:"+strings.Repeat("f", 64)); !errors.Is(err, ErrInvalidRootFS) {
		t.Fatalf("mismatch=%v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"mutated":true}`), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := VerifyOCIImageConfig(path, expected); !errors.Is(err, ErrInvalidRootFS) {
		t.Fatalf("mutation=%v", err)
	}
}

func TestRootFSIdentityBindsContentModeAndSymlink(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "opt", "pysolate"), 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "opt", "pysolate", "runner.py")
	if err := os.WriteFile(file, []byte("result=1\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("runner.py", filepath.Join(root, "opt", "pysolate", "current.py")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/usr/lib/missing-in-host", filepath.Join(root, "absolute-guest-link")); err != nil {
		t.Fatal(err)
	}
	first, err := RootFSIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RootFSIdentity(root)
	if err != nil || first != second {
		t.Fatalf("first=%s second=%s err=%v", first, second, err)
	}
	if err := os.Chmod(file, 0o644); err != nil {
		t.Fatal(err)
	}
	modeChanged, err := RootFSIdentity(root)
	if err != nil || modeChanged == first {
		t.Fatalf("mode digest=%s err=%v", modeChanged, err)
	}
	if err := os.WriteFile(file, []byte("result=2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	contentChanged, err := RootFSIdentity(root)
	if err != nil || contentChanged == modeChanged {
		t.Fatalf("content digest=%s err=%v", contentChanged, err)
	}
}

func TestRootFSIdentityRejectsSpecialFilesAndEmptyRoot(t *testing.T) {
	if _, err := RootFSIdentity(t.TempDir()); err == nil {
		t.Fatal("empty rootfs accepted")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "run"), 0o755); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(root, "run", "socket")
	// A named pipe is enough to prove mutable IPC nodes are not part of a sealed rootfs.
	if err := syscall.Mkfifo(socket, 0o600); err != nil {
		t.Skipf("fifo unavailable: %v", err)
	}
	if _, err := RootFSIdentity(root); err == nil {
		t.Fatal("special file accepted")
	}
}

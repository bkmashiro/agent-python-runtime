//go:build darwin || linux

package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestWorkspaceCreateFromDirectoryRejectsFIFOAtomically(t *testing.T) {
	source := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(source, "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := newTestManager(t)
	if _, err := manager.CreateFromDirectory(source, DefaultLimits()); !errors.Is(err, ErrInvalidWorkspace) {
		t.Fatalf("FIFO source err=%v", err)
	} else if strings.Contains(err.Error(), source) {
		t.Fatalf("source Host path leaked in error: %v", err)
	}
	children, err := os.ReadDir(manager.base)
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 0 || len(manager.entries) != 0 {
		t.Fatalf("failed FIFO ingress left entries=%d roots=%d", len(manager.entries), len(children))
	}
}

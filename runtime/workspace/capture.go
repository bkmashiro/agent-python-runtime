package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// CaptureFile returns a bounded copy of one regular file from an immutable,
// unleased workspace root. It is a Host evidence-capture seam: mutable or
// concurrently attached workspaces fail closed, and no Host path is exposed.
func (manager *Manager) CaptureFile(ref Ref, name string, maxBytes uint64) ([]byte, error) {
	if manager == nil || !validRef(ref) || maxBytes == 0 {
		return nil, ErrInvalidWorkspace
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return nil, ErrWorkspaceClosed
	}
	item, ok := manager.entries[ref]
	if !ok {
		return nil, ErrWorkspaceNotFound
	}
	if item.owner != "" {
		return nil, ErrWorkspaceBusy
	}
	if !item.immutable || item.rootRecord == nil {
		return nil, ErrWorkspaceImmutable
	}
	cleaned, err := cleanGuestPath(name, item.limits.MaxDepth, false)
	if err != nil || cleaned == "." || cleaned != name {
		return nil, ErrInvalidWorkspace
	}
	full := filepath.Join(item.root, filepath.FromSlash(cleaned))
	info, err := os.Lstat(full)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrWorkspaceNotFound
		}
		return nil, fmt.Errorf("inspect captured workspace file: %w", err)
	}
	limit := maxBytes
	if item.limits.MaxFileBytes < limit {
		limit = item.limits.MaxFileBytes
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() < 0 || uint64(info.Size()) > limit {
		return nil, ErrInvalidWorkspace
	}
	body, err := os.ReadFile(full)
	if err != nil {
		return nil, fmt.Errorf("capture workspace file: %w", err)
	}
	if uint64(len(body)) > limit {
		return nil, ErrInvalidWorkspace
	}
	return append([]byte(nil), body...), nil
}

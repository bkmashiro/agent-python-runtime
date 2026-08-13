package workspace

import (
	"errors"
	"fmt"
	"os"

	wazerosys "github.com/tetratelabs/wazero/sys"
)

var ErrAttemptTerminal = errors.New("workspace attempt is already terminal")

// Attempt is a private, independently addressed copy of a published workspace.
// Publishing returns the attempt identity; it never mutates the base in place.
type Attempt struct {
	manager  *Manager
	base     Ref
	ref      Ref
	terminal bool
}

func (manager *Manager) ForkAttempt(base Ref) (*Attempt, error) {
	if manager == nil || !validRef(base) {
		return nil, ErrInvalidWorkspace
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return nil, ErrWorkspaceClosed
	}
	item, ok := manager.entries[base]
	if !ok {
		return nil, ErrWorkspaceNotFound
	}
	if item.owner != "" {
		return nil, ErrWorkspaceBusy
	}
	if _, err := scanOrdinaryTree(item.root, item.limits); err != nil {
		return nil, err
	}
	source, err := os.OpenRoot(item.root)
	if err != nil {
		return nil, fmt.Errorf("open attempt base: %w", err)
	}
	defer source.Close()
	rootInfo, err := os.Lstat(item.root)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return nil, ErrInvalidWorkspace
	}
	ref, destination, err := manager.allocateRootLocked()
	if err != nil {
		return nil, err
	}
	failed := true
	defer func() {
		if failed {
			_ = os.RemoveAll(destination)
		}
	}()
	usage := &ingressUsage{}
	if err := copySourceDirectory(source, ".", destination, wazerosys.NewStat_t(rootInfo).Dev, item.limits, usage, nil); err != nil {
		return nil, err
	}
	if _, err := scanOrdinaryTree(destination, item.limits); err != nil {
		return nil, err
	}
	manager.entries[ref] = &entry{root: destination, limits: item.limits}
	failed = false
	return &Attempt{manager: manager, base: base, ref: ref}, nil
}

func (attempt *Attempt) BaseRef() Ref {
	if attempt == nil {
		return ""
	}
	return attempt.base
}

func (attempt *Attempt) Ref() Ref {
	if attempt == nil {
		return ""
	}
	return attempt.ref
}

func (attempt *Attempt) Publish() (Ref, error) {
	if attempt == nil || attempt.manager == nil {
		return "", ErrInvalidWorkspace
	}
	attempt.manager.mu.Lock()
	defer attempt.manager.mu.Unlock()
	if attempt.terminal {
		return "", ErrAttemptTerminal
	}
	item, ok := attempt.manager.entries[attempt.ref]
	if !ok || item.owner != "" {
		return "", ErrWorkspaceBusy
	}
	if _, err := scanOrdinaryTree(item.root, item.limits); err != nil {
		return "", err
	}
	attempt.terminal = true
	return attempt.ref, nil
}

func (attempt *Attempt) Discard() error {
	if attempt == nil || attempt.manager == nil {
		return ErrInvalidWorkspace
	}
	attempt.manager.mu.Lock()
	defer attempt.manager.mu.Unlock()
	if attempt.terminal {
		return ErrAttemptTerminal
	}
	item, ok := attempt.manager.entries[attempt.ref]
	if !ok {
		return ErrWorkspaceNotFound
	}
	if item.owner != "" {
		return ErrWorkspaceBusy
	}
	if err := os.RemoveAll(item.root); err != nil {
		return fmt.Errorf("discard workspace attempt: %w", err)
	}
	delete(attempt.manager.entries, attempt.ref)
	attempt.terminal = true
	return nil
}

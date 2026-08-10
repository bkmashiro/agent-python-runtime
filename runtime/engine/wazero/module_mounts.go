package wazero

import (
	"errors"
	"sync"

	experimentalsys "github.com/tetratelabs/wazero/experimental/sys"
)

type temporaryMount interface {
	FS() experimentalsys.FS
	Close() error
}

type temporaryMountFactory func() (temporaryMount, error)

// moduleMounts owns the filesystem authority attached to one single-use module.
// The persistent workspace is fixed at preparation time but gated; the scratch
// filesystem is not created until the module is checked out for a real Run.
type moduleMounts struct {
	mutex             sync.Mutex
	workspace         *workspaceGate
	temporary         *workspaceGate
	newTemporary      temporaryMountFactory
	temporaryResource temporaryMount
	activated         bool
	closed            bool
}

func newModuleMounts(workspaceFS experimentalsys.FS, factory temporaryMountFactory) (*moduleMounts, error) {
	if workspaceFS == nil || factory == nil {
		return nil, errors.New("module mounts require workspace and temporary bindings")
	}
	return &moduleMounts{
		workspace:    newWorkspaceGate(workspaceFS),
		temporary:    newWorkspaceGate(nil),
		newTemporary: factory,
	}, nil
}

func (mounts *moduleMounts) activate() error {
	if mounts == nil {
		return nil
	}
	mounts.mutex.Lock()
	defer mounts.mutex.Unlock()
	if mounts.closed {
		return errors.New("module mounts are closed")
	}
	if mounts.activated {
		return errors.New("module mounts were already activated")
	}
	if err := mounts.workspace.activate(); err != nil {
		return err
	}
	temporary, err := mounts.newTemporary()
	if err != nil {
		return err
	}
	if temporary == nil {
		return errors.New("temporary mount factory returned no resource")
	}
	mounts.temporaryResource = temporary
	if temporary.FS() == nil {
		cleanupErr := temporary.Close()
		if cleanupErr == nil {
			mounts.temporaryResource = nil
		}
		return errors.Join(errors.New("temporary mount factory returned no filesystem"), cleanupErr)
	}
	if err := mounts.temporary.attachAndActivate(temporary.FS()); err != nil {
		cleanupErr := temporary.Close()
		if cleanupErr == nil {
			mounts.temporaryResource = nil
		}
		return errors.Join(err, cleanupErr)
	}
	mounts.activated = true
	return nil
}

func (mounts *moduleMounts) close() error {
	if mounts == nil {
		return nil
	}
	mounts.mutex.Lock()
	defer mounts.mutex.Unlock()
	if mounts.closed {
		return nil
	}
	if mounts.temporaryResource != nil {
		if err := mounts.temporaryResource.Close(); err != nil {
			return err
		}
		mounts.temporaryResource = nil
	}
	mounts.closed = true
	return nil
}

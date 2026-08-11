package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

type mountedWorkspaceBinding struct {
	manager    *workspace.Manager
	ref        workspace.Ref
	base       string
	outputPath string
	closed     bool
}

func prepareMountedWorkspace(config *mountedWorkspaceConfig) (*mountedWorkspaceBinding, error) {
	if config == nil {
		return nil, nil
	}
	if err := config.validate(); err != nil {
		return nil, err
	}
	limits, err := config.resolveLimits()
	if err != nil {
		return nil, err
	}
	base, err := os.MkdirTemp("", "pysolate-workspaces-")
	if err != nil {
		return nil, errors.New("create workspace manager root")
	}
	if err := os.Chmod(base, 0o700); err != nil {
		_ = os.RemoveAll(base)
		return nil, errors.New("secure workspace manager root")
	}
	manager, err := workspace.NewManager(base)
	if err != nil {
		_ = os.RemoveAll(base)
		return nil, errors.New("initialize workspace manager")
	}
	binding := &mountedWorkspaceBinding{manager: manager, base: base, outputPath: config.OutputCapsule}
	failed := true
	defer func() {
		if failed {
			_ = binding.close()
		}
	}()
	switch {
	case config.SourceDirectory != "":
		binding.ref, err = manager.CreateFromDirectory(config.SourceDirectory, limits)
	case config.InputCapsule != "":
		var input *os.File
		input, err = os.Open(config.InputCapsule)
		if err == nil {
			binding.ref, _, err = manager.ImportCapsule(input, limits)
			closeErr := input.Close()
			err = errors.Join(err, closeErr)
		}
	default:
		binding.ref, err = manager.Create(nil, limits)
	}
	if err != nil {
		return nil, errors.New("provision mounted workspace")
	}
	failed = false
	return binding, nil
}

func (binding *mountedWorkspaceBinding) export() (workspace.CapsuleInfo, error) {
	if binding == nil || binding.closed || binding.manager == nil || binding.outputPath == "" {
		return workspace.CapsuleInfo{}, errors.New("workspace capsule output is unavailable")
	}
	directory := filepath.Dir(binding.outputPath)
	temporary, err := os.CreateTemp(directory, ".pysolate-workspace-*.tmp")
	if err != nil {
		return workspace.CapsuleInfo{}, errors.New("create workspace capsule output")
	}
	temporaryPath := temporary.Name()
	failed := true
	defer func() {
		if failed {
			_ = temporary.Close()
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return workspace.CapsuleInfo{}, errors.New("secure workspace capsule output")
	}
	info, err := binding.manager.ExportCapsule(binding.ref, temporary)
	if err != nil {
		return workspace.CapsuleInfo{}, errors.New("serialize workspace capsule")
	}
	if err := temporary.Sync(); err != nil {
		return workspace.CapsuleInfo{}, errors.New("sync workspace capsule output")
	}
	if err := temporary.Close(); err != nil {
		return workspace.CapsuleInfo{}, errors.New("close workspace capsule output")
	}
	if err := os.Rename(temporaryPath, binding.outputPath); err != nil {
		return workspace.CapsuleInfo{}, errors.New("publish workspace capsule output")
	}
	failed = false
	if parent, err := os.Open(directory); err == nil {
		_ = parent.Sync()
		_ = parent.Close()
	}
	return info, nil
}

func (binding *mountedWorkspaceBinding) close() error {
	if binding == nil || binding.closed {
		return nil
	}
	binding.closed = true
	managerErr := binding.manager.Close()
	removeErr := os.RemoveAll(binding.base)
	if err := errors.Join(managerErr, removeErr); err != nil {
		return fmt.Errorf("close mounted workspace: %w", err)
	}
	return nil
}

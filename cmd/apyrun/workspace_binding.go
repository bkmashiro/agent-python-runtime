package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

type mountedWorkspaceBinding struct {
	manager     *workspace.Manager
	ref         workspace.Ref
	base        string
	outputPath  string
	policy      runtimeconfig.WorkspaceDispositionPolicy
	initialInfo workspace.CapsuleInfo
	closed      bool
}

type stagedWorkspaceCapsule struct {
	temporaryPath string
	outputPath    string
	info          workspace.CapsuleInfo
	sha256        string
	published     bool
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
	binding := &mountedWorkspaceBinding{
		manager: manager, base: base, outputPath: config.OutputCapsule, policy: config.Disposition,
	}
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
	if err == nil {
		binding.initialInfo, err = manager.Inspect(binding.ref)
	}
	if err != nil {
		return nil, errors.New("provision mounted workspace")
	}
	failed = false
	return binding, nil
}

func (binding *mountedWorkspaceBinding) prepareDisposition(status runtimeconfig.RunResponseStatus, requestSHA256 string) (runtimeconfig.WorkspaceReceipt, *stagedWorkspaceCapsule, error) {
	if binding == nil || binding.closed || binding.manager == nil {
		return runtimeconfig.WorkspaceReceipt{}, nil, errors.New("mounted workspace disposition is unavailable")
	}
	finalInfo, err := binding.manager.Inspect(binding.ref)
	if err != nil {
		return runtimeconfig.WorkspaceReceipt{}, nil, errors.New("inspect final mounted workspace")
	}
	export := binding.policy == runtimeconfig.WorkspaceExportOnResponse ||
		(binding.policy == runtimeconfig.WorkspaceExportOnSuccess && status == runtimeconfig.RunResponseOK)
	receipt := runtimeconfig.WorkspaceReceipt{
		SchemaVersion:          runtimeconfig.WorkspaceReceiptSchemaVersion,
		RequestSHA256:          requestSHA256,
		Policy:                 binding.policy,
		Disposition:            runtimeconfig.WorkspaceDiscarded,
		InitialWorkspaceSHA256: binding.initialInfo.WorkspaceSHA256,
		FinalWorkspaceSHA256:   finalInfo.WorkspaceSHA256,
		FinalTreeSHA256:        finalInfo.TreeSHA256,
		EntryCount:             finalInfo.EntryCount,
		TotalBytes:             finalInfo.TotalBytes,
	}
	if !export {
		if err := receipt.ValidateForStatus(status); err != nil {
			return runtimeconfig.WorkspaceReceipt{}, nil, errors.New("author discarded workspace receipt")
		}
		return receipt, nil, nil
	}
	staged, err := binding.stageExport()
	if err != nil {
		return runtimeconfig.WorkspaceReceipt{}, nil, err
	}
	if staged.info != finalInfo {
		staged.discard()
		return runtimeconfig.WorkspaceReceipt{}, nil, errors.New("workspace changed during disposition")
	}
	receipt.Disposition = runtimeconfig.WorkspaceExported
	receipt.CapsuleSHA256 = &staged.sha256
	if err := receipt.ValidateForStatus(status); err != nil {
		staged.discard()
		return runtimeconfig.WorkspaceReceipt{}, nil, errors.New("author exported workspace receipt")
	}
	return receipt, staged, nil
}

func (binding *mountedWorkspaceBinding) stageExport() (*stagedWorkspaceCapsule, error) {
	if binding.outputPath == "" {
		return nil, errors.New("workspace capsule output is unavailable")
	}
	directory := filepath.Dir(binding.outputPath)
	temporary, err := os.CreateTemp(directory, ".pysolate-workspace-*.tmp")
	if err != nil {
		return nil, errors.New("create workspace capsule output")
	}
	staged := &stagedWorkspaceCapsule{temporaryPath: temporary.Name(), outputPath: binding.outputPath}
	failed := true
	defer func() {
		if failed {
			_ = temporary.Close()
			staged.discard()
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return nil, errors.New("secure workspace capsule output")
	}
	hash := sha256.New()
	staged.info, err = binding.manager.ExportCapsule(binding.ref, io.MultiWriter(temporary, hash))
	if err != nil {
		return nil, errors.New("serialize workspace capsule")
	}
	staged.sha256 = "sha256:" + hex.EncodeToString(hash.Sum(nil))
	if err := temporary.Sync(); err != nil {
		return nil, errors.New("sync workspace capsule output")
	}
	if err := temporary.Close(); err != nil {
		return nil, errors.New("close workspace capsule output")
	}
	failed = false
	return staged, nil
}

func (staged *stagedWorkspaceCapsule) publish() error {
	if staged == nil || staged.published || staged.temporaryPath == "" || staged.outputPath == "" {
		return errors.New("staged workspace capsule is unavailable")
	}
	if err := os.Rename(staged.temporaryPath, staged.outputPath); err != nil {
		return errors.New("publish workspace capsule output")
	}
	staged.published = true
	staged.temporaryPath = ""
	directory := filepath.Dir(staged.outputPath)
	if parent, err := os.Open(directory); err == nil {
		_ = parent.Sync()
		_ = parent.Close()
	}
	return nil
}

func (staged *stagedWorkspaceCapsule) discard() {
	if staged == nil || staged.published || staged.temporaryPath == "" {
		return
	}
	_ = os.Remove(staged.temporaryPath)
	staged.temporaryPath = ""
}

func (binding *mountedWorkspaceBinding) close() error {
	if binding == nil || binding.closed {
		return nil
	}
	if binding.manager != nil {
		if err := binding.manager.Close(); err != nil {
			return fmt.Errorf("close mounted workspace: %w", err)
		}
		binding.manager = nil
	}
	if binding.base != "" {
		if err := os.RemoveAll(binding.base); err != nil {
			return fmt.Errorf("remove mounted workspace root: %w", err)
		}
		binding.base = ""
	}
	binding.closed = true
	return nil
}

package main

import (
	"errors"
	"os"
	"path/filepath"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/playback"
)

type stagedPlaybackBundle struct {
	temporaryPath string
	outputPath    string
	identity      string
	published     bool
}

func executionProfileSHA256(config runtimeconfig.RunConfig) (string, error) {
	return runtimeconfig.ExecutionProfileBindingSHA256(config)
}

func validatePlaybackAdmission(bundle playback.Bundle, plan *capability.Plan, config runtimeconfig.RunConfig, wasm []byte, requestSHA256 string, workspaceBinding *mountedWorkspaceBinding) error {
	if plan == nil || bundle.CapabilityPlanSHA256 != plan.Identity() || bundle.RequestSHA256 != requestSHA256 || bundle.ArtifactSHA256 != playback.SHA256(wasm) {
		return errors.New("playback admission identity mismatch")
	}
	profileSHA256, err := executionProfileSHA256(config)
	if err != nil || bundle.ExecutionProfileSHA256 != profileSHA256 {
		return errors.New("playback execution profile mismatch")
	}
	grants := plan.Grants()
	if len(grants) != len(bundle.Grants) {
		return errors.New("playback grant mismatch")
	}
	for index := range grants {
		if grants[index] != bundle.Grants[index] {
			return errors.New("playback grant mismatch")
		}
	}
	if workspaceBinding == nil {
		if bundle.InitialWorkspaceSHA256 != "" || bundle.FinalWorkspaceSHA256 != "" {
			return errors.New("playback workspace mismatch")
		}
	} else if bundle.InitialWorkspaceSHA256 == "" || bundle.InitialWorkspaceSHA256 != workspaceBinding.initialInfo.WorkspaceSHA256 {
		return errors.New("playback initial workspace mismatch")
	}
	return nil
}

func validateBranchAdmission(parent playback.Bundle, branch playback.BranchManifest, childPlan *capability.Plan, config runtimeconfig.RunConfig, wasm []byte, requestSHA256 string, workspaceBinding *mountedWorkspaceBinding) error {
	if err := branch.ValidateParent(parent); err != nil || childPlan == nil || branch.ChildCapabilityPlanSHA256 != childPlan.Identity() ||
		branch.RequestSHA256 != requestSHA256 || branch.ArtifactSHA256 != playback.SHA256(wasm) {
		return errors.New("branch admission identity mismatch")
	}
	profileSHA256, err := executionProfileSHA256(config)
	if err != nil || branch.ExecutionProfileSHA256 != profileSHA256 {
		return errors.New("branch execution profile mismatch")
	}
	grants := childPlan.Grants()
	if len(grants) != len(branch.ChildGrants) {
		return errors.New("branch child grant mismatch")
	}
	for index := range grants {
		if grants[index] != branch.ChildGrants[index] {
			return errors.New("branch child grant mismatch")
		}
	}
	if workspaceBinding == nil {
		if branch.InitialWorkspaceSHA256 != "" {
			return errors.New("branch initial workspace mismatch")
		}
	} else if branch.InitialWorkspaceSHA256 == "" || branch.InitialWorkspaceSHA256 != workspaceBinding.initialInfo.WorkspaceSHA256 {
		return errors.New("branch initial workspace mismatch")
	}
	return nil
}

func validatePlaybackOutcome(bundle playback.Bundle, response runtimeconfig.RunResponse) error {
	if string(response.Status) != bundle.ExpectedStatus {
		return errors.New("playback Agent status mismatch")
	}
	resultSHA256, err := playback.CanonicalSHA256(response.Result)
	if err != nil || resultSHA256 != bundle.ExpectedResultSHA256 {
		return errors.New("playback Agent result mismatch")
	}
	if response.WorkspaceReceipt == nil {
		if bundle.FinalWorkspaceSHA256 != "" {
			return errors.New("playback final workspace mismatch")
		}
		return nil
	}
	if response.WorkspaceReceipt.InitialWorkspaceSHA256 != bundle.InitialWorkspaceSHA256 || response.WorkspaceReceipt.FinalWorkspaceSHA256 != bundle.FinalWorkspaceSHA256 {
		return errors.New("playback final workspace mismatch")
	}
	return nil
}

func stagePlaybackBundle(outputPath string, bundle playback.Bundle) (*stagedPlaybackBundle, error) {
	if outputPath == "" || !filepath.IsAbs(outputPath) || filepath.Clean(outputPath) != outputPath {
		return nil, errors.New("invalid playback bundle output path")
	}
	if _, err := os.Lstat(outputPath); err == nil || !os.IsNotExist(err) {
		return nil, errors.New("playback bundle output already exists")
	}
	encoded, err := playback.Encode(bundle)
	if err != nil {
		return nil, errors.New("encode playback bundle")
	}
	if _, err := playback.Decode(encoded); err != nil {
		return nil, errors.New("validate playback bundle")
	}
	directory := filepath.Dir(outputPath)
	file, err := os.CreateTemp(directory, "."+filepath.Base(outputPath)+".tmp-*")
	if err != nil {
		return nil, errors.New("stage playback bundle")
	}
	staged := &stagedPlaybackBundle{temporaryPath: file.Name(), outputPath: outputPath, identity: bundle.Identity}
	failed := true
	defer func() {
		if failed {
			_ = file.Close()
			_ = os.Remove(staged.temporaryPath)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return nil, errors.New("protect playback bundle")
	}
	if _, err := file.Write(encoded); err != nil {
		return nil, errors.New("write playback bundle")
	}
	if err := file.Sync(); err != nil {
		return nil, errors.New("sync playback bundle")
	}
	if err := file.Close(); err != nil {
		return nil, errors.New("close playback bundle")
	}
	failed = false
	return staged, nil
}

func (staged *stagedPlaybackBundle) publish() error {
	if staged == nil || staged.temporaryPath == "" || staged.outputPath == "" || staged.published {
		return errors.New("playback bundle stage is unavailable")
	}
	// A same-directory hard link publishes atomically without overwriting an
	// existing protected artifact. The staged name is removed only after the
	// directory entry is durable.
	if err := os.Link(staged.temporaryPath, staged.outputPath); err != nil {
		return errors.New("publish playback bundle")
	}
	if err := syncDirectory(filepath.Dir(staged.outputPath)); err != nil {
		_ = os.Remove(staged.outputPath)
		return errors.New("sync published playback bundle")
	}
	if err := os.Remove(staged.temporaryPath); err != nil {
		return errors.New("remove playback bundle stage")
	}
	if err := syncDirectory(filepath.Dir(staged.outputPath)); err != nil {
		return errors.New("sync playback bundle directory")
	}
	staged.published = true
	return nil
}

func (staged *stagedPlaybackBundle) discard() error {
	if staged == nil || staged.temporaryPath == "" || staged.published {
		return nil
	}
	err := os.Remove(staged.temporaryPath)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

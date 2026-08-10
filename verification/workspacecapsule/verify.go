// Package workspacecapsule verifies the observable lifecycle contract of a
// real Workspace Capsule against a real CPython-WASI artifact.
package workspacecapsule

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

const SchemaVersion = "workspace-capsule-verification/v1"

type Status string
type CheckStatus string

const (
	StatusVerified Status      = "verified"
	StatusFailed   Status      = "failed"
	CheckPass      CheckStatus = "pass"
	CheckFail      CheckStatus = "fail"
)

type EngineProperties struct {
	Backend           string                           `json:"backend"`
	ResetMode         enginecontract.ResetMode         `json:"reset_mode"`
	RequestedStrategy enginecontract.ExecutionStrategy `json:"requested_strategy"`
	ActiveStrategy    enginecontract.ExecutionStrategy `json:"active_strategy"`
	Fallback          bool                             `json:"fallback"`
	FallbackReason    string                           `json:"fallback_reason,omitempty"`
}

type Check struct {
	Name   string      `json:"name"`
	Status CheckStatus `json:"status"`
	Detail string      `json:"detail"`
}

type Report struct {
	SchemaVersion  string           `json:"schema_version"`
	Status         Status           `json:"status"`
	ArtifactSHA256 string           `json:"artifact_sha256"`
	Engine         EngineProperties `json:"engine"`
	Checks         []Check          `json:"checks"`
}

// Verify provisions a private source tree and workspace, executes four Runs,
// and checks the observable single-use instance contract. Factory workspace
// fields must be empty; Verify owns and removes all Host paths it creates.
func Verify(ctx context.Context, wasm []byte, config runtimeconfig.RunConfig, factory wazeroengine.Factory) (report Report, returnErr error) {
	digest := sha256.Sum256(wasm)
	report = Report{
		SchemaVersion:  SchemaVersion,
		Status:         StatusFailed,
		ArtifactSHA256: hex.EncodeToString(digest[:]),
		Checks:         make([]Check, 0, 22),
	}
	if len(wasm) == 0 {
		return report, errors.New("workspace verification artifact is empty")
	}
	if err := config.Validate(); err != nil {
		return report, fmt.Errorf("workspace verification config: %w", err)
	}
	if factory.WorkspaceManager != nil || factory.WorkspaceRef != "" || factory.WorkspaceOwner != "" {
		return report, errors.New("workspace verification factory is already bound")
	}
	if factory.Strategy == "" {
		factory.Strategy = enginecontract.StrategySingleUsePrepared
	}
	if factory.Strategy != enginecontract.StrategyFreshInstance {
		if factory.PreparedCapacity == 0 {
			factory.PreparedCapacity = 1
		}
		if factory.PreparedMaxCapacity == 0 {
			factory.PreparedMaxCapacity = factory.PreparedCapacity
		}
	}

	base, err := os.MkdirTemp("", "pysolate-workspace-verify-")
	if err != nil {
		return report, errors.New("create workspace verification root")
	}
	defer os.RemoveAll(base)
	if err := os.Chmod(base, 0o700); err != nil {
		return report, errors.New("secure workspace verification root")
	}
	source, err := os.MkdirTemp("", "pysolate-workspace-source-")
	if err != nil {
		return report, errors.New("create workspace verification source")
	}
	defer os.RemoveAll(source)
	if err := os.Mkdir(filepath.Join(source, "state"), 0o700); err != nil {
		return report, errors.New("prepare workspace verification source")
	}
	if err := os.WriteFile(filepath.Join(source, "state", "count.txt"), []byte("0"), 0o600); err != nil {
		return report, errors.New("prepare workspace verification input")
	}
	manager, err := workspace.NewManager(base)
	if err != nil {
		return report, errors.New("create workspace verification manager")
	}
	managerClosed := false
	defer func() {
		if !managerClosed {
			_ = manager.Close()
		}
	}()
	verificationLimits := workspace.Limits{MaxFiles: 64, MaxBytes: 8 << 10, MaxFileBytes: 1 << 10, MaxDepth: 8}
	ref, err := manager.CreateFromDirectory(source, verificationLimits)
	if err != nil {
		return report, sanitizeError("provision workspace verification source", err, base, source)
	}
	addCheck(&report, "opaque_workspace_ref", regexp.MustCompile(`^ws-[0-9a-f]{32}$`).MatchString(string(ref)), "workspace identity is opaque and path-free")
	if err := os.WriteFile(filepath.Join(source, "state", "count.txt"), []byte("999"), 0o600); err != nil {
		return report, errors.New("mutate detached verification source")
	}

	factory.WorkspaceManager = manager
	factory.WorkspaceRef = ref
	factory.WorkspaceOwner = "workspace-capsule-verifier"
	runner, err := factory.New(ctx, wasm, config)
	if err != nil {
		return report, sanitizeError("construct workspace verification runner", err, base, source)
	}
	runnerClosed := false
	defer func() {
		if !runnerClosed {
			_ = runner.Close(context.Background())
		}
	}()
	properties := runner.Properties()
	report.Engine = EngineProperties{
		Backend: properties.Backend, ResetMode: properties.ResetMode,
		RequestedStrategy: properties.RequestedStrategy, ActiveStrategy: properties.ActiveStrategy,
		Fallback: properties.Fallback, FallbackReason: properties.FallbackReason,
	}
	propertiesValid := properties.Validate() == nil && properties.ResetMode == enginecontract.ResetModeFreshInstance
	addCheck(&report, "single_use_instance_strategy", propertiesValid, "engine reports a valid fresh-instance lifecycle")

	contender, contenderErr := factory.New(ctx, wasm, config)
	if contender != nil {
		_ = contender.Close(context.Background())
	}
	addCheck(&report, "exclusive_workspace_lease", errors.Is(contenderErr, workspace.ErrWorkspaceBusy), "a second Runner cannot acquire the active workspace")

	payloads := make([][]byte, 0, 5)
	first, raw, err := run(ctx, runner, "workspace-verify-1", `
from pathlib import Path
path = Path("/workspace/state/count.txt")
before = int(path.read_text())
path.write_text(str(before + 1))
Path("/tmp/run-only.txt").write_text("first")
instance_heap_marker = "first"
result = {"before": before}
`)
	if err != nil {
		return report, sanitizeError("execute workspace verification run 1", err, base, source)
	}
	payloads = append(payloads, raw)
	var firstResult struct {
		Before int `json:"before"`
	}
	firstOK := first.Status == runtimeconfig.RunResponseOK && json.Unmarshal(first.Result, &firstResult) == nil && firstResult.Before == 0
	addCheck(&report, "source_copy_detached", firstOK, "Run 1 observes the provisioned copy, not a later source mutation")

	second, raw, err := run(ctx, runner, "workspace-verify-2", `
from pathlib import Path
result = {
    "count": int(Path("/workspace/state/count.txt").read_text()),
    "heap_continued": "instance_heap_marker" in globals(),
    "tmp_continued": Path("/tmp/run-only.txt").exists(),
}
`)
	if err != nil {
		return report, sanitizeError("execute workspace verification run 2", err, base, source)
	}
	payloads = append(payloads, raw)
	var secondResult struct {
		Count         int  `json:"count"`
		HeapContinued bool `json:"heap_continued"`
		TmpContinued  bool `json:"tmp_continued"`
	}
	secondOK := second.Status == runtimeconfig.RunResponseOK && json.Unmarshal(second.Result, &secondResult) == nil
	addCheck(&report, "workspace_continuation", secondOK && secondResult.Count == 1, "a fresh instance observes the prior ordinary-file write")
	addCheck(&report, "python_heap_fresh", secondOK && !secondResult.HeapContinued, "Python module globals do not continue")
	addCheck(&report, "tmp_fresh_after_success", secondOK && !secondResult.TmpContinued, "successful Run scratch state does not continue")

	failed, raw, err := run(ctx, runner, "workspace-verify-3", `
from pathlib import Path
Path("/workspace/state/count.txt").write_text("2")
Path("/tmp/failed-only.txt").write_text("failed")
raise RuntimeError("intentional workspace verification failure")
`)
	if err != nil {
		return report, sanitizeError("execute workspace verification run 3", err, base, source)
	}
	payloads = append(payloads, raw)
	addCheck(&report, "guest_failure_reported", failed.Status == runtimeconfig.RunResponseError && failed.Error != nil, "intentional Guest failure is reported without aborting verification")

	fourth, raw, err := run(ctx, runner, "workspace-verify-4", `
from pathlib import Path
result = {
    "count": int(Path("/workspace/state/count.txt").read_text()),
    "heap_continued": "instance_heap_marker" in globals(),
    "tmp_continued": Path("/tmp/failed-only.txt").exists(),
}
`)
	if err != nil {
		return report, sanitizeError("execute workspace verification run 4", err, base, source)
	}
	payloads = append(payloads, raw)
	var fourthResult struct {
		Count         int  `json:"count"`
		HeapContinued bool `json:"heap_continued"`
		TmpContinued  bool `json:"tmp_continued"`
	}
	fourthOK := fourth.Status == runtimeconfig.RunResponseOK && json.Unmarshal(fourth.Result, &fourthResult) == nil
	addCheck(&report, "failed_run_workspace_write_persists", fourthOK && fourthResult.Count == 2, "completed workspace writes survive a failed Run")
	addCheck(&report, "tmp_fresh_after_failure", fourthOK && !fourthResult.TmpContinued, "failed Run scratch state does not continue")
	addCheck(&report, "python_heap_fresh_after_failure", fourthOK && !fourthResult.HeapContinued, "failed Run does not preserve Python globals")

	fifth, raw, err := run(ctx, runner, "workspace-verify-5", `
from pathlib import Path
import os
import stat

ops = Path("/workspace/ops")
ops.mkdir()
original = ops / "original.txt"
original.write_text("abcdef")
listed = original.name in [entry.name for entry in ops.iterdir()]
file_is_regular = stat.S_ISREG(original.stat().st_mode)
dir_is_directory = stat.S_ISDIR(ops.stat().st_mode)
renamed = original.rename(ops / "renamed.txt")
os.truncate(renamed, 3)
truncated = renamed.read_text() == "abc"
renamed.unlink()
ops.rmdir()
removed = not ops.exists()

link_rejected = True
link_target = Path("/workspace/link-target.txt")
link_target.write_text("target")
try:
    os.symlink(str(link_target), "/workspace/symlink.txt")
    link_rejected = False
except (OSError, NotImplementedError, AttributeError):
    pass
try:
    os.link(str(link_target), "/workspace/hardlink.txt")
    link_rejected = False
except (OSError, NotImplementedError, AttributeError):
    pass
link_target.unlink()

quota_path = Path("/workspace/quota.bin")
quota_rejected = False
try:
    quota_path.write_bytes(b"x" * 1025)
except OSError:
    quota_rejected = True
quota_size = quota_path.stat().st_size if quota_path.exists() else 0
if quota_path.exists():
    quota_path.unlink()

git_rejected = False
try:
    Path("/workspace/.git").mkdir()
except OSError:
    git_rejected = True

ambient_denied = False
try:
    Path("/etc/passwd").read_bytes()
except OSError:
    ambient_denied = True

result = {
    "created_and_listed": listed,
    "ordinary_types": file_is_regular and dir_is_directory,
    "renamed_and_truncated": truncated,
    "deleted": removed,
    "link_rejected": link_rejected,
    "quota_rejected": quota_rejected and quota_size == 0,
    "git_rejected": git_rejected,
    "ambient_denied": ambient_denied,
}
`)
	if err != nil {
		return report, sanitizeError("execute workspace verification run 5", err, base, source)
	}
	payloads = append(payloads, raw)
	if fifth.Status != runtimeconfig.RunResponseOK {
		message := "unknown Guest error"
		if fifth.Error != nil {
			message = fifth.Error.Code + ": " + fifth.Error.Message
			if fifth.Error.Traceback != nil {
				message += ": " + *fifth.Error.Traceback
			}
		}
		return report, sanitizeError("workspace filesystem probe did not complete", errors.New(message), base, source)
	}
	var fifthResult struct {
		CreatedAndListed    bool `json:"created_and_listed"`
		OrdinaryTypes       bool `json:"ordinary_types"`
		RenamedAndTruncated bool `json:"renamed_and_truncated"`
		Deleted             bool `json:"deleted"`
		LinkRejected        bool `json:"link_rejected"`
		QuotaRejected       bool `json:"quota_rejected"`
		GitRejected         bool `json:"git_rejected"`
		AmbientDenied       bool `json:"ambient_denied"`
	}
	fifthOK := fifth.Status == runtimeconfig.RunResponseOK && json.Unmarshal(fifth.Result, &fifthResult) == nil
	addCheck(&report, "ordinary_create_and_list", fifthOK && fifthResult.CreatedAndListed, "Guest can create and list ordinary files and directories")
	addCheck(&report, "ordinary_object_types", fifthOK && fifthResult.OrdinaryTypes, "Guest stat distinguishes regular files and directories")
	addCheck(&report, "ordinary_rename_and_truncate", fifthOK && fifthResult.RenamedAndTruncated, "Guest can rename and truncate an ordinary file")
	addCheck(&report, "ordinary_delete", fifthOK && fifthResult.Deleted, "Guest can delete ordinary files and directories")
	addCheck(&report, "link_objects_rejected", fifthOK && fifthResult.LinkRejected, "Guest cannot create symbolic or hard links")
	addCheck(&report, "workspace_quota_enforced", fifthOK && fifthResult.QuotaRejected, "an over-limit Guest write is rejected without committed bytes")
	addCheck(&report, "git_metadata_rejected", fifthOK && fifthResult.GitRejected, "Guest cannot create a .git metadata directory")
	addCheck(&report, "ambient_host_filesystem_denied", fifthOK && fifthResult.AmbientDenied, "Guest cannot read ambient Host filesystem paths")

	pathsHidden := true
	for _, payload := range payloads {
		text := string(payload)
		if strings.Contains(text, base) || strings.Contains(text, source) {
			pathsHidden = false
		}
	}
	addCheck(&report, "host_paths_hidden", pathsHidden, "Guest responses contain no verifier Host paths")

	if err := runner.Close(ctx); err != nil {
		addCheck(&report, "runner_cleanup", false, "Runner cleanup failed")
	} else {
		runnerClosed = true
		lease, acquireErr := manager.Acquire(ref, "workspace-capsule-verifier-post-close")
		released := false
		if acquireErr == nil {
			released = lease.Release() == nil
		}
		addCheck(&report, "runner_cleanup", acquireErr == nil && released, "Runner close releases module filesystems and the workspace lease")
	}
	if err := manager.Close(); err != nil {
		addCheck(&report, "manager_cleanup", false, "Manager cleanup failed")
	} else {
		managerClosed = true
		children, readErr := os.ReadDir(base)
		addCheck(&report, "manager_cleanup", readErr == nil && len(children) == 0, "Manager close removes all managed workspace roots")
	}

	report.Status = StatusVerified
	for _, check := range report.Checks {
		if check.Status != CheckPass {
			report.Status = StatusFailed
			break
		}
	}
	return report, nil
}

func run(ctx context.Context, runner enginecontract.Runner, runID, code string) (runtimeconfig.RunResponse, []byte, error) {
	request, err := json.Marshal(runtimeconfig.RunRequest{RunID: runID, Code: code, Inputs: json.RawMessage(`{}`)})
	if err != nil {
		return runtimeconfig.RunResponse{}, nil, err
	}
	payload, err := runner.Run(ctx, request, "")
	if err != nil {
		return runtimeconfig.RunResponse{}, nil, err
	}
	decodedRequest, err := runtimeconfig.DecodeRunRequest(request)
	if err != nil {
		return runtimeconfig.RunResponse{}, payload, err
	}
	response, err := runtimeconfig.DecodeAndValidateRunResponse(decodedRequest, payload)
	return response, payload, err
}

func addCheck(report *Report, name string, passed bool, detail string) {
	status := CheckFail
	if passed {
		status = CheckPass
	}
	report.Checks = append(report.Checks, Check{Name: name, Status: status, Detail: detail})
}

func sanitizeError(action string, err error, paths ...string) error {
	message := err.Error()
	for _, value := range paths {
		if value != "" {
			message = strings.ReplaceAll(message, value, "[HOST_PATH]")
		}
	}
	return fmt.Errorf("%s: %s", action, message)
}

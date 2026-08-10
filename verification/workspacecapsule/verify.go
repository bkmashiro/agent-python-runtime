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
	"sync/atomic"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

const (
	SchemaVersion       = "workspace-capsule-verification/v2"
	MaxStressIterations = 1000
)

type Options struct {
	StressIterations           int
	CancellationBarrierTimeout time.Duration
}

func DefaultOptions() Options {
	return Options{CancellationBarrierTimeout: 9 * time.Second}
}

func (options Options) validate() error {
	if options.StressIterations < 0 || options.StressIterations > MaxStressIterations {
		return fmt.Errorf("stress iterations must be between 0 and %d", MaxStressIterations)
	}
	if options.CancellationBarrierTimeout <= 0 || options.CancellationBarrierTimeout > 15*time.Second {
		return errors.New("cancellation barrier timeout must be greater than zero and at most 15s")
	}
	return nil
}

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

type StressSummary struct {
	RequestedIterations int  `json:"requested_iterations"`
	CompletedIterations int  `json:"completed_iterations"`
	OpenFDsBefore       *int `json:"open_fds_before,omitempty"`
	OpenFDsAfter        *int `json:"open_fds_after,omitempty"`
	OpenFDDelta         *int `json:"open_fd_delta,omitempty"`
}

type Report struct {
	SchemaVersion  string           `json:"schema_version"`
	Status         Status           `json:"status"`
	ArtifactSHA256 string           `json:"artifact_sha256"`
	Engine         EngineProperties `json:"engine"`
	Checks         []Check          `json:"checks"`
	Stress         *StressSummary   `json:"stress,omitempty"`
}

// Verify uses the default bounded interruption profile and no stress loop.
func Verify(ctx context.Context, wasm []byte, config runtimeconfig.RunConfig, factory wazeroengine.Factory) (Report, error) {
	return VerifyWithOptions(ctx, wasm, config, factory, DefaultOptions())
}

// VerifyWithOptions provisions a private source tree and workspace, executes
// real disposable instances, and checks the observable lifecycle contract.
// Factory workspace fields must be empty; verification owns and removes all
// Host paths it creates.
func VerifyWithOptions(ctx context.Context, wasm []byte, config runtimeconfig.RunConfig, factory wazeroengine.Factory, options Options) (report Report, returnErr error) {
	digest := sha256.Sum256(wasm)
	checkCapacity := 30
	if options.StressIterations > 0 {
		checkCapacity += 5
	}
	report = Report{
		SchemaVersion:  SchemaVersion,
		Status:         StatusFailed,
		ArtifactSHA256: hex.EncodeToString(digest[:]),
		Checks:         make([]Check, 0, checkCapacity),
	}
	if err := options.validate(); err != nil {
		return report, fmt.Errorf("workspace verification options: %w", err)
	}
	if len(wasm) == 0 {
		return report, errors.New("workspace verification artifact is empty")
	}
	if err := config.Validate(); err != nil {
		return report, fmt.Errorf("workspace verification config: %w", err)
	}
	if factory.WorkspaceManager != nil || factory.WorkspaceRef != "" || factory.WorkspaceOwner != "" || factory.BrokerFactory != nil {
		return report, errors.New("workspace verification factory already has Host capability bindings")
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
	cancelBarrier := make(chan struct{}, 1)
	var brokerCount atomic.Uint64
	barrierGrant := capability.Grant{
		Name: capability.FetchManyCapability,
		Targets: map[string]capability.TargetGrant{
			"barrier": {BaseURL: "https://workspace-verifier.invalid"},
		},
		MaxCalls: 1, MaxRequestsPerCall: 1, MaxTotalRequests: 1,
		MaxConcurrency: 1, MaxResponseBytes: 64, PerRequestTimeout: time.Second,
	}
	barrierFetcher := capability.FetcherFunc(func(_ context.Context, request capability.ResolvedRequest, _ uint32) (capability.FetchOutput, error) {
		if strings.HasSuffix(request.URL, "/ready") {
			select {
			case cancelBarrier <- struct{}{}:
			default:
			}
		}
		return capability.FetchOutput{StatusCode: 200, Body: []byte(`{"ready":true}`), ContentType: "application/json"}, nil
	})
	factory.BrokerFactory = func(context.Context) (*capability.Broker, error) {
		identity := brokerCount.Add(1)
		return capability.NewBroker(capability.Config{
			RunIdentity: fmt.Sprintf("workspace-verifier-%d", identity),
			Grants:      map[string]capability.Grant{barrierGrant.Name: barrierGrant},
		}, barrierFetcher)
	}
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

	brokerBudgetFresh := true
	for probe := 0; probe < 2; probe++ {
		response, raw, probeErr := run(ctx, runner, fmt.Sprintf("workspace-verify-broker-%d", probe+1), `
from agent_runtime.tools import fetch_many
items = fetch_many([{"request_id": "fresh", "target": "barrier", "path": "/fresh"}])
result = len(items) == 1 and items[0]["status"] == "ok"
`)
		if probeErr != nil {
			return report, sanitizeError("execute Broker freshness probe", probeErr, base, source)
		}
		payloads = append(payloads, raw)
		var accepted bool
		valid := response.Status == runtimeconfig.RunResponseOK && json.Unmarshal(response.Result, &accepted) == nil && accepted && response.Metrics != nil && response.Metrics.CapabilityCalls == 1
		brokerBudgetFresh = brokerBudgetFresh && valid
	}
	addCheck(&report, "broker_budget_fresh_across_runs", brokerBudgetFresh, "two disposable instances each receive a fresh one-call Broker budget")

	interruptConfig := config
	if interruptConfig.Timeout < 5*time.Second {
		interruptConfig.Timeout = 5 * time.Second
	}
	if err := runner.Close(ctx); err != nil {
		return report, sanitizeError("close contract verification runner", err, base, source)
	}
	runnerClosed = true
	runner, err = factory.New(ctx, wasm, interruptConfig)
	if err != nil {
		return report, sanitizeError("construct timeout verification runner", err, base, source)
	}
	runnerClosed = false

	timeoutPayload, timeoutErr := runRaw(ctx, runner, "workspace-verify-timeout", `
from pathlib import Path
Path("/workspace/state/timeout.txt").write_text("committed")
Path("/tmp/timeout-only.txt").write_text("ephemeral")
while True:
    pass
`)
	if len(timeoutPayload) > 0 {
		payloads = append(payloads, timeoutPayload)
	}
	timedOut := interruptObserved(timeoutPayload, timeoutErr, context.DeadlineExceeded)
	addCheck(&report, "configured_timeout_interrupts_instance", timedOut, "configured Run timeout interrupts a non-terminating Guest")
	timeoutRecovery, raw, recoveryErr := run(ctx, runner, "workspace-verify-timeout-recovery", `
from pathlib import Path
result = {
    "workspace_committed": Path("/workspace/state/timeout.txt").read_text() == "committed",
    "tmp_continued": Path("/tmp/timeout-only.txt").exists(),
}
`)
	if recoveryErr != nil {
		return report, sanitizeError("execute timeout recovery probe", recoveryErr, base, source)
	}
	payloads = append(payloads, raw)
	var timeoutRecoveryResult struct {
		WorkspaceCommitted bool `json:"workspace_committed"`
		TmpContinued       bool `json:"tmp_continued"`
	}
	timeoutRecoveryOK := timeoutRecovery.Status == runtimeconfig.RunResponseOK && json.Unmarshal(timeoutRecovery.Result, &timeoutRecoveryResult) == nil
	addCheck(&report, "timeout_workspace_write_persists", timeoutRecoveryOK && timeoutRecoveryResult.WorkspaceCommitted, "workspace writes completed before timeout remain visible")
	addCheck(&report, "tmp_fresh_after_timeout", timeoutRecoveryOK && !timeoutRecoveryResult.TmpContinued, "timed-out Run scratch state does not continue")

	if err := runner.Close(ctx); err != nil {
		return report, sanitizeError("close timeout verification runner", err, base, source)
	}
	runnerClosed = true
	cancellationConfig := config
	if cancellationConfig.Timeout < 10*time.Second {
		cancellationConfig.Timeout = 10 * time.Second
	}
	runner, err = factory.New(ctx, wasm, cancellationConfig)
	if err != nil {
		return report, sanitizeError("construct cancellation verification runner", err, base, source)
	}
	runnerClosed = false

	cancelContext, cancel := context.WithCancel(ctx)
	type cancelOutcome struct {
		payload []byte
		err     error
	}
	cancelDone := make(chan cancelOutcome, 1)
	go func() {
		payload, runErr := runRaw(cancelContext, runner, "workspace-verify-cancel", `
from pathlib import Path
from agent_runtime.tools import fetch_many
Path("/workspace/state/cancel.txt").write_text("committed")
Path("/tmp/cancel-only.txt").write_text("ephemeral")
fetch_many([{"request_id": "ready", "target": "barrier", "path": "/ready"}])
while True:
    pass
`)
		cancelDone <- cancelOutcome{payload: payload, err: runErr}
	}()
	barrierReached := false
	var cancelResult cancelOutcome
	barrierTimer := time.NewTimer(options.CancellationBarrierTimeout)
	select {
	case <-cancelBarrier:
		barrierReached = true
		cancel()
		cancelResult = <-cancelDone
	case cancelResult = <-cancelDone:
		cancel()
	case <-barrierTimer.C:
		cancel()
		cancelResult = <-cancelDone
	}
	if !barrierTimer.Stop() {
		select {
		case <-barrierTimer.C:
		default:
		}
	}
	if len(cancelResult.payload) > 0 {
		payloads = append(payloads, cancelResult.payload)
	}
	if !barrierReached {
		evidence := cancelResult.err
		if evidence == nil {
			bounded := cancelResult.payload
			if len(bounded) > 4096 {
				bounded = bounded[:4096]
			}
			evidence = errors.New(string(bounded))
		}
		return report, sanitizeError("cancellation barrier was not reached", evidence, base, source)
	}
	cancelled := interruptObserved(cancelResult.payload, cancelResult.err, context.Canceled)
	addCheck(&report, "context_cancellation_interrupts_instance", cancelled, "Host context cancellation interrupts a non-terminating Guest")
	cancelRecovery, raw, recoveryErr := run(ctx, runner, "workspace-verify-cancel-recovery", `
from pathlib import Path
result = {
    "workspace_committed": Path("/workspace/state/cancel.txt").read_text() == "committed",
    "tmp_continued": Path("/tmp/cancel-only.txt").exists(),
}
`)
	if recoveryErr != nil {
		return report, sanitizeError("execute cancellation recovery probe", recoveryErr, base, source)
	}
	payloads = append(payloads, raw)
	var cancelRecoveryResult struct {
		WorkspaceCommitted bool `json:"workspace_committed"`
		TmpContinued       bool `json:"tmp_continued"`
	}
	cancelRecoveryOK := cancelRecovery.Status == runtimeconfig.RunResponseOK && json.Unmarshal(cancelRecovery.Result, &cancelRecoveryResult) == nil
	addCheck(&report, "cancelled_workspace_write_persists", cancelRecoveryOK && cancelRecoveryResult.WorkspaceCommitted, "workspace writes completed before cancellation remain visible")
	addCheck(&report, "tmp_fresh_after_cancellation", cancelRecoveryOK && !cancelRecoveryResult.TmpContinued, "cancelled Run scratch state does not continue")

	if options.StressIterations > 0 {
		runStress(ctx, runner, options.StressIterations, &report, &payloads, openDescriptorCount)
	}
	expectedBrokers := uint64(11 + options.StressIterations)
	addCheck(&report, "broker_fresh_per_run", brokerCount.Load() == expectedBrokers, fmt.Sprintf("Host constructed %d independent per-Run Brokers", expectedBrokers))

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

type descriptorSampler func() (int, bool)

func runStress(ctx context.Context, runner enginecontract.Runner, iterations int, report *Report, payloads *[][]byte, sampleDescriptors descriptorSampler) {
	summary := &StressSummary{RequestedIterations: iterations}
	report.Stress = summary
	openFDsBefore, descriptorOracleAvailable := sampleDescriptors()
	allRunsSucceeded := true
	sequenceExact := true
	heapFresh := true
	tmpFresh := true
	for iteration := 0; iteration < iterations; iteration++ {
		response, raw, err := run(ctx, runner, fmt.Sprintf("workspace-stress-%04d", iteration), `
from pathlib import Path
path = Path("/workspace/state/stress-count.txt")
before = int(path.read_text()) if path.exists() else 0
tmp_path = Path("/tmp/stress-only.txt")
result = {
    "before": before,
    "heap_continued": "stress_heap_marker" in globals(),
    "tmp_continued": tmp_path.exists(),
}
path.write_text(str(before + 1))
tmp_path.write_text("ephemeral")
stress_heap_marker = before
`)
		if err != nil {
			allRunsSucceeded = false
			sequenceExact = false
			heapFresh = false
			tmpFresh = false
			break
		}
		*payloads = append(*payloads, raw)
		var result struct {
			Before        int  `json:"before"`
			HeapContinued bool `json:"heap_continued"`
			TmpContinued  bool `json:"tmp_continued"`
		}
		valid := response.Status == runtimeconfig.RunResponseOK && json.Unmarshal(response.Result, &result) == nil
		if !valid {
			allRunsSucceeded = false
			sequenceExact = false
			heapFresh = false
			tmpFresh = false
			break
		}
		summary.CompletedIterations++
		sequenceExact = sequenceExact && result.Before == iteration
		heapFresh = heapFresh && !result.HeapContinued
		tmpFresh = tmpFresh && !result.TmpContinued
	}
	completedAll := summary.CompletedIterations == iterations
	addCheck(report, "stress_runs_complete", allRunsSucceeded && completedAll, fmt.Sprintf("all %d requested stress Runs return valid responses", iterations))
	addCheck(report, "stress_workspace_sequence", completedAll && sequenceExact, "workspace counter advances exactly once per disposable instance")
	addCheck(report, "stress_heap_fresh", completedAll && heapFresh, "Python globals remain fresh across every stress instance")
	addCheck(report, "stress_tmp_fresh", completedAll && tmpFresh, "scratch state remains fresh across every stress instance")
	if openFDsAfter, afterAvailable := sampleDescriptors(); descriptorOracleAvailable && afterAvailable {
		delta := openFDsAfter - openFDsBefore
		summary.OpenFDsBefore = intPointer(openFDsBefore)
		summary.OpenFDsAfter = intPointer(openFDsAfter)
		summary.OpenFDDelta = intPointer(delta)
		addCheck(report, "stress_open_fds_bounded", completedAll && delta <= 2, fmt.Sprintf("Host open descriptors changed by %d across stress Runs", delta))
	}
}

func intPointer(value int) *int { return &value }

func runRaw(ctx context.Context, runner enginecontract.Runner, runID, code string) ([]byte, error) {
	request, err := json.Marshal(runtimeconfig.RunRequest{RunID: runID, Code: code, Inputs: json.RawMessage(`{}`)})
	if err != nil {
		return nil, err
	}
	return runner.Run(ctx, request, "")
}

func interruptObserved(payload []byte, runErr error, target error) bool {
	if errors.Is(runErr, target) {
		return true
	}
	text := ""
	if runErr != nil {
		text = runErr.Error()
	}
	if len(payload) > 0 {
		text += " " + string(payload)
	}
	text = strings.ToLower(text)
	if target == context.DeadlineExceeded {
		return strings.Contains(text, "deadline exceeded") || strings.Contains(text, "timed out") || strings.Contains(text, "timeout")
	}
	return strings.Contains(text, "context canceled") || strings.Contains(text, "context cancelled")
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

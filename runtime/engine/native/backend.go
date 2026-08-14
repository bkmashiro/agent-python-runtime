package native

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/capabilityrpc"
	"github.com/bkmashiro/agent-python-runtime/runtime/lifecycle"
	"github.com/bkmashiro/agent-python-runtime/runtime/placement"
	"github.com/bkmashiro/agent-python-runtime/runtime/receipt"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

const EvidenceSchemaVersion = "pysolate.native-lifecycle.v1"

var (
	ErrInvalidConfig          = errors.New("invalid native backend config")
	ErrNativeExecution        = errors.New("native sandbox execution failed")
	ErrOutputLimit            = errors.New("native sandbox output exceeds bound")
	ErrRootFSIdentityMismatch = errors.New("native rootfs identity mismatch")
	ErrImageIdentityMismatch  = errors.New("native OCI image config identity mismatch")
)

type Config struct {
	RunscPath        string
	RootFS           string
	StateRoot        string
	Platform         string
	HostUDS          string
	NetworkMode      string
	ImageDigest      string
	ImageConfigPath  string
	Artifact         runtimeconfig.ExecutionArtifact
	Plan             *capability.Plan
	TrustedPrepare   string
	Timeout          time.Duration
	MaxOutputBytes   int
	MemoryLimitBytes int64
	PidsLimit        int64
	WorkspaceManager *workspace.Manager
	WorkspaceRef     workspace.Ref
}

type Evidence struct {
	SchemaVersion           string            `json:"schema_version"`
	Backend                 string            `json:"backend"`
	Platform                string            `json:"platform"`
	HostUDS                 string            `json:"host_uds"`
	NetworkMode             string            `json:"network_mode"`
	ImageDigest             string            `json:"image_digest"`
	ImageConfigVerified     bool              `json:"image_config_verified"`
	ImageVerifyNanoseconds  int64             `json:"image_verify_nanoseconds"`
	RootFSSHA256            string            `json:"rootfs_sha256"`
	RootFSVerifyNanoseconds int64             `json:"rootfs_verify_nanoseconds"`
	ArtifactIdentity        string            `json:"artifact_identity"`
	DecisionID              string            `json:"decision_id"`
	ExecutionID             string            `json:"execution_id"`
	CapabilityPlanSHA256    string            `json:"capability_plan_sha256"`
	CapabilityReceipts      []receipt.Receipt `json:"capability_receipts,omitempty"`
	Ready                   bool              `json:"ready"`
	ExitStatus              int               `json:"exit_status"`
	WallNanoseconds         int64             `json:"wall_nanoseconds"`
	UserCPUNanoseconds      int64             `json:"user_cpu_nanoseconds"`
	SystemCPUNanoseconds    int64             `json:"system_cpu_nanoseconds"`
	MaxRSSBytes             int64             `json:"max_rss_bytes"`
	ResourceSamples         uint64            `json:"resource_samples"`
	CgroupMemoryPeakBytes   uint64            `json:"cgroup_memory_peak_bytes"`
	PSSPeakBytes            uint64            `json:"pss_peak_bytes"`
	PrivateDirtyPeakBytes   uint64            `json:"private_dirty_peak_bytes"`
	ReadBytes               uint64            `json:"read_bytes"`
	WriteBytes              uint64            `json:"write_bytes"`
	PidsPeak                uint64            `json:"pids_peak"`
	MemoryLimitBytes        int64             `json:"memory_limit_bytes"`
	PidsLimit               int64             `json:"pids_limit"`
	DeleteReconciled        bool              `json:"delete_reconciled"`
	ControlRootUnmounted    bool              `json:"control_root_unmounted"`
	CgroupReconciled        bool              `json:"cgroup_reconciled"`
	WorkspaceRef            string            `json:"workspace_ref,omitempty"`
	WorkspaceTreeBefore     string            `json:"workspace_tree_before,omitempty"`
	WorkspaceTreeAfter      string            `json:"workspace_tree_after,omitempty"`
	WorkspaceLeaseReleased  bool              `json:"workspace_lease_released"`
	RunscStateEntriesAfter  int               `json:"runsc_state_entries_after"`
	ScratchRemoved          bool              `json:"scratch_removed"`
}

func (e Evidence) Lifecycle() lifecycle.Evidence {
	status := "error"
	if e.ExitStatus == 0 && e.Ready && e.DeleteReconciled && e.CgroupReconciled && e.ControlRootUnmounted && e.ScratchRemoved && e.WorkspaceLeaseReleased {
		status = "ok"
	}
	return lifecycle.Evidence{SchemaVersion: lifecycle.SchemaVersion, ExecutionID: e.ExecutionID, Backend: e.Backend, ArtifactIdentity: e.ArtifactIdentity, LogicalExecutions: 1, PhysicalExecutions: 1, Phases: []lifecycle.Phase{{Name: "artifact.verify", WallNanoseconds: e.RootFSVerifyNanoseconds}, {Name: "backend.execute", WallNanoseconds: e.WallNanoseconds}}, Resources: lifecycle.Resources{UserCPUNanoseconds: e.UserCPUNanoseconds, SystemCPUNanoseconds: e.SystemCPUNanoseconds, MaxRSSBytes: e.MaxRSSBytes, CgroupMemoryPeakBytes: e.CgroupMemoryPeakBytes, PSSPeakBytes: e.PSSPeakBytes, PrivateDirtyPeakBytes: e.PrivateDirtyPeakBytes, ReadBytes: e.ReadBytes, WriteBytes: e.WriteBytes, PidsPeak: e.PidsPeak, Samples: e.ResourceSamples}, Cleanup: lifecycle.Cleanup{Process: e.DeleteReconciled, Socket: e.ScratchRemoved, Mount: e.ControlRootUnmounted, Cgroup: e.CgroupReconciled, WorkspaceLease: e.WorkspaceLeaseReleased}, TerminalStatus: status}
}

type Backend struct{ Config Config }

func (backend Backend) Execute(ctx context.Context, plan placement.Plan, raw []byte) ([]byte, error) {
	output, _, err := backend.ExecuteWithEvidence(ctx, plan, raw)
	return output, err
}

func (backend Backend) ExecuteWithEvidence(ctx context.Context, plan placement.Plan, raw []byte) ([]byte, Evidence, error) {
	config := backend.Config
	if err := validateConfig(config, plan); err != nil {
		return nil, Evidence{}, err
	}
	imageVerifyStarted := time.Now()
	if err := VerifyOCIImageConfig(config.ImageConfigPath, config.ImageDigest); err != nil {
		return nil, Evidence{SchemaVersion: EvidenceSchemaVersion, Backend: string(runtimeconfig.BackendNativeSandbox), ImageDigest: config.ImageDigest, ImageVerifyNanoseconds: time.Since(imageVerifyStarted).Nanoseconds()}, errors.Join(ErrImageIdentityMismatch, err)
	}
	imageVerifyNanoseconds := time.Since(imageVerifyStarted).Nanoseconds()
	verifyStarted := time.Now()
	verifiedRootFS, err := RootFSIdentity(config.RootFS)
	verifyNanoseconds := time.Since(verifyStarted).Nanoseconds()
	if err != nil || verifiedRootFS != config.Artifact.RootFSSHA256 {
		return nil, Evidence{SchemaVersion: EvidenceSchemaVersion, Backend: string(runtimeconfig.BackendNativeSandbox), Platform: config.Platform, HostUDS: config.HostUDS, NetworkMode: config.NetworkMode, ImageDigest: config.ImageDigest, ImageConfigVerified: true, ImageVerifyNanoseconds: imageVerifyNanoseconds, RootFSSHA256: verifiedRootFS, RootFSVerifyNanoseconds: verifyNanoseconds, ArtifactIdentity: config.Artifact.Identity(), DecisionID: plan.Decision.Identity}, ErrRootFSIdentityMismatch
	}
	request, err := runtimeconfig.DecodeRunRequest(raw)
	if err != nil {
		return nil, Evidence{}, err
	}
	if request.RunID == "" {
		return nil, Evidence{}, ErrInvalidConfig
	}
	executionID, err := randomIdentifier("native")
	if err != nil {
		return nil, Evidence{}, err
	}
	var workspaceLease *workspace.Lease
	var workspaceSource, workspaceTreeBefore string
	if plan.Decision.StateClass == runtimeconfig.StateWorkspaceRef {
		if config.WorkspaceManager == nil || config.WorkspaceRef == "" {
			return nil, Evidence{}, ErrInvalidConfig
		}
		workspaceLease, err = config.WorkspaceManager.Acquire(config.WorkspaceRef, executionID)
		if err != nil {
			return nil, Evidence{}, err
		}
		before, snapshotErr := workspaceLease.Snapshot()
		if snapshotErr != nil {
			_ = workspaceLease.Release()
			return nil, Evidence{}, snapshotErr
		}
		workspaceTreeBefore = before.Info.TreeSHA256
		workspaceSource, err = workspaceLease.BindMountSource()
		if err != nil {
			_ = workspaceLease.Release()
			return nil, Evidence{}, err
		}
	} else if config.WorkspaceManager != nil || config.WorkspaceRef != "" {
		return nil, Evidence{}, ErrInvalidConfig
	}
	var workspaceReleaseOnce sync.Once
	var workspaceReleaseErr error
	releaseWorkspace := func() {
		workspaceReleaseOnce.Do(func() {
			if workspaceLease != nil {
				workspaceReleaseErr = workspaceLease.Release()
			}
		})
	}
	defer releaseWorkspace()
	broker, err := capability.NewBroker(capability.Config{RunIdentity: executionID, Plan: config.Plan})
	if err != nil {
		return nil, Evidence{}, err
	}
	registry := capabilityrpc.NewRegistry()
	credential, err := randomCredential()
	if err != nil {
		return nil, Evidence{}, err
	}
	channelID, err := randomIdentifier("channel")
	if err != nil {
		return nil, Evidence{}, err
	}
	invocationID := "invocation-" + shortIdentity(plan.Decision.RequestSHA256)
	runDir, err := os.MkdirTemp(config.StateRoot, "native-run-")
	if err != nil {
		return nil, Evidence{}, err
	}
	if err := os.Chmod(runDir, 0o700); err != nil {
		_ = os.RemoveAll(runDir)
		return nil, Evidence{}, err
	}
	defer os.RemoveAll(runDir)
	channelDir := filepath.Join(runDir, "channel")
	runscState := filepath.Join(runDir, "runsc")
	if err := os.MkdirAll(channelDir, 0o700); err != nil {
		return nil, Evidence{}, err
	}
	if err := os.MkdirAll(runscState, 0o700); err != nil {
		return nil, Evidence{}, err
	}
	socketPath := filepath.Join(channelDir, "broker.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, Evidence{}, err
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		listener.Close()
		return nil, Evidence{}, err
	}
	if err := registry.Open(capabilityrpc.ChannelConfig{ID: channelID, Credential: credential, InvocationID: invocationID,
		ExecutionID: executionID, Transport: capabilityrpc.TransportUnixHTTP, ExpiresAt: time.Now().Add(config.Timeout + 5*time.Second),
		MaxRequestBytes: 1 << 20, Broker: broker}); err != nil {
		listener.Close()
		return nil, Evidence{}, err
	}
	server := &http.Server{Handler: capabilityrpc.HTTPHandler(registry), ReadHeaderTimeout: 2 * time.Second}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	var cleanupOnce sync.Once
	cleanupTransport := func() {
		cleanupOnce.Do(func() {
			registry.Revoke(channelID)
			_ = server.Close()
			_ = listener.Close()
			<-serveDone
		})
	}
	defer cleanupTransport()

	envelope, err := json.Marshal(struct {
		SchemaVersion  string          `json:"schema_version"`
		Request        json.RawMessage `json:"request"`
		TrustedPrepare string          `json:"trusted_prepare"`
	}{SchemaVersion: "pysolate.native-run.v1", Request: append(json.RawMessage(nil), raw...), TrustedPrepare: config.TrustedPrepare})
	if err != nil {
		return nil, Evidence{}, err
	}
	environment := []string{
		"PATH=/usr/local/bin:/usr/bin:/bin", "PYTHONPATH=/opt/pysolate", "LANG=C.UTF-8",
		"PYSOLATE_RPC_SOCKET=/run/pysolate/broker.sock", "PYSOLATE_RPC_CHANNEL_ID=" + channelID,
		"PYSOLATE_RPC_INVOCATION_ID=" + invocationID, "PYSOLATE_RPC_EXECUTION_ID=" + executionID,
		"PYSOLATE_RPC_PLAN_SHA256=" + config.Plan.Identity(), "PYSOLATE_RPC_CREDENTIAL=" + credential,
	}
	bundleDir := filepath.Join(runDir, "bundle")
	containerID := "pysolate-" + strings.TrimPrefix(executionID, "native-")
	if err := writeOCIBundle(bundleDir, config.RootFS, channelDir, workspaceSource, containerID, environment, config.MemoryLimitBytes, config.PidsLimit); err != nil {
		return nil, Evidence{}, err
	}
	runCtx, cancel := context.WithTimeout(ctx, config.Timeout)
	defer cancel()
	globalArgs := []string{"--root=" + runscState, "--network=" + config.NetworkMode, "--platform=" + config.Platform, "--host-uds=" + config.HostUDS}
	args := append(append([]string(nil), globalArgs...), "run", "--bundle", bundleDir, containerID)
	command := exec.CommandContext(runCtx, config.RunscPath, args...)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return nil
		}
		return syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	}
	command.WaitDelay = 2 * time.Second
	command.Stdin = bytes.NewReader(envelope)
	command.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/root", "LANG=C.UTF-8"}
	stdout := &boundedBuffer{maximum: config.MaxOutputBytes}
	stderr := &boundedBuffer{maximum: config.MaxOutputBytes}
	command.Stdout, command.Stderr = stdout, stderr
	started := time.Now()
	runErr := command.Start()
	var resources resourceAggregate
	if runErr == nil {
		sampleCtx, stopSampling := context.WithCancel(context.Background())
		samples := sampleCgroup(sampleCtx, filepath.Join("/sys/fs/cgroup", containerID))
		runErr = command.Wait()
		stopSampling()
		resources = <-samples
	}
	wall := time.Since(started)
	evidence := Evidence{SchemaVersion: EvidenceSchemaVersion, Backend: string(runtimeconfig.BackendNativeSandbox), Platform: config.Platform, HostUDS: config.HostUDS, NetworkMode: config.NetworkMode,
		ImageDigest: config.ImageDigest, ImageConfigVerified: true, ImageVerifyNanoseconds: imageVerifyNanoseconds, RootFSSHA256: verifiedRootFS, RootFSVerifyNanoseconds: verifyNanoseconds, ArtifactIdentity: config.Artifact.Identity(), DecisionID: plan.Decision.Identity, ExecutionID: executionID,
		CapabilityPlanSHA256: config.Plan.Identity(), WorkspaceRef: string(config.WorkspaceRef), WorkspaceTreeBefore: workspaceTreeBefore, ExitStatus: exitStatus(runErr), WallNanoseconds: wall.Nanoseconds(), MemoryLimitBytes: config.MemoryLimitBytes, PidsLimit: config.PidsLimit}
	evidence.ResourceSamples = resources.Samples
	evidence.CgroupMemoryPeakBytes = resources.MemoryCurrentPeak
	evidence.PSSPeakBytes = resources.PSSPeak
	evidence.PrivateDirtyPeakBytes = resources.PrivateDirtyPeak
	evidence.ReadBytes, evidence.WriteBytes, evidence.PidsPeak = resources.ReadBytes, resources.WriteBytes, resources.PidsPeak
	if command.ProcessState != nil {
		evidence.UserCPUNanoseconds = command.ProcessState.UserTime().Nanoseconds()
		evidence.SystemCPUNanoseconds = command.ProcessState.SystemTime().Nanoseconds()
		if usage, ok := command.ProcessState.SysUsage().(*syscall.Rusage); ok {
			evidence.MaxRSSBytes = usage.Maxrss * 1024
		}
	}
	var workspaceSnapshotErr error
	if workspaceLease != nil {
		after, snapshotErr := workspaceLease.Snapshot()
		workspaceSnapshotErr = snapshotErr
		if snapshotErr == nil {
			evidence.WorkspaceTreeAfter = after.Info.TreeSHA256
		}
	}
	releaseWorkspace()
	evidence.WorkspaceLeaseReleased = workspaceLease == nil || workspaceReleaseErr == nil
	evidence.DeleteReconciled = reconcileRunsc(config.RunscPath, globalArgs, containerID, 2*time.Second)
	evidence.CgroupReconciled = !pathExists(filepath.Join("/sys/fs/cgroup", containerID))
	evidence.ControlRootUnmounted = unmountRunscRoot(runscState)
	var response struct {
		Status    string `json:"status"`
		Readiness struct {
			Status     string `json:"status"`
			PlanSHA256 string `json:"plan_sha256"`
		} `json:"readiness"`
	}
	decodeErr := json.Unmarshal(stdout.Bytes(), &response)
	evidence.Ready = decodeErr == nil && response.Readiness.Status == "ready" && response.Readiness.PlanSHA256 == config.Plan.Identity()
	evidence.CapabilityReceipts = broker.Receipts()
	cleanupTransport()
	cleanupErr := os.RemoveAll(runDir)
	evidence.ScratchRemoved = cleanupErr == nil && !pathExists(runDir)
	if evidence.ScratchRemoved {
		evidence.RunscStateEntriesAfter = 0
	} else {
		evidence.RunscStateEntriesAfter = waitStateEntries(runscState, 2*time.Second)
	}
	if runErr != nil || workspaceSnapshotErr != nil || !evidence.WorkspaceLeaseReleased || stdout.overflow || stderr.overflow || decodeErr != nil || response.Status != "ok" || !evidence.Ready || !evidence.DeleteReconciled || !evidence.CgroupReconciled || !evidence.ControlRootUnmounted || !evidence.ScratchRemoved || evidence.RunscStateEntriesAfter != 0 {
		if stdout.overflow || stderr.overflow {
			return stdout.Bytes(), evidence, fmt.Errorf("%w: %w", ErrNativeExecution, ErrOutputLimit)
		}
		return stdout.Bytes(), evidence, fmt.Errorf("%w: status=%d stderr=%s", ErrNativeExecution, evidence.ExitStatus, boundedText(stderr.Bytes()))
	}
	return stdout.Bytes(), evidence, nil
}

func imageIdentityBound(config Config) bool {
	return config.ImageDigest != "" && config.ImageDigest == config.Artifact.ImageDigest
}

func validateConfig(config Config, plan placement.Plan) error {
	if config.RunscPath == "" || config.RootFS == "" || config.StateRoot == "" || config.Platform == "" || config.HostUDS != "open" || config.NetworkMode != "sandbox" ||
		!imageIdentityBound(config) || !filepath.IsAbs(config.ImageConfigPath) || config.Artifact.Validate() != nil || config.Artifact.Backend != runtimeconfig.BackendNativeSandbox ||
		config.Plan == nil || config.Plan.Identity() == "" || config.Timeout <= 0 || config.Timeout > 5*time.Minute ||
		config.MaxOutputBytes <= 0 || config.MaxOutputBytes > 16<<20 || config.MemoryLimitBytes < 64<<20 || config.MemoryLimitBytes > 4<<30 || config.PidsLimit < 8 || config.PidsLimit > 1024 || plan.Decision.Backend != runtimeconfig.BackendNativeSandbox || plan.Decision.Identity == "" {
		return ErrInvalidConfig
	}
	if plan.Decision.StateClass == runtimeconfig.StateWorkspaceRef {
		if config.WorkspaceManager == nil || config.WorkspaceRef == "" {
			return ErrInvalidConfig
		}
	} else if config.WorkspaceManager != nil || config.WorkspaceRef != "" {
		return ErrInvalidConfig
	}
	return nil
}

func unmountRunscRoot(root string) bool {
	path := filepath.Join(root, "null-netns")
	err := syscall.Unmount(path, 0)
	return err == nil || errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.EINVAL)
}

func reconcileRunsc(runscPath string, globalArgs []string, containerID string, maximum time.Duration) bool {
	deleteCtx, deleteCancel := context.WithTimeout(context.Background(), maximum)
	deleteArgs := append(append([]string(nil), globalArgs...), "delete", "--force", containerID)
	_ = exec.CommandContext(deleteCtx, runscPath, deleteArgs...).Run()
	deleteCancel()
	deadline := time.Now().Add(maximum)
	for {
		listCtx, listCancel := context.WithTimeout(context.Background(), time.Second)
		listArgs := append(append([]string(nil), globalArgs...), "list", "--format=json")
		output, err := exec.CommandContext(listCtx, runscPath, listArgs...).Output()
		listCancel()
		var containers []json.RawMessage
		if err == nil && len(output) <= 1<<20 && json.Unmarshal(output, &containers) == nil && len(containers) == 0 {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func waitStateEntries(path string, maximum time.Duration) int {
	deadline := time.Now().Add(maximum)
	for {
		entries, err := os.ReadDir(path)
		if os.IsNotExist(err) || (err == nil && len(entries) == 0) {
			return 0
		}
		if time.Now().After(deadline) {
			if err != nil {
				return -1
			}
			return len(entries)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func pathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func randomIdentifier(prefix string) (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return prefix + "-" + hex.EncodeToString(value[:]), nil
}

func randomCredential() (string, error) {
	var value [32]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}
func shortIdentity(value string) string {
	value = strings.TrimPrefix(value, "sha256:")
	if len(value) > 24 {
		value = value[:24]
	}
	return value
}
func exitStatus(err error) int {
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	return -1
}
func boundedText(value []byte) string {
	if len(value) > 1024 {
		value = value[:1024]
	}
	return strconv.QuoteToASCII(string(value))
}

type boundedBuffer struct {
	data     bytes.Buffer
	maximum  int
	overflow bool
}

func (buffer *boundedBuffer) Write(value []byte) (int, error) {
	original := len(value)
	if buffer.data.Len()+len(value) > buffer.maximum {
		keep := buffer.maximum - buffer.data.Len()
		if keep > 0 {
			_, _ = buffer.data.Write(value[:keep])
		}
		buffer.overflow = true
		return original, nil
	}
	_, _ = buffer.data.Write(value)
	return original, nil
}
func (buffer *boundedBuffer) Bytes() []byte { return append([]byte(nil), buffer.data.Bytes()...) }

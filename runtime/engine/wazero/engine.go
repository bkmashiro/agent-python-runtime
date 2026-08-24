package wazero

import (
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	goruntime "runtime"
	"sort"
	"strings"
	"sync"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	"github.com/bkmashiro/agent-python-runtime/runtime/observe"
	"github.com/bkmashiro/agent-python-runtime/runtime/passregistration"
	"github.com/bkmashiro/agent-python-runtime/runtime/playback"
	"github.com/bkmashiro/agent-python-runtime/runtime/preparedregion"
	"github.com/bkmashiro/agent-python-runtime/runtime/receipt"
	"github.com/bkmashiro/agent-python-runtime/runtime/sourcepatch"
	"github.com/bkmashiro/agent-python-runtime/runtime/valueslot"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
	wazerort "github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	experimentalsysfs "github.com/tetratelabs/wazero/experimental/sysfs"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	wazerosys "github.com/tetratelabs/wazero/sys"
)

type BrokerFactory func(context.Context) (*capability.Broker, error)

// Factory configures the portable PoC backend. Every Run gets a fresh guest.
type Factory struct {
	BrokerFactory    BrokerFactory
	WorkspaceManager *workspace.Manager
	WorkspaceRef     workspace.Ref
	WorkspaceOwner   string
	PreparedRegions  *preparedregion.PreparedRegionTable
	ValueSlots       *valueslot.Table
}

func (Factory) Name() string { return "wazero" }

func (factory Factory) New(ctx context.Context, wasm []byte, config runtimeconfig.RunConfig) (enginecontract.Runner, error) {
	binding, err := factory.validatedBinding(config)
	if err != nil {
		return nil, err
	}
	return newEngine(ctx, wasm, config, factory.BrokerFactory, binding, factory.PreparedRegions, factory.ValueSlots)
}

func (factory Factory) validatedBinding(config runtimeconfig.RunConfig) (*workspaceBinding, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if (config.Mechanisms.SemanticPreDispatch || config.Mechanisms.SplitPhaseCalls) && factory.BrokerFactory == nil {
		return nil, errors.New("Host scheduling requires a capability Broker factory")
	}
	if (config.Mechanisms.ProgrammaticToolCalling || config.Mechanisms.ApprovalSuspension) && factory.BrokerFactory == nil {
		return nil, errors.New("programmatic tool calling and approval suspension require a capability Broker factory")
	}
	fields := 0
	if factory.WorkspaceManager != nil {
		fields++
	}
	if factory.WorkspaceRef != "" {
		fields++
	}
	if factory.WorkspaceOwner != "" {
		fields++
	}
	if fields != 0 && fields != 3 {
		return nil, errors.New("workspace binding requires manager, reference, and owner")
	}
	if config.DeterministicVerification != nil && fields != 0 {
		return nil, runtimeconfig.ErrDeterministicVerificationAdmission
	}
	var binding *workspaceBinding
	if factory.WorkspaceManager != nil {
		binding = &workspaceBinding{manager: factory.WorkspaceManager, ref: factory.WorkspaceRef, owner: factory.WorkspaceOwner}
	}
	if factory.PreparedRegions != nil && (!config.Mechanisms.SemanticAnalysis || config.Mechanisms.Streaming) {
		return nil, errors.New("prepared region table requires non-streaming semantic analysis")
	}
	if config.Mechanisms.ValueSlots && factory.ValueSlots == nil {
		return nil, errors.New("value slots require a Host value-slot table")
	}
	if factory.ValueSlots != nil && !config.Mechanisms.ValueSlots {
		return nil, errors.New("Host value-slot tables require the value-slot mechanism")
	}
	return binding, nil
}

var _ enginecontract.Factory = Factory{}
var _ enginecontract.Runner = (*Engine)(nil)

type forbiddenStdout struct {
	mu   sync.Mutex
	used bool
}

func (stdout *forbiddenStdout) Write(payload []byte) (int, error) {
	if len(payload) != 0 {
		stdout.mu.Lock()
		stdout.used = true
		stdout.mu.Unlock()
	}
	return len(payload), nil
}

func (stdout *forbiddenStdout) Used() bool {
	stdout.mu.Lock()
	defer stdout.mu.Unlock()
	return stdout.used
}

type boundedDiagnostic struct {
	mu        sync.Mutex
	buffer    []byte
	truncated bool
}

func (diagnostic *boundedDiagnostic) Write(payload []byte) (int, error) {
	diagnostic.mu.Lock()
	defer diagnostic.mu.Unlock()
	original := len(payload)
	remaining := guestDiagnosticMax - len(diagnostic.buffer)
	if remaining > 0 {
		if len(payload) > remaining {
			payload = payload[:remaining]
		}
		diagnostic.buffer = append(diagnostic.buffer, payload...)
	}
	if original > remaining {
		diagnostic.truncated = true
	}
	return original, nil
}

func (diagnostic *boundedDiagnostic) String() string {
	diagnostic.mu.Lock()
	defer diagnostic.mu.Unlock()
	value := string(diagnostic.buffer)
	if diagnostic.truncated {
		value += "\n[guest stderr truncated]"
	}
	return value
}

func (diagnostic *boundedDiagnostic) Reset() {
	diagnostic.mu.Lock()
	defer diagnostic.mu.Unlock()
	diagnostic.buffer = diagnostic.buffer[:0]
	diagnostic.truncated = false
}

type preparedInstance struct {
	module    api.Module
	stderr    *boundedDiagnostic
	stdout    *forbiddenStdout
	temporary *workspace.Temporary
	cold      coldIOContinuation
}

type PreparedState struct {
	SchemaVersion     string  `json:"schema_version"`
	Selected          bool    `json:"selected"`
	Ready             bool    `json:"ready"`
	PreparedRuns      uint64  `json:"prepared_runs"`
	FreshFallbackRuns uint64  `json:"fresh_fallback_runs"`
	PrepareMS         float64 `json:"prepare_ms"`
}

type COWProbe struct {
	SchemaVersion         string   `json:"schema_version"`
	Platform              string   `json:"platform"`
	PreparedCompatible    bool     `json:"prepared_compatible"`
	MemoryCount           int      `json:"memory_count"`
	ImportedMemoryCount   int      `json:"imported_memory_count"`
	MemoryFixed           bool     `json:"memory_fixed"`
	MemoryMaximumDeclared bool     `json:"memory_maximum_declared"`
	MemoryCOWCandidate    bool     `json:"memory_cow_candidate"`
	COWSelected           bool     `json:"cow_selected"`
	Fallback              bool     `json:"fallback"`
	Blockers              []string `json:"blockers"`
}

type workspaceBinding struct {
	manager *workspace.Manager
	ref     workspace.Ref
	owner   string
}

var (
	errCOWEngineClosing = errors.New("COW engine is closing")
	errCOWRunsActive    = errors.New("COW engine still has active runs")
)

type Engine struct {
	runtime             wazerort.Runtime
	compiled            wazerort.CompiledModule
	config              runtimeconfig.RunConfig
	brokerFactory       BrokerFactory
	workspaceBinding    *workspaceBinding
	workspaceBindMu     sync.Mutex
	workspaceLease      *workspace.Lease
	workspaceRun        chan struct{}
	artifactSHA256      string
	preparedMu          sync.Mutex
	preparedInitMu      sync.Mutex
	preparedInitialized bool
	preparedInitErr     error
	preparedTrustedSHA  string
	prepared            *preparedInstance
	preparedNumpyInput  *PreparedNumpyInput
	preparedState       PreparedState
	cowMu               sync.Mutex
	cowRuntime          cowPreparedRuntime
	cowParentRuntime    cowPreparedRuntime
	cowActive           uint64
	cowClosing          bool
	semanticSessionMu   sync.Mutex
	semanticSessionRuns uint64
	semanticClosing     bool
	coldEvidence        coldEvidenceStore
	semanticLifecycle   semanticAnalysisLifecycleStore
	preparedRegions     *preparedregion.PreparedRegionTable
	valueSlots          *valueslot.Table
	splitPhaseEvidence  splitPhaseEvidenceStore
}

type splitPhaseEvidenceStore struct {
	mu       sync.Mutex
	snapshot capability.SplitPhaseSnapshot
}

func (store *splitPhaseEvidenceStore) set(snapshot capability.SplitPhaseSnapshot) {
	store.mu.Lock()
	store.snapshot = snapshot
	store.mu.Unlock()
}

func (store *splitPhaseEvidenceStore) get() capability.SplitPhaseSnapshot {
	store.mu.Lock()
	defer store.mu.Unlock()
	copy := store.snapshot
	copy.Events = append([]capability.SplitPhaseEvent(nil), store.snapshot.Events...)
	return copy
}

// SplitPhaseEvidence returns the last completed Run's physical-work ledger.
// Logical call receipts remain owned by Broker and are intentionally separate.
func (engine *Engine) SplitPhaseEvidence() capability.SplitPhaseSnapshot {
	if engine == nil {
		return capability.SplitPhaseSnapshot{}
	}
	return engine.splitPhaseEvidence.get()
}

func New(ctx context.Context, wasm []byte, config runtimeconfig.RunConfig) (*Engine, error) {
	return newEngine(ctx, wasm, config, nil, nil, nil, nil)
}

func NewWithBrokerFactory(ctx context.Context, wasm []byte, config runtimeconfig.RunConfig, factory BrokerFactory) (*Engine, error) {
	return newEngine(ctx, wasm, config, factory, nil, nil, nil)
}

func newEngine(ctx context.Context, wasm []byte, config runtimeconfig.RunConfig, brokerFactory BrokerFactory, binding *workspaceBinding, preparedRegions *preparedregion.PreparedRegionTable, valueSlots *valueslot.Table) (*Engine, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid run config: %w", err)
	}
	config = cloneFamilyRunConfig(config)
	if config.ColdIO != nil {
		policy := *config.ColdIO
		config.ColdIO = &policy
	}
	if len(wasm) < 8 {
		return nil, errors.New("guest module is too short")
	}
	artifactDigest := sha256.Sum256(wasm)
	artifactSHA256 := fmt.Sprintf("sha256:%x", artifactDigest[:])
	if profile := config.ExecutionProfile; profile != nil && profile.ArtifactSHA256() != "" && profile.ArtifactSHA256() != artifactSHA256 {
		return nil, runtimeconfig.ErrExecutionProfileArtifactMismatch
	}
	if config.DeterministicVerification != nil {
		if artifactSHA256 != config.DeterministicVerification.ArtifactSHA256() || binding != nil {
			return nil, runtimeconfig.ErrDeterministicVerificationAdmission
		}
	}
	runtimeConfig := wazerort.NewRuntimeConfig().WithCloseOnContextDone(true).WithMemoryLimitPages(config.MemoryLimitPages)
	wasmRuntime := wazerort.NewRuntimeWithConfig(ctx, runtimeConfig)
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, wasmRuntime); err != nil {
		_ = wasmRuntime.Close(ctx)
		return nil, fmt.Errorf("instantiate WASI imports: %w", err)
	}
	if err := instantiateCapabilityHost(ctx, wasmRuntime); err != nil {
		_ = wasmRuntime.Close(ctx)
		return nil, fmt.Errorf("instantiate capability imports: %w", err)
	}
	compiled, err := wasmRuntime.CompileModule(ctx, wasm)
	if err != nil {
		_ = wasmRuntime.Close(ctx)
		return nil, fmt.Errorf("compile guest: %w", err)
	}
	engine := &Engine{
		runtime: wasmRuntime, compiled: compiled, config: config, brokerFactory: brokerFactory, workspaceBinding: binding, preparedRegions: preparedRegions, valueSlots: valueSlots,
		artifactSHA256: artifactSHA256,
	}
	if binding != nil {
		engine.workspaceRun = make(chan struct{}, 1)
	}
	engine.preparedState = PreparedState{SchemaVersion: "pysolate.prepared-runtime.v1", Selected: config.Mechanisms.PreparedRuntime}
	return engine, nil
}

func newPreparedNumpyCopyEngine(ctx context.Context, wasm []byte, config runtimeconfig.RunConfig, brokerFactory BrokerFactory, binding *workspaceBinding, input PreparedNumpyInput) (*Engine, error) {
	if input.validateForConfig(config) != nil || len(input.body) == 0 || len(input.descriptorJSON) == 0 {
		return nil, ErrPreparedNumpyInput
	}
	engine, err := newEngine(ctx, wasm, config, brokerFactory, binding, nil, nil)
	if err != nil {
		return nil, err
	}
	copyInput := input
	copyInput.body = append([]byte(nil), input.body...)
	copyInput.descriptorJSON = append([]byte(nil), input.descriptorJSON...)
	copyInput.descriptor.Shape = append([]uint64(nil), input.descriptor.Shape...)
	engine.preparedNumpyInput = &copyInput
	return engine, nil
}

func (engine *Engine) Properties() enginecontract.Properties {
	properties := enginecontract.Properties{
		Backend:                   "wazero",
		WorkspaceMounted:          engine.workspaceBinding != nil,
		CapabilityBrokerAvailable: engine.brokerFactory != nil,
	}
	properties.ExecutionProfileBindingSHA256, _ = runtimeconfig.ExecutionProfileBindingSHA256(engine.config)
	if engine.config.DeterministicVerification != nil {
		properties.DeterministicProfileSHA256 = engine.config.DeterministicVerification.Identity()
	}
	if engine.config.ExecutionProfile != nil {
		profile := engine.config.ExecutionProfile
		properties.ExecutionProfileID = profile.ID()
		properties.AllowedImports = profile.AllowedImports()
		properties.AvailableImports = profile.AvailableImports()
		properties.QualifiedImports = profile.QualifiedImports()
		properties.ArtifactSHA256 = engine.artifactSHA256
		properties.ManifestSHA256 = profile.ManifestSHA256()
	}
	return properties
}

func (engine *Engine) ensureWorkspace() error {
	if engine.workspaceBinding == nil {
		return nil
	}
	engine.workspaceBindMu.Lock()
	defer engine.workspaceBindMu.Unlock()
	if engine.workspaceLease != nil {
		return nil
	}
	lease, err := engine.workspaceBinding.manager.Acquire(engine.workspaceBinding.ref, engine.workspaceBinding.owner)
	if err != nil {
		return err
	}
	engine.workspaceLease = lease
	return nil
}

func (engine *Engine) cowIsClosing() bool {
	engine.cowMu.Lock()
	defer engine.cowMu.Unlock()
	return engine.cowClosing
}

func (engine *Engine) publishCOWRuntime(runtime cowPreparedRuntime) error {
	engine.cowMu.Lock()
	defer engine.cowMu.Unlock()
	if engine.cowClosing {
		return errCOWEngineClosing
	}
	engine.cowRuntime = runtime
	return nil
}

func (engine *Engine) ensurePrepared(ctx context.Context) error {
	_, err := engine.ensurePreparedWithResult(ctx)
	return err
}

// PrepareSemanticRuntime provisions a workspace/Broker-free analyzer's configured
// single-use prepared slot or Linux COW baseline without serving it. The Guest
// still has the configured WASI clock/random substrate; this is not an
// authority-free interpreter snapshot.
func (engine *Engine) PrepareSemanticRuntime(ctx context.Context) error {
	if engine == nil || ctx == nil || !engine.config.Mechanisms.PreparedRuntime {
		return runtimeconfig.ErrMechanismDisabled
	}
	properties := engine.Properties()
	if properties.WorkspaceMounted || properties.CapabilityBrokerAvailable {
		return ErrSemanticAnalysisSessionAuthority
	}
	return engine.ensurePrepared(ctx)
}

// PrepareNumpyCOWShard provisions the one Linux private-COW NumPy package
// baseline. Engines with workspace or Broker authority are rejected.
func (engine *Engine) PrepareNumpyCOWShard(ctx context.Context) error {
	if engine == nil || ctx == nil || !engine.config.Mechanisms.PreparedRuntime || !engine.config.Mechanisms.MemoryCOW {
		return runtimeconfig.ErrMechanismDisabled
	}
	if err := engine.validateNumpyCOWProfile(); err != nil {
		return err
	}
	source := trustedCOWPackageSource
	identity, err := trustedCOWPrepareIdentity(source)
	if err != nil {
		return err
	}
	properties := engine.Properties()
	if properties.WorkspaceMounted || properties.CapabilityBrokerAvailable {
		return ErrSemanticAnalysisSessionAuthority
	}
	_, err = engine.ensurePreparedWithResultAndTrustedSource(ctx, source, identity)
	return err
}

// DeriveNumpyI64COWDataset builds the research-only fixed
// <i8 (1024,1024) active image from the prepared NumPy package baseline. The
// immutable package parent is retained so another derivation does not inherit
// the prior dataset.
func (engine *Engine) DeriveNumpyI64COWDataset(ctx context.Context, body []byte) error {
	if engine == nil || ctx == nil || !engine.config.Mechanisms.PreparedRuntime || !engine.config.Mechanisms.MemoryCOW {
		return runtimeconfig.ErrMechanismDisabled
	}
	if err := engine.validateNumpyCOWProfile(); err != nil {
		return err
	}
	source, identity, err := trustedCOWDerivedSource(body)
	if err != nil {
		return err
	}
	properties := engine.Properties()
	if properties.WorkspaceMounted || properties.CapabilityBrokerAvailable {
		return ErrSemanticAnalysisSessionAuthority
	}
	return engine.replaceDerivedCOWRuntime(func(parent cowPreparedRuntime) (cowPreparedRuntime, error) {
		deriver, ok := parent.(derivableCOWPreparedRuntime)
		if !ok {
			return nil, runtimeconfig.ErrMechanismDisabled
		}
		return deriver.derive(ctx, engine, source, identity)
	})
}

// PrepareNumpyCOWInput seals one descriptor-bound ndarray into a Linux private-
// COW image. The package parent remains immutable and the body is consumed only
// through the bounded binary preparation ABI.
func (engine *Engine) PrepareNumpyCOWInput(ctx context.Context, input PreparedNumpyInput) error {
	if engine == nil || ctx == nil || !engine.config.Mechanisms.PreparedRuntime || !engine.config.Mechanisms.MemoryCOW {
		return runtimeconfig.ErrMechanismDisabled
	}
	if err := engine.validateNumpyCOWProfile(); err != nil {
		return err
	}
	if err := input.validateForConfig(engine.config); err != nil {
		return err
	}
	properties := engine.Properties()
	if properties.WorkspaceMounted || properties.CapabilityBrokerAvailable {
		return ErrSemanticAnalysisSessionAuthority
	}
	if err := engine.PrepareNumpyCOWShard(ctx); err != nil {
		return err
	}
	return engine.replaceDerivedCOWRuntime(func(parent cowPreparedRuntime) (cowPreparedRuntime, error) {
		deriver, ok := parent.(numpyDerivableCOWPreparedRuntime)
		if !ok {
			return nil, runtimeconfig.ErrMechanismDisabled
		}
		return deriver.deriveNumpy(ctx, engine, input, input.identity)
	})
}

func (engine *Engine) replaceDerivedCOWRuntime(build func(cowPreparedRuntime) (cowPreparedRuntime, error)) error {
	engine.preparedInitMu.Lock()
	defer engine.preparedInitMu.Unlock()
	if !engine.preparedInitialized || engine.preparedInitErr != nil || engine.preparedTrustedSHA == "" {
		return ErrTrustedCOWPrepareBinding
	}
	engine.cowMu.Lock()
	defer engine.cowMu.Unlock()
	if engine.cowClosing {
		return errCOWEngineClosing
	}
	if engine.cowActive != 0 {
		return errCOWRunsActive
	}
	parent := engine.cowParentRuntime
	if parent == nil {
		parent = engine.cowRuntime
	}
	derived, err := build(parent)
	if err != nil {
		return err
	}
	oldActive := engine.cowRuntime
	if engine.cowParentRuntime == nil {
		engine.cowParentRuntime = oldActive
	} else if oldActive != nil {
		if err := oldActive.close(); err != nil {
			_ = derived.close()
			return err
		}
	}
	engine.cowRuntime = derived
	return nil
}

func (engine *Engine) validateNumpyCOWProfile() error {
	profile := engine.config.ExecutionProfile
	if profile == nil || profile.Validate() != nil || profile.ID() != "numpy-core" || profile.ArtifactSHA256() == "" || profile.ManifestSHA256() == "" {
		return ErrTrustedCOWPrepareBinding
	}
	for _, qualified := range profile.QualifiedImports() {
		if qualified == "numpy" {
			return nil
		}
	}
	return ErrTrustedCOWPrepareBinding
}

func (engine *Engine) acquireSemanticSession() error {
	engine.semanticSessionMu.Lock()
	defer engine.semanticSessionMu.Unlock()
	if engine.semanticClosing {
		return ErrSemanticAnalysisEngineClosing
	}
	engine.semanticSessionRuns++
	return nil
}

func (engine *Engine) releaseSemanticSession() {
	engine.semanticSessionMu.Lock()
	if engine.semanticSessionRuns > 0 {
		engine.semanticSessionRuns--
	}
	engine.semanticSessionMu.Unlock()
}

func (engine *Engine) beginSemanticClose() error {
	engine.semanticSessionMu.Lock()
	defer engine.semanticSessionMu.Unlock()
	engine.semanticClosing = true
	if engine.semanticSessionRuns != 0 {
		return ErrSemanticAnalysisSessionsActive
	}
	return nil
}

func (engine *Engine) ensurePreparedWithResult(ctx context.Context) (bool, error) {
	return engine.ensurePreparedWithResultAndTrustedSource(ctx, "", "")
}

func (engine *Engine) ensurePreparedWithResultAndTrustedSource(ctx context.Context, trustedSource, trustedIdentity string) (bool, error) {
	engine.preparedInitMu.Lock()
	defer engine.preparedInitMu.Unlock()
	if engine.cowIsClosing() {
		return false, errCOWEngineClosing
	}
	if engine.preparedInitialized {
		if trustedIdentity != "" && engine.preparedTrustedSHA != trustedIdentity {
			return false, ErrTrustedCOWPrepareBinding
		}
		return false, engine.preparedInitErr
	}
	engine.preparedInitialized = true
	engine.preparedTrustedSHA = trustedIdentity
	if engine.config.Mechanisms.MemoryCOW {
		started := time.Now()
		var cowRuntime cowPreparedRuntime
		var err error
		if trustedSource == "" {
			cowRuntime, err = newCOWPreparedRuntime(ctx, engine)
		} else {
			cowRuntime, err = newCOWPreparedRuntimeWithTrustedSource(ctx, engine, trustedSource, trustedIdentity)
		}
		prepareMS := float64(time.Since(started)) / float64(time.Millisecond)
		engine.preparedMu.Lock()
		engine.preparedState.PrepareMS = prepareMS
		engine.preparedMu.Unlock()
		if err != nil {
			engine.preparedInitErr = fmt.Errorf("prepare COW baseline: %w: %w", err, runtimeconfig.ErrMechanismDisabled)
			return true, engine.preparedInitErr
		}
		if err := engine.publishCOWRuntime(cowRuntime); err != nil {
			_ = cowRuntime.close()
			engine.preparedInitErr = err
			return true, err
		}
		engine.preparedMu.Lock()
		engine.preparedState.Ready = true
		engine.preparedMu.Unlock()
	} else if engine.config.Mechanisms.PreparedRuntime {
		started := time.Now()
		prepared, err := engine.newPrepared(ctx)
		prepareMS := float64(time.Since(started)) / float64(time.Millisecond)
		engine.preparedMu.Lock()
		engine.preparedState.PrepareMS = prepareMS
		engine.preparedMu.Unlock()
		if err != nil {
			engine.preparedInitErr = fmt.Errorf("prepare single-use guest: %w", err)
			return true, engine.preparedInitErr
		}
		engine.preparedMu.Lock()
		engine.prepared = prepared
		engine.preparedState.Ready = true
		engine.preparedMu.Unlock()
	}
	return true, nil
}

func (engine *Engine) acquireCOWRuntime() (cowPreparedRuntime, func(), bool, error) {
	engine.cowMu.Lock()
	defer engine.cowMu.Unlock()
	if engine.cowClosing {
		return nil, nil, false, errCOWEngineClosing
	}
	if engine.cowRuntime == nil {
		return nil, nil, false, nil
	}
	engine.cowActive++
	var once sync.Once
	release := func() {
		once.Do(func() {
			engine.cowMu.Lock()
			engine.cowActive--
			engine.cowMu.Unlock()
		})
	}
	return engine.cowRuntime, release, true, nil
}

func (engine *Engine) closeCOWRuntime() error {
	engine.cowMu.Lock()
	defer engine.cowMu.Unlock()
	engine.cowClosing = true
	if engine.cowActive != 0 {
		return errCOWRunsActive
	}
	var activeErr, parentErr error
	if engine.cowRuntime != nil {
		activeErr = engine.cowRuntime.close()
		if activeErr == nil {
			engine.cowRuntime = nil
		}
	}
	if engine.cowParentRuntime != nil {
		parentErr = engine.cowParentRuntime.close()
		if parentErr == nil {
			engine.cowParentRuntime = nil
		}
	}
	return errors.Join(activeErr, parentErr)
}

func (engine *Engine) Close(ctx context.Context) error {
	if engine == nil || engine.runtime == nil {
		return nil
	}
	if err := engine.beginSemanticClose(); err != nil {
		return err
	}
	engine.preparedInitMu.Lock()
	defer engine.preparedInitMu.Unlock()
	if err := engine.closeCOWRuntime(); err != nil {
		return err
	}
	preparedErr := engine.closePrepared()
	if engine.preparedNumpyInput != nil {
		engine.preparedNumpyInput.body = nil
		engine.preparedNumpyInput.descriptorJSON = nil
		engine.preparedNumpyInput = nil
	}
	runtimeErr := engine.runtime.Close(ctx)
	var workspaceErr error
	if engine.workspaceLease != nil {
		workspaceErr = engine.workspaceLease.Release()
	}
	return errors.Join(preparedErr, runtimeErr, workspaceErr, engine.preparedRegions.Close())
}

func (engine *Engine) baseModuleConfig(stderr io.Writer, stdout io.Writer) wazerort.ModuleConfig {
	config := wazerort.NewModuleConfig().WithName("").WithStderr(stderr).WithStdout(stdout)
	if profile := engine.config.DeterministicVerification; profile != nil {
		clock := newDeterministicClock(profile.WalltimeUnixNano(), profile.MonotonicStartNano(), profile.ClockStepNano())
		resolution := wazerosys.ClockResolution(profile.ClockStepNano())
		config = config.WithRandSource(newDeterministicReader(profile.RandomSeed())).
			WithWalltime(clock.walltime, resolution).
			WithNanotime(clock.nanotime, resolution).
			WithNanosleep(clock.nanosleep)
	} else {
		config = config.WithRandSource(cryptorand.Reader).WithSysWalltime().WithSysNanotime().WithSysNanosleep()
	}
	return config
}

func (engine *Engine) moduleConfig(stderr io.Writer, stdout io.Writer) (wazerort.ModuleConfig, *workspace.Temporary, error) {
	config := engine.baseModuleConfig(stderr, stdout)
	if engine.workspaceLease == nil {
		return config, nil, nil
	}
	temporary, err := engine.workspaceLease.NewTemporary()
	if err != nil {
		return nil, nil, err
	}
	base, ok := wazerort.NewFSConfig().(experimentalsysfs.FSConfig)
	if !ok {
		_ = temporary.Close()
		return nil, nil, errors.New("wazero does not support rooted workspace mounts")
	}
	withWorkspace, ok := base.WithSysFSMount(engine.workspaceLease.FS(), "workspace").(experimentalsysfs.FSConfig)
	if !ok {
		_ = temporary.Close()
		return nil, nil, errors.New("wazero does not support multiple rooted mounts")
	}
	return config.WithFSConfig(withWorkspace.WithSysFSMount(temporary.FS(), "tmp")), temporary, nil
}

func (engine *Engine) newPrepared(ctx context.Context) (*preparedInstance, error) {
	prepareContext, cancel := context.WithTimeout(ctx, engine.config.Timeout)
	defer cancel()
	stderr := &boundedDiagnostic{}
	stdout := &forbiddenStdout{}
	moduleConfig, temporary, err := engine.moduleConfig(stderr, stdout)
	if err != nil {
		return nil, err
	}
	module, err := engine.runtime.InstantiateModule(prepareContext, engine.compiled, moduleConfig)
	if err != nil {
		if temporary != nil {
			_ = temporary.Close()
		}
		return nil, err
	}
	failed := true
	defer func() {
		if failed {
			_ = module.Close(context.Background())
			if temporary != nil {
				_ = temporary.Close()
			}
		}
	}()
	if err := callNoArgs(prepareContext, module, "_initialize"); err != nil {
		return nil, withGuestDiagnostic(err, stderr.String())
	}
	if err := callStatusWithBytes(prepareContext, module, "runtime_init", []byte("{}")); err != nil {
		return nil, withGuestDiagnostic(err, stderr.String())
	}
	stderr.Reset()
	failed = false
	return &preparedInstance{module: module, stderr: stderr, stdout: stdout, temporary: temporary}, nil
}

func (engine *Engine) takePrepared() *preparedInstance {
	engine.preparedMu.Lock()
	defer engine.preparedMu.Unlock()
	prepared := engine.prepared
	engine.prepared = nil
	if prepared != nil {
		engine.preparedState.Ready = false
		engine.preparedState.PreparedRuns++
	} else if engine.preparedState.Selected {
		engine.preparedState.FreshFallbackRuns++
	}
	return prepared
}

func (engine *Engine) PreparedState() PreparedState {
	if engine == nil {
		return PreparedState{}
	}
	engine.preparedMu.Lock()
	defer engine.preparedMu.Unlock()
	return engine.preparedState
}

func (engine *Engine) COWProbe() COWProbe {
	probe := COWProbe{
		SchemaVersion: "pysolate.cow-probe.v1", Platform: goruntime.GOOS,
		PreparedCompatible: engine != nil, Fallback: true,
	}
	if engine == nil || engine.compiled == nil {
		probe.Blockers = append(probe.Blockers, "compiled_module_unavailable")
		sort.Strings(probe.Blockers)
		return probe
	}
	imported := engine.compiled.ImportedMemories()
	exported := engine.compiled.ExportedMemories()
	definitions := make(map[uint32]api.MemoryDefinition, len(imported)+len(exported))
	for _, definition := range imported {
		definitions[definition.Index()] = definition
	}
	for _, definition := range exported {
		definitions[definition.Index()] = definition
	}
	probe.MemoryCount = len(definitions)
	probe.ImportedMemoryCount = len(imported)
	memory, exportedMemory := exported["memory"]
	if exportedMemory {
		maximum, declared := memory.Max()
		probe.MemoryMaximumDeclared = declared
		probe.MemoryFixed = declared && maximum == memory.Min()
	}
	probe.MemoryCOWCandidate = goruntime.GOOS == "linux" && exportedMemory && probe.MemoryCount == 1 && len(imported) == 0 && probe.MemoryMaximumDeclared
	if goruntime.GOOS != "linux" {
		probe.Blockers = append(probe.Blockers, "linux_memfd_private_mapping_unavailable")
	}
	if !probe.MemoryCOWCandidate {
		probe.Blockers = append(probe.Blockers, "linear_memory_not_bounded_private_candidate")
	}
	engine.cowMu.Lock()
	cowSelected := engine.cowRuntime != nil
	engine.cowMu.Unlock()
	if cowSelected {
		probe.COWSelected = true
		probe.Fallback = false
		probe.Blockers = nil
	}
	sort.Strings(probe.Blockers)
	return probe
}

func (engine *Engine) closePrepared() error {
	engine.preparedMu.Lock()
	prepared := engine.prepared
	engine.prepared = nil
	engine.preparedState.Ready = false
	engine.preparedMu.Unlock()
	if prepared == nil {
		return nil
	}
	moduleErr := prepared.module.Close(context.Background())
	var temporaryErr error
	if prepared.temporary != nil {
		temporaryErr = prepared.temporary.Close()
	}
	return errors.Join(moduleErr, temporaryErr)
}

func (engine *Engine) AnalyzeSemantic(ctx context.Context, request []byte) (payload []byte, analysisErr error) {
	lifecycle := SemanticAnalysisLifecycleEvidence{Invocations: 1}
	defer func() {
		if analysisErr == nil {
			lifecycle.Successes = 1
		} else {
			lifecycle.Failures = 1
		}
		if engine != nil {
			engine.semanticLifecycle.add(lifecycle)
		}
	}()
	if engine == nil || !engine.config.Mechanisms.SemanticAnalysis {
		return nil, runtimeconfig.ErrMechanismDisabled
	}
	analysisContext, cancel := context.WithTimeout(ctx, engine.config.Timeout)
	defer cancel()
	stderr := &boundedDiagnostic{}
	stdout := &forbiddenStdout{}
	started := time.Now()
	lifecycle.ModuleInstantiations = 1
	module, err := engine.runtime.InstantiateModule(analysisContext, engine.compiled, engine.baseModuleConfig(stderr, stdout))
	lifecycle.InstantiateNanos = uint64(time.Since(started))
	if err != nil {
		return nil, fmt.Errorf("instantiate semantic analyzer Guest: %w", err)
	}
	defer func() {
		started := time.Now()
		analysisErr = errors.Join(analysisErr, module.Close(context.Background()))
		lifecycle.CloseNanos += uint64(time.Since(started))
	}()
	started = time.Now()
	lifecycle.InitializeCalls = 1
	if err := callNoArgs(analysisContext, module, "_initialize"); err != nil {
		lifecycle.InitializeNanos = uint64(time.Since(started))
		return nil, withGuestDiagnostic(err, stderr.String())
	}
	lifecycle.InitializeNanos = uint64(time.Since(started))
	started = time.Now()
	lifecycle.RuntimeInitCalls = 1
	if err := callStatusWithBytes(analysisContext, module, "runtime_init", []byte("{}")); err != nil {
		lifecycle.RuntimeInitNanos = uint64(time.Since(started))
		return nil, withGuestDiagnostic(err, stderr.String())
	}
	lifecycle.RuntimeInitNanos = uint64(time.Since(started))
	started = time.Now()
	payload, err = callGuestResponse(analysisContext, module, "runtime_analyze_source", request, engine.config.MaxResponseBytes)
	lifecycle.AnalyzeNanos = uint64(time.Since(started))
	if err != nil {
		return nil, withGuestDiagnostic(err, stderr.String())
	}
	if stdout.Used() {
		return payload, ErrGuestStdoutBypass
	}
	return payload, nil
}

func (engine *Engine) Run(ctx context.Context, request []byte, trustedPrepare string) ([]byte, error) {
	prepares := make(chan string, 1)
	if trustedPrepare != "" {
		prepares <- trustedPrepare
	}
	close(prepares)
	return engine.runWithPrepares(ctx, request, prepares, false, nil)
}

// RunPreparedRegionDerived commits one sealed derived selection before the
// fresh final Guest executes. It is deliberately outside the generic Runner
// interface and permits neither authority-bearing execution nor runtime
// fallback to the original program.
func (engine *Engine) RunPreparedRegionDerived(ctx context.Context, request []byte, trustedPrepare string, selection preparedregion.PreparedRegionExecutionSelection, decision preparedregion.PreparedRegionDecision, capsule preparedregion.PreparedRegionCapsule, patch preparedregion.PreparedRegionPatch) ([]byte, error) {
	if !engine.config.Mechanisms.SemanticAnalysis {
		return nil, runtimeconfig.ErrMechanismDisabled
	}
	properties := engine.Properties()
	if properties.CapabilityBrokerAvailable || properties.WorkspaceMounted {
		return nil, ErrPreparedRegionScratchAuthority
	}
	if selection.Validate(decision, capsule, patch) != nil || engine.preparedRegions.ValidateReady(decision, capsule) != nil {
		return nil, preparedregion.ErrInvalidPreparedRegion
	}
	runRequest, err := runtimeconfig.DecodeRunRequest(request)
	if err != nil {
		return nil, err
	}
	sourceDigest := sha256.Sum256([]byte(runRequest.Code))
	if selection.FinalSourceSHA256 != fmt.Sprintf("sha256:%x", sourceDigest[:]) {
		return nil, preparedregion.ErrInvalidPreparedRegion
	}
	profileSHA256, err := runtimeconfig.ExecutionProfileBindingSHA256(engine.config)
	if err != nil || decision.ExecutionProfileSHA256 != profileSHA256 {
		return nil, preparedregion.ErrInvalidPreparedRegion
	}
	decisionRaw, sealedDecision, err := preparedregion.SealPreparedRegionDecision(decision.PreparedRegionBinding)
	if err != nil || sealedDecision != decision {
		return nil, preparedregion.ErrInvalidPreparedRegion
	}
	patchRaw, sealedPatch, err := preparedregion.SealPreparedRegionPatch(patch.PreparedRegionPatchBinding)
	if err != nil || sealedPatch != patch {
		return nil, preparedregion.ErrInvalidPreparedRegion
	}
	selectionRaw, sealedSelection, err := preparedregion.SealPreparedRegionExecutionSelection(decision, capsule, patch)
	if err != nil || sealedSelection != selection {
		return nil, preparedregion.ErrInvalidPreparedRegion
	}
	selectionRequest, err := json.Marshal(map[string]string{
		"decision": string(decisionRaw), "patch": string(patchRaw), "selection": string(selectionRaw),
	})
	if err != nil {
		return nil, err
	}
	prepares := make(chan string, 1)
	if trustedPrepare != "" {
		prepares <- trustedPrepare
	}
	close(prepares)
	return engine.runWithPrepares(ctx, request, prepares, false, &derivedSelection{export: "runtime_select_prepared_region_execution", payload: selectionRequest})
}

// RunSourcePatchDerived executes one exact-Guest-produced source patch against
// the unchanged original RunRequest. The first plugin seam is intentionally
// authority-free; effect-owning passes require their own typed stage adapter.
func (engine *Engine) RunSourcePatchDerived(ctx context.Context, request []byte, patch sourcepatch.Patch, registration passregistration.Registration) ([]byte, error) {
	if !engine.config.Mechanisms.SemanticAnalysis {
		return nil, runtimeconfig.ErrMechanismDisabled
	}
	properties := engine.Properties()
	if properties.CapabilityBrokerAvailable || properties.WorkspaceMounted {
		return nil, ErrPreparedRegionScratchAuthority
	}
	runRequest, err := runtimeconfig.DecodeRunRequest(request)
	if err != nil {
		return nil, err
	}
	if registration.Stage() != passregistration.StageWholeProgramPatch || !patch.Applied() || patch.Validate(runRequest.Code, registration) != nil {
		return nil, sourcepatch.ErrInvalidPatch
	}
	patchRaw, err := json.Marshal(patch)
	if err != nil {
		return nil, err
	}
	prepares := make(chan string)
	close(prepares)
	return engine.runWithPrepares(ctx, request, prepares, false, &derivedSelection{export: "runtime_select_source_pass_execution", payload: append(patchRaw, '\n')})
}

// RunHostScheduledSourcePatchDerived executes the single v1 split-phase read
// adapter. Unlike the authority-free source-patch seam, this path requires a
// Broker and the explicit default-off split-phase mechanism.
func (engine *Engine) RunHostScheduledSourcePatchDerived(ctx context.Context, request []byte, patch sourcepatch.Patch, registration passregistration.Registration) ([]byte, error) {
	if !engine.config.Mechanisms.SplitPhaseCalls || engine.brokerFactory == nil || engine.config.ProgramSurface != runtimeconfig.ProgramSurfaceDirect {
		return nil, runtimeconfig.ErrMechanismDisabled
	}
	runRequest, err := runtimeconfig.DecodeRunRequest(request)
	if err != nil {
		return nil, err
	}
	if registration.Name() != sourcepatch.SplitPhaseSourcesReadName || registration.Stage() != passregistration.StageWholeProgramPatch ||
		!patch.Applied() || patch.Validate(runRequest.Code, registration) != nil {
		return nil, sourcepatch.ErrInvalidPatch
	}
	patchRaw, err := json.Marshal(patch)
	if err != nil {
		return nil, err
	}
	prepares := make(chan string)
	close(prepares)
	return engine.runWithPrepares(ctx, request, prepares, false, &derivedSelection{export: "runtime_select_source_pass_execution", payload: append(patchRaw, '\n')})
}

// RunValueSlotSourcePatchDerived executes the single v1 data-local reduction adapter.
// The pass describes one semantic scalar slot; the Host-selected table owns materialization.
func (engine *Engine) RunValueSlotSourcePatchDerived(ctx context.Context, request []byte, patch sourcepatch.Patch, registration passregistration.Registration) ([]byte, error) {
	if !engine.config.Mechanisms.ValueSlots || engine.valueSlots == nil || engine.config.ProgramSurface != runtimeconfig.ProgramSurfaceDirect {
		return nil, runtimeconfig.ErrMechanismDisabled
	}
	spec, strategy, backingIdentity, err := engine.valueSlots.Describe("slot-numpy-sum-v1")
	if err != nil || spec.ID != "slot-numpy-sum-v1" || spec.SourceOccurrence != "line-4:result" ||
		spec.ProducerIdentity != "numpy-int64-sum-v1" || spec.InputIdentity == "" || spec.PrivacyPartition == "" ||
		spec.Kind != valueslot.KindJSONScalar || spec.MaxBytes > 32 || spec.ClaimPolicy != valueslot.ClaimSingleUse ||
		spec.MaxClaims != 1 || strategy != valueslot.StrategyInlineJSON || backingIdentity == "" {
		return nil, valueslot.ErrInvalidEntry
	}
	runRequest, err := runtimeconfig.DecodeRunRequest(request)
	if err != nil {
		return nil, err
	}
	if registration.Name() != sourcepatch.DataLocalNumpySumName || registration.Stage() != passregistration.StageWholeProgramPatch ||
		!patch.Applied() || patch.Validate(runRequest.Code, registration) != nil {
		return nil, sourcepatch.ErrInvalidPatch
	}
	patchRaw, err := json.Marshal(patch)
	if err != nil {
		return nil, err
	}
	prepares := make(chan string)
	close(prepares)
	return engine.runWithPrepares(ctx, request, prepares, false, &derivedSelection{
		export: "runtime_select_source_pass_execution", payload: append(patchRaw, '\n'),
		sourceValidationExport: "runtime_validate_source_for_patch",
	})
}

// RunStream keeps one fresh Guest alive while Host-trusted preparation chunks
// arrive. It is an internal streaming seam; Agent source still enters only
// through the final validated request.
func (engine *Engine) RunStream(ctx context.Context, request []byte, prepares <-chan string) ([]byte, error) {
	if !engine.config.Mechanisms.Streaming {
		return nil, runtimeconfig.ErrMechanismDisabled
	}
	return engine.runWithPrepares(ctx, request, prepares, true, nil)
}

type derivedSelection struct {
	export                 string
	payload                []byte
	sourceValidationExport string
}

func (engine *Engine) runWithPrepares(ctx context.Context, request []byte, prepares <-chan string, streaming bool, selection *derivedSelection) (payload []byte, runErr error) {
	if len(request) == 0 || uint64(len(request)) > uint64(engine.config.MaxRequestBytes) {
		return nil, errors.New("request exceeds configured bounds")
	}
	runRequest, err := runtimeconfig.DecodeRunRequest(request)
	if err != nil {
		return nil, err
	}
	if err := runtimeconfig.AdmitRunRequirements(runRequest); err != nil {
		return nil, err
	}
	if err := runtimeconfig.EvaluateRunCompatibility(runRequest, engine.config.ExecutionProfile); err != nil {
		return nil, err
	}
	if engine.config.DeterministicVerification != nil {
		if err := runtimeconfig.AdmitDeterministicVerificationExecution(runRequest, engine.workspaceLease != nil); err != nil {
			return nil, err
		}
	}
	var executionRef *runtimeconfig.ExecutionRef
	invocationRef, hasInvocationRef := enginecontract.InvocationRefFromContext(ctx)
	if hasInvocationRef {
		digest := sha256.Sum256([]byte(runRequest.Code))
		ref := runtimeconfig.ExecutionRef{InvocationRef: invocationRef, ExecutedCodeSHA256: fmt.Sprintf("sha256:%x", digest[:])}
		executionRef = &ref
	}
	observationSession, hasObservation := enginecontract.ObservationSessionFromContext(ctx)
	if hasObservation && observationSession.Mode() != observe.Off {
		if !hasInvocationRef || observationSession.ExecutionID() != invocationRef.ExecutionID {
			return nil, ErrObservationIdentityMismatch
		}
	}
	runContext, cancel := context.WithTimeout(ctx, engine.config.Timeout)
	defer cancel()
	if engine.preparedRegions != nil {
		runContext = context.WithValue(runContext, preparedRegionContextKey{}, engine.preparedRegions)
		defer func() { runErr = errors.Join(runErr, engine.preparedRegions.Close()) }()
	}
	if engine.valueSlots != nil {
		runContext = context.WithValue(runContext, valueSlotContextKey{}, engine.valueSlots)
		defer func() { runErr = errors.Join(runErr, engine.valueSlots.Close()) }()
	}
	if engine.workspaceRun != nil {
		select {
		case engine.workspaceRun <- struct{}{}:
		case <-runContext.Done():
			return nil, runContext.Err()
		}
		defer func() { <-engine.workspaceRun }()
	}
	if err := engine.ensureWorkspace(); err != nil {
		return nil, err
	}
	if err := engine.verifyDataLocalValueInput(); err != nil {
		return nil, err
	}
	if err := engine.ensurePrepared(runContext); err != nil {
		return nil, err
	}
	var workspaceInitial *workspace.Snapshot
	if hasObservation && observationSession.Mode() != observe.Off && engine.workspaceLease != nil {
		snapshot, snapshotErr := engine.workspaceLease.Snapshot()
		if snapshotErr != nil {
			return nil, snapshotErr
		}
		workspaceInitial = &snapshot
	}
	observation := observationLifecycle{session: observationSession}
	var broker *capability.Broker
	var splitPhaseTable *capability.SplitPhaseTable
	capabilityCallsObserved := false
	if hasObservation && observationSession.Mode() != observe.Off {
		profileSHA256, profileErr := runtimeconfig.ExecutionProfileBindingSHA256(engine.config)
		if profileErr != nil {
			return nil, profileErr
		}
		start := observe.ExecutionStartedPayload{
			ArtifactSHA256: engine.artifactSHA256, ExecutedCodeSHA256: executionRef.ExecutedCodeSHA256,
			ExecutionProfileSHA256: profileSHA256,
		}
		if engine.config.DeterministicVerification != nil {
			start.DeterministicProfileSHA256 = engine.config.DeterministicVerification.Identity()
		}
		if err := observation.start(runContext, start); err != nil {
			return nil, err
		}
		defer func() {
			if runErr != nil && !observation.terminal {
				if broker != nil && !capabilityCallsObserved {
					receiptErr := observation.capabilityCalls(context.Background(), broker.Receipts())
					runErr = errors.Join(runErr, receiptErr)
					capabilityCallsObserved = receiptErr == nil
				}
				terminalErr := observation.fail(context.Background(), "runtime_error")
				runErr = errors.Join(runErr, terminalErr)
			}
		}()
	}
	var prepared *preparedInstance
	cowRuntime, releaseCOW, cowSelected, acquireErr := engine.acquireCOWRuntime()
	if acquireErr != nil {
		return nil, acquireErr
	}
	if cowSelected {
		prepared, _, err = cowRuntime.prepare(runContext, engine)
		if err != nil {
			releaseCOW()
			return nil, fmt.Errorf("prepare single-use COW slot: %w", err)
		}
		defer releaseCOW()
		engine.preparedMu.Lock()
		if prepared != nil {
			engine.preparedState.PreparedRuns++
		}
		engine.preparedMu.Unlock()
	} else {
		prepared = engine.takePrepared()
	}
	stderr := &boundedDiagnostic{}
	stdout := &forbiddenStdout{}
	var temporary *workspace.Temporary
	var module api.Module
	initialized := false
	if prepared != nil {
		stderr = prepared.stderr
		stdout = prepared.stdout
		temporary = prepared.temporary
		module = prepared.module
		initialized = true
	} else {
		moduleConfig, moduleTemporary, moduleErr := engine.moduleConfig(stderr, stdout)
		if moduleErr != nil {
			return nil, moduleErr
		}
		temporary = moduleTemporary
		module, err = engine.runtime.InstantiateModule(runContext, engine.compiled, moduleConfig)
		if err != nil {
			if temporary != nil {
				_ = temporary.Close()
			}
			return nil, fmt.Errorf("instantiate guest: %w", err)
		}
	}
	if temporary != nil {
		defer func() { runErr = errors.Join(runErr, temporary.Close()) }()
	}
	moduleClosed := false
	defer func() {
		if !moduleClosed {
			runErr = errors.Join(runErr, module.Close(context.Background()))
		}
	}()
	if prepared != nil && prepared.cold != nil {
		runContext = withColdIOContinuation(runContext, prepared.cold)
		defer func() { engine.coldEvidence.set(prepared.cold.finish()) }()
	}
	if !initialized {
		if err := callNoArgs(runContext, module, "_initialize"); err != nil {
			return nil, withGuestDiagnostic(err, stderr.String())
		}
		if err := callStatusWithBytes(runContext, module, "runtime_init", []byte("{}")); err != nil {
			return nil, withGuestDiagnostic(err, stderr.String())
		}
	}
	if engine.preparedNumpyInput != nil {
		if err := callPreparedNumpyInput(runContext, module, *engine.preparedNumpyInput); err != nil {
			return nil, withGuestDiagnostic(err, stderr.String())
		}
	}
	validationExport := "runtime_validate_source"
	if selection != nil && selection.sourceValidationExport != "" {
		validationExport = selection.sourceValidationExport
	}
	if err := callSourceValidation(runContext, module, validationExport, request); err != nil {
		return nil, withGuestDiagnostic(err, stderr.String())
	}
	if selection != nil {
		if err := callStatusWithBytes(runContext, module, selection.export, selection.payload); err != nil {
			return nil, withGuestDiagnostic(err, stderr.String())
		}
	}
	brokerFinalized := false
	if engine.brokerFactory != nil {
		broker, err = engine.brokerFactory(runContext)
		if err != nil {
			return nil, fmt.Errorf("create capability broker: %w", err)
		}
		if broker == nil {
			return nil, errors.New("capability broker factory returned nil")
		}
		if broker.SemanticPreDispatchEnabled() != engine.config.Mechanisms.SemanticPreDispatch {
			return nil, errors.New("capability Broker semantic pre-dispatch mode does not match Run configuration")
		}
		if broker.ApprovalSuspensionEnabled() != engine.config.Mechanisms.ApprovalSuspension {
			return nil, errors.New("capability Broker approval suspension mode does not match Run configuration")
		}
		expectsProgrammaticParent := engine.config.ProgramSurface == runtimeconfig.ProgramSurfaceProgrammatic || engine.config.ProgramSurface == runtimeconfig.ProgramSurfaceBoth
		if broker.ProgrammaticParentBound() != expectsProgrammaticParent {
			return nil, errors.New("capability Broker programmatic parent binding does not match Run configuration")
		}
		expectsDirectAlongsideProgrammatic := engine.config.ProgramSurface == runtimeconfig.ProgramSurfaceBoth
		if broker.DirectCallsAllowedWithProgrammaticParent() != expectsDirectAlongsideProgrammatic {
			return nil, errors.New("capability Broker direct/programmatic admission does not match Run configuration")
		}
		if executionRef != nil && broker.RunIdentity() != executionRef.ExecutionID {
			return nil, ErrExecutionIdentityMismatch
		}
		if observation.started {
			if err := observation.capabilityPlan(runContext, broker.CapabilityPlanSHA256()); err != nil {
				return nil, err
			}
			if err := broker.AttachCallLifecycleObserver(&observation); err != nil {
				return nil, fmt.Errorf("attach capability lifecycle observer: %w", err)
			}
		}
		if engine.config.Mechanisms.SplitPhaseCalls {
			maxCalls := broker.CapabilityPlan().MaxCalls()
			if maxCalls > 4 {
				maxCalls = 4
			}
			var tableErr error
			splitPhaseTable, tableErr = capability.NewSplitPhaseTable(broker.CapabilityPlan(), capability.SplitPhaseLimits{
				MaxCalls: maxCalls, MaxCostUnits: uint64(maxCalls) * 4, MaxResultBytes: uint64(engine.config.MaxResponseBytes) * uint64(maxCalls),
			})
			if tableErr != nil {
				return nil, fmt.Errorf("create split-phase call table: %w", tableErr)
			}
			if tableErr = broker.AttachStagedClaimer(splitPhaseTable); tableErr != nil {
				return nil, fmt.Errorf("attach split-phase call table: %w", tableErr)
			}
			runContext = context.WithValue(runContext, splitPhaseContextKey{}, splitPhaseTable)
		}
		runContext = context.WithValue(runContext, brokerContextKey{}, broker)
	}
	defer func() {
		if broker != nil && !brokerFinalized {
			cancel()
			finalizeErr := broker.Finalize(false)
			runErr = errors.Join(runErr, finalizeErr)
			if splitPhaseTable != nil {
				engine.splitPhaseEvidence.set(splitPhaseTable.Snapshot())
			}
		}
	}()
	runContext = context.WithValue(runContext, streamingContextKey{}, streaming)
	for {
		select {
		case <-runContext.Done():
			return nil, runContext.Err()
		case trustedPrepare, ok := <-prepares:
			if !ok {
				goto prepareComplete
			}
			if trustedPrepare == "" {
				return nil, errors.New("stream prepare chunk is empty")
			}
			if err := callStatusWithBytes(runContext, module, "runtime_prepare", []byte(trustedPrepare)); err != nil {
				return nil, withGuestDiagnostic(err, stderr.String())
			}
		}
	}

prepareComplete:
	payload, err = callExecute(runContext, module, request, engine.config.MaxResponseBytes)
	if err != nil {
		return nil, withGuestDiagnostic(err, stderr.String())
	}
	if stdout != nil && stdout.Used() {
		return payload, ErrGuestStdoutBypass
	}
	if broker != nil {
		brokerFinalized = true
		if err := broker.Finalize(true); err != nil {
			return nil, fmt.Errorf("finalize capability broker: %w", err)
		}
		if splitPhaseTable != nil {
			engine.splitPhaseEvidence.set(splitPhaseTable.Snapshot())
		}
	}
	var receipts []receipt.Receipt
	var capabilityCalls uint32
	var capabilityPlanSHA256 string
	if broker != nil {
		receipts, capabilityCalls = broker.Receipts(), broker.CallCount()
		capabilityPlanSHA256 = broker.CapabilityPlanSHA256()
	}
	if observation.started {
		if err := observation.capabilityCalls(runContext, receipts); err != nil {
			return payload, err
		}
		capabilityCallsObserved = true
	}
	if _, err := runtimeconfig.DecodeAndValidateGuestRunResponse(runRequest, payload); err != nil && !errors.Is(err, runtimeconfig.ErrRunResultSchemaMismatch) {
		return payload, err
	}
	payload, err = projectHostEvidence(payload, receipts, capabilityCalls, capabilityPlanSHA256, executionRef, engine.config.MaxResponseBytes)
	if err != nil {
		return nil, err
	}
	validatedResponse, err := runtimeconfig.DecodeAndValidateRunResponse(runRequest, payload)
	if err != nil {
		return payload, err
	}
	if closeErr := module.Close(context.Background()); closeErr != nil {
		return payload, closeErr
	}
	moduleClosed = true
	if temporary != nil {
		if closeErr := temporary.Close(); closeErr != nil {
			return payload, closeErr
		}
		temporary = nil
	}
	if observation.started && engine.workspaceLease != nil && workspaceInitial != nil {
		finalSnapshot, snapshotErr := engine.workspaceLease.Snapshot()
		if snapshotErr != nil {
			return payload, snapshotErr
		}
		if err := observation.workspace(runContext, *workspaceInitial, finalSnapshot); err != nil {
			return payload, err
		}
	}
	if observation.started {
		if validatedResponse.Status == runtimeconfig.RunResponseOK {
			resultSHA256, digestErr := playback.CanonicalSHA256(validatedResponse.Result)
			if digestErr != nil {
				return payload, digestErr
			}
			if err := observation.complete(runContext, resultSHA256); err != nil {
				return payload, err
			}
		} else if err := observation.fail(runContext, "guest_error"); err != nil {
			return payload, err
		}
	}
	return payload, nil
}

func (engine *Engine) verifyDataLocalValueInput() error {
	if engine.valueSlots == nil {
		return nil
	}
	spec, _, _, err := engine.valueSlots.Describe("slot-numpy-sum-v1")
	if errors.Is(err, valueslot.ErrMissingSlot) {
		return nil
	}
	if err != nil || engine.workspaceLease == nil {
		return valueslot.ErrInvalidEntry
	}
	snapshot, err := engine.workspaceLease.Snapshot()
	if err != nil {
		return err
	}
	for _, entry := range snapshot.Entries {
		if entry.Path == "input.npy" && entry.Kind == "file" && entry.SHA256 == spec.InputIdentity {
			return nil
		}
	}
	return valueslot.ErrInvalidEntry
}

type brokerContextKey struct{}
type streamingContextKey struct{}
type preparedRegionContextKey struct{}
type splitPhaseContextKey struct{}
type valueSlotContextKey struct{}

const hostCallPayloadMax = 1024 * 1024

func instantiateCapabilityHost(ctx context.Context, runtime wazerort.Runtime) error {
	_, err := runtime.NewHostModuleBuilder("agent_runtime_v1").
		NewFunctionBuilder().
		WithFunc(hostCall).
		Export("host_call").
		NewFunctionBuilder().
		WithFunc(hostMaterializeValue).
		Export("materialize_value").
		NewFunctionBuilder().
		WithFunc(hostSubmitCall).
		Export("submit_call").
		NewFunctionBuilder().
		WithFunc(hostMaterializeCall).
		Export("materialize_call").
		NewFunctionBuilder().
		WithFunc(hostMaterializeSlot).
		Export("materialize_slot").
		Instantiate(ctx)
	return err
}

func hostMaterializeValue(ctx context.Context, module api.Module, decisionPointer uint32, decisionLength uint32, responsePointer uint32, responseCapacity uint32) int32 {
	if decisionLength != 71 || responseCapacity == 0 || responseCapacity > preparedregion.PreparedRegionMaxPayloadBytes {
		return -1
	}
	table, ok := ctx.Value(preparedRegionContextKey{}).(*preparedregion.PreparedRegionTable)
	if !ok || table == nil {
		return -1
	}
	decisionView, ok := module.Memory().Read(decisionPointer, decisionLength)
	if !ok {
		return -1
	}
	payload, err := table.Claim(string(decisionView))
	if err != nil || len(payload) == 0 || len(payload) > int(responseCapacity) {
		return -1
	}
	if !module.Memory().Write(responsePointer, payload) {
		return -1
	}
	return int32(len(payload))
}

func hostSubmitCall(ctx context.Context, module api.Module, slotPointer, slotLength, requestPointer, requestLength uint32) int32 {
	enginecontract.MarkHostCallAttempt(ctx)
	if slotLength == 0 || slotLength > 96 || requestLength == 0 || requestLength > hostCallPayloadMax {
		return -1
	}
	table, ok := ctx.Value(splitPhaseContextKey{}).(*capability.SplitPhaseTable)
	if !ok || table == nil {
		return -1
	}
	slotView, ok := module.Memory().Read(slotPointer, slotLength)
	if !ok {
		return -1
	}
	requestView, ok := module.Memory().Read(requestPointer, requestLength)
	if !ok {
		return -1
	}
	if err := table.Submit(ctx, string(append([]byte(nil), slotView...)), append([]byte(nil), requestView...)); err != nil {
		return -1
	}
	return 0
}

func hostMaterializeCall(ctx context.Context, module api.Module, slotPointer, slotLength, responsePointer, responseCapacity uint32) int32 {
	if slotLength == 0 || slotLength > 96 || responseCapacity == 0 || responseCapacity > hostCallPayloadMax {
		return -1
	}
	table, tableOK := ctx.Value(splitPhaseContextKey{}).(*capability.SplitPhaseTable)
	broker, brokerOK := ctx.Value(brokerContextKey{}).(*capability.Broker)
	if !tableOK || table == nil || !brokerOK || broker == nil {
		return -1
	}
	slotView, ok := module.Memory().Read(slotPointer, slotLength)
	if !ok {
		return -1
	}
	response, err := table.Materialize(ctx, string(append([]byte(nil), slotView...)), broker)
	if err != nil || len(response) == 0 || len(response) > int(responseCapacity) {
		return -1
	}
	if !module.Memory().Write(responsePointer, response) {
		return -1
	}
	return int32(len(response))
}

func hostMaterializeSlot(ctx context.Context, module api.Module, slotPointer, slotLength, responsePointer, responseCapacity uint32) int32 {
	if slotLength == 0 || slotLength > 128 || responseCapacity < 2 || responseCapacity > hostCallPayloadMax+1 {
		return -1
	}
	table, ok := ctx.Value(valueSlotContextKey{}).(*valueslot.Table)
	if !ok || table == nil {
		return -1
	}
	slotView, ok := module.Memory().Read(slotPointer, slotLength)
	if !ok {
		return -1
	}
	payload, strategy, err := table.Claim(string(append([]byte(nil), slotView...)))
	if err != nil || len(payload) == 0 || len(payload)+1 > int(responseCapacity) {
		return -1
	}
	response := make([]byte, len(payload)+1)
	switch strategy {
	case valueslot.StrategyInlineJSON:
		response[0] = 1
	case valueslot.StrategyPrivateCopy:
		response[0] = 2
	default:
		return -1
	}
	copy(response[1:], payload)
	if !module.Memory().Write(responsePointer, response) {
		return -1
	}
	return int32(len(response))
}

func hostCall(
	ctx context.Context,
	module api.Module,
	requestPointer uint32,
	requestLength uint32,
	responsePointer uint32,
	responseCapacity uint32,
) int32 {
	enginecontract.MarkHostCallAttempt(ctx)
	if requestLength == 0 || requestLength > hostCallPayloadMax ||
		responseCapacity == 0 || responseCapacity > hostCallPayloadMax {
		return -1
	}
	broker, ok := ctx.Value(brokerContextKey{}).(*capability.Broker)
	if !ok || broker == nil {
		return -1
	}
	requestView, ok := module.Memory().Read(requestPointer, requestLength)
	if !ok {
		return -1
	}
	request := append([]byte(nil), requestView...)
	call := func(callContext context.Context) ([]byte, error) {
		if streaming, _ := callContext.Value(streamingContextKey{}).(bool); streaming {
			return broker.CallStreaming(callContext, request)
		}
		return broker.Call(callContext, request)
	}
	var response []byte
	var err error
	if continuation := coldIOContinuationFromContext(ctx); continuation != nil {
		response, err = continuation.wait(ctx, call)
	} else {
		response, err = call(ctx)
	}
	if err != nil || len(response) > int(responseCapacity) {
		return -1
	}
	if !module.Memory().Write(responsePointer, response) {
		return -1
	}
	return int32(len(response))
}

var (
	ErrGuestClaimedExecutionRef    = errors.New("Guest response claimed Host execution reference")
	ErrGuestClaimedCapabilityPlan  = errors.New("Guest response claimed Host capability plan identity")
	ErrGuestStdoutBypass           = errors.New("Guest wrote outside the canonical model log channel")
	ErrExecutionIdentityMismatch   = errors.New("Host execution identity mismatch")
	ErrCapabilityPlanMismatch      = errors.New("Host capability plan identity mismatch")
	ErrObservationIdentityMismatch = errors.New("Host observation execution identity mismatch")
)

func projectHostEvidence(
	payload []byte,
	receipts []receipt.Receipt,
	capabilityCalls uint32,
	capabilityPlanSHA256 string,
	executionRef *runtimeconfig.ExecutionRef,
	maxResponse uint32,
) ([]byte, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return nil, fmt.Errorf("decode guest response for Host evidence: %w", err)
	}
	if envelope == nil {
		return nil, errors.New("guest response is not an object")
	}
	for key := range envelope {
		switch key {
		case "status", "result", "logs", "result_present", "result_source", "source_contract", "receipts", "metrics", "error":
		case "capability_plan_sha256":
			return nil, ErrGuestClaimedCapabilityPlan
		case "execution_ref":
			return nil, ErrGuestClaimedExecutionRef
		default:
			return nil, fmt.Errorf("guest response contains non-canonical field %q", key)
		}
	}
	if receipts == nil {
		receipts = []receipt.Receipt{}
	}
	for _, hostReceipt := range receipts {
		if capabilityPlanSHA256 == "" || hostReceipt.CapabilityPlanSHA256 != capabilityPlanSHA256 {
			return nil, ErrCapabilityPlanMismatch
		}
	}
	if capabilityPlanSHA256 != "" {
		encodedPlan, err := json.Marshal(capabilityPlanSHA256)
		if err != nil {
			return nil, fmt.Errorf("encode Host capability plan identity: %w", err)
		}
		envelope["capability_plan_sha256"] = encodedPlan
	}
	if executionRef != nil {
		if executionRef.Validate() != nil {
			return nil, runtimeconfig.ErrInvalidInvocationRef
		}
		for _, hostReceipt := range receipts {
			if hostReceipt.RunID != executionRef.ExecutionID {
				return nil, ErrExecutionIdentityMismatch
			}
		}
		encodedRef, err := json.Marshal(executionRef)
		if err != nil {
			return nil, fmt.Errorf("encode Host execution reference: %w", err)
		}
		envelope["execution_ref"] = encodedRef
	}

	encodedReceipts, err := json.Marshal(receipts)
	if err != nil {
		return nil, fmt.Errorf("encode Host receipts: %w", err)
	}
	envelope["receipts"] = encodedReceipts
	var metrics map[string]json.RawMessage
	if raw, ok := envelope["metrics"]; !ok || json.Unmarshal(raw, &metrics) != nil || metrics == nil {
		return nil, errors.New("guest response metrics are invalid")
	}
	for key := range metrics {
		switch key {
		case "guest_time_ms", "capability_calls", "result_bytes":
		default:
			return nil, fmt.Errorf("guest response metrics contain non-canonical field %q", key)
		}
	}
	encodedCalls, err := json.Marshal(capabilityCalls)
	if err != nil {
		return nil, err
	}
	metrics["capability_calls"] = encodedCalls
	encodedMetrics, err := json.Marshal(metrics)
	if err != nil {
		return nil, fmt.Errorf("encode Host metrics: %w", err)
	}
	envelope["metrics"] = encodedMetrics
	merged, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("encode response with Host evidence: %w", err)
	}
	if uint64(len(merged)) > uint64(maxResponse) {
		return nil, errors.New("response with Host evidence exceeds configured bounds")
	}
	return merged, nil
}

func callNoArgs(ctx context.Context, module api.Module, name string) error {
	function := module.ExportedFunction(name)
	if function == nil {
		return fmt.Errorf("required export %q is missing", name)
	}
	if _, err := function.Call(ctx); err != nil {
		return fmt.Errorf("call %s: %w", name, err)
	}
	return nil
}

func callSourceValidation(ctx context.Context, module api.Module, export string, request []byte) error {
	results, release, err := callWithBytes(ctx, module, export, request)
	if release != nil {
		defer release()
	}
	if err != nil {
		return err
	}
	if len(results) != 1 {
		return fmt.Errorf("%s returned an unexpected result count", export)
	}
	switch uint32(results[0]) {
	case 0:
		return nil
	case 1:
		return runtimeconfig.ErrAgentSourceContractUnsupported
	case 2:
		return runtimeconfig.ErrAgentSourceInvalid
	default:
		return errors.New("runtime_validate_source returned an invalid status")
	}
}

func callStatusWithBytes(ctx context.Context, module api.Module, name string, data []byte) error {
	results, release, err := callWithBytes(ctx, module, name, data)
	if release != nil {
		defer release()
	}
	if err != nil {
		return err
	}
	if len(results) != 1 || uint32(results[0]) != 0 {
		return fmt.Errorf("%s returned non-zero status", name)
	}
	return nil
}

func callExecute(ctx context.Context, module api.Module, request []byte, maxResponse uint32) ([]byte, error) {
	return callGuestResponse(ctx, module, "execute", request, maxResponse)
}

func callGuestResponse(ctx context.Context, module api.Module, name string, request []byte, maxResponse uint32) ([]byte, error) {
	results, release, err := callWithBytes(ctx, module, name, request)
	if release != nil {
		defer release()
	}
	if err != nil {
		return nil, err
	}
	if len(results) != 1 {
		return nil, fmt.Errorf("%s returned an unexpected result count", name)
	}
	return readGuestResponse(module.Memory(), uint32(results[0]), maxResponse)
}

func callWithBytes(
	ctx context.Context,
	module api.Module,
	name string,
	data []byte,
) (results []uint64, release func(), err error) {
	if len(data) > math.MaxUint32 {
		return nil, nil, errors.New("guest input is too large")
	}
	allocate := module.ExportedFunction("alloc")
	deallocate := module.ExportedFunction("dealloc")
	function := module.ExportedFunction(name)
	if allocate == nil || deallocate == nil || function == nil {
		return nil, nil, fmt.Errorf("required allocation or %s export is missing", name)
	}
	allocated, err := allocate.Call(ctx, uint64(uint32(len(data))))
	if err != nil || len(allocated) != 1 || allocated[0] == 0 {
		return nil, nil, fmt.Errorf("guest allocation failed: %w", err)
	}
	pointer := uint32(allocated[0])
	release = func() {
		releaseContext, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, _ = deallocate.Call(releaseContext, uint64(pointer))
	}
	if !module.Memory().Write(pointer, data) {
		release()
		return nil, nil, errors.New("guest input write is out of bounds")
	}
	results, err = function.Call(ctx, uint64(pointer), uint64(uint32(len(data))))
	if err != nil {
		release()
		return nil, nil, fmt.Errorf("call %s: %w", name, err)
	}
	return results, release, nil
}

func readGuestResponse(memory api.Memory, pointer uint32, maxResponse uint32) ([]byte, error) {
	header, ok := memory.Read(pointer, 4)
	if !ok {
		return nil, errors.New("response length prefix is out of bounds")
	}
	length := binary.LittleEndian.Uint32(header)
	if length > maxResponse {
		return nil, fmt.Errorf("response payload length %d exceeds limit %d", length, maxResponse)
	}
	if uint64(pointer)+4+uint64(length) > math.MaxUint32 {
		return nil, errors.New("response frame pointer overflows memory address space")
	}
	frame, ok := memory.Read(pointer, 4+length)
	if !ok {
		return nil, errors.New("response frame is out of bounds")
	}
	return enginecontract.DecodeLengthPrefixedResponse(frame, maxResponse)
}

const guestDiagnosticMax = 16 * 1024

func withGuestDiagnostic(base error, diagnostic string) error {
	diagnostic = strings.TrimSpace(diagnostic)
	if diagnostic == "" {
		return base
	}
	if len(diagnostic) > guestDiagnosticMax {
		diagnostic = diagnostic[len(diagnostic)-guestDiagnosticMax:]
	}
	return fmt.Errorf("%w; guest stderr: %s", base, diagnostic)
}

package wazero

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	"github.com/bkmashiro/agent-python-runtime/runtime/receipt"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
	wazerort "github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// BrokerFactory returns a new per-Run capability broker. Returning nil leaves
// the bridge fail-closed for Runs without Host grants.
type BrokerFactory func(context.Context) (*capability.Broker, error)

type Observation struct {
	Phase    string
	Duration time.Duration
	Success  bool
}

type Observer func(Observation)

const (
	COWWarmupRequestShellV1 = "request-shell-v1"
	COWWarmupNumPyReadyV1   = "numpy-ready-v1"
)

// ValidateCOWWarmupProfile validates an artifact-defined warmup profile ID.
// The profile implementation remains inside the verified guest artifact.
func ValidateCOWWarmupProfile(profile string) error {
	if profile == "" {
		return nil
	}
	if len(profile) > 64 {
		return errors.New("COW warmup profile exceeds 64 bytes")
	}
	for index, character := range profile {
		alpha := character >= 'a' && character <= 'z'
		digit := character >= '0' && character <= '9'
		separator := character == '-' || character == '_' || character == '.'
		if !alpha && !digit && !separator || index == 0 && !alpha && !digit {
			return errors.New("COW warmup profile must use lowercase ASCII identifiers")
		}
	}
	return nil
}

// Factory constructs wazero-backed runners behind the neutral engine contract.
type Factory struct {
	BrokerFactory    BrokerFactory
	Observer         Observer
	FootprintSink    enginecontract.FootprintSink
	ReclaimSink      enginecontract.ReclaimSink
	Strategy         enginecontract.ExecutionStrategy
	PreparedCapacity uint32
	// PreparedMaxCapacity reserves a bounded pool envelope that can grow without
	// creating another Runtime, CompiledModule, or COW baseline. Zero means the
	// initial PreparedCapacity is also the maximum.
	PreparedMaxCapacity uint32
	// PreparedRefillWorkers selects a fixed worker count. Zero keeps the bounded
	// default of at most four. Explicit values are limited to sixteen.
	PreparedRefillWorkers uint32
	// AdaptivePreparedRefill opts into four outstanding refills normally, eight
	// below the low watermark, and twelve at critical pressure or while callers
	// wait. It requires PreparedRefillWorkers to remain zero.
	AdaptivePreparedRefill bool
	// VerifyCOWPreparedImage opts into a full linear-memory digest on every
	// prepared slot. It is intended for bounded diagnostics, not production.
	VerifyCOWPreparedImage bool
	// COWWarmupProfile selects an audited guest-defined canonical warmup. Empty
	// keeps the current production baseline. Arbitrary source is never accepted.
	COWWarmupProfile string
	// PreparedWarmupProfile selects the same artifact-defined warmup for every
	// independently initialized single-use prepared slot. It is an experimental
	// measurement seam and is mutually exclusive with COWWarmupProfile.
	PreparedWarmupProfile string
	// COWSnapshotShell derives a replacement-only module with empty active data
	// payloads and reconstructs those payloads in the canonical memory seed. It
	// is valid only for cow-ready-single-use.
	COWSnapshotShell bool
	CompilationCache *CompilationCache

	// WorkspaceManager/Ref/Owner form one Host-owned binding. The untrusted
	// RunRequest cannot select a Host path or change this binding. A Factory
	// holds one exclusive writer lease for the lifetime of the Runner so that
	// sequential disposable instances see the same ordinary-file tree.
	WorkspaceManager *workspace.Manager
	WorkspaceRef     workspace.Ref
	WorkspaceOwner   string
}

func (Factory) Name() string { return "wazero" }

func (factory Factory) New(ctx context.Context, wasm []byte, config runtimeconfig.RunConfig) (enginecontract.Runner, error) {
	workspaceFields := 0
	if factory.WorkspaceManager != nil {
		workspaceFields++
	}
	if factory.WorkspaceRef != "" {
		workspaceFields++
	}
	if factory.WorkspaceOwner != "" {
		workspaceFields++
	}
	if workspaceFields != 0 && workspaceFields != 3 {
		return nil, errors.New("workspace binding requires manager, reference, and owner")
	}
	maximum := factory.PreparedMaxCapacity
	if maximum == 0 {
		maximum = factory.PreparedCapacity
	}
	if maximum < factory.PreparedCapacity {
		return nil, fmt.Errorf("prepared maximum capacity %d is below initial capacity %d", maximum, factory.PreparedCapacity)
	}
	hardBound := maxPreparedCapacity
	if factory.Strategy == enginecontract.StrategyCOWReadySingleUse {
		hardBound = maxCOWPreparedCapacity
	}
	if factory.PreparedCapacity > hardBound || maximum > hardBound {
		return nil, fmt.Errorf("prepared capacity %d/%d exceeds hard bound %d", factory.PreparedCapacity, maximum, hardBound)
	}
	if factory.AdaptivePreparedRefill && (factory.PreparedRefillWorkers != 0 || factory.PreparedCapacity == 0) {
		return nil, errors.New("adaptive prepared refill requires a non-empty pool and no fixed worker count")
	}
	if factory.PreparedRefillWorkers > maxPreparedRefillWorkers || factory.PreparedRefillWorkers > maximum ||
		(factory.PreparedRefillWorkers > 0 && factory.PreparedCapacity == 0) {
		return nil, errors.New("prepared refill worker count is outside the configured pool bounds")
	}
	if err := ValidateCOWWarmupProfile(factory.COWWarmupProfile); err != nil {
		return nil, err
	}
	if err := ValidateCOWWarmupProfile(factory.PreparedWarmupProfile); err != nil {
		return nil, err
	}
	if factory.COWWarmupProfile != "" && factory.PreparedWarmupProfile != "" {
		return nil, errors.New("COW and non-COW prepared warmup profiles are mutually exclusive")
	}
	if factory.COWWarmupProfile != "" && factory.Strategy != enginecontract.StrategyCOWReadySingleUse {
		return nil, errors.New("COW warmup profile is outside cow-ready-single-use")
	}
	if factory.PreparedWarmupProfile != "" && factory.Strategy != enginecontract.StrategySingleUsePrepared {
		return nil, errors.New("prepared warmup profile is outside single-use-preinitialized")
	}
	if factory.COWSnapshotShell && factory.Strategy != enginecontract.StrategyCOWReadySingleUse {
		return nil, errors.New("COW snapshot shell is outside cow-ready-single-use")
	}
	refillWorkers := factory.PreparedRefillWorkers
	if factory.AdaptivePreparedRefill {
		refillWorkers = adaptivePreparedRefillSentinel
	}
	warmupProfile := factory.COWWarmupProfile
	if factory.PreparedWarmupProfile != "" {
		warmupProfile = factory.PreparedWarmupProfile
	}
	var workspaceLease *workspace.Lease
	if factory.WorkspaceManager != nil {
		var err error
		workspaceLease, err = factory.WorkspaceManager.Acquire(factory.WorkspaceRef, factory.WorkspaceOwner)
		if err != nil {
			return nil, err
		}
		defer func() {
			if workspaceLease != nil {
				_ = workspaceLease.Release()
			}
		}()
	}
	result, err := newEngine(ctx, wasm, factory.COWSnapshotShell, config, factory.BrokerFactory, factory.Observer, factory.FootprintSink, factory.ReclaimSink, factory.Strategy, factory.PreparedCapacity, maximum, refillWorkers, warmupProfile, factory.VerifyCOWPreparedImage, factory.CompilationCache, workspaceLease)
	if err != nil {
		return nil, err
	}
	workspaceLease = nil
	return result, nil
}

var _ enginecontract.Factory = Factory{}
var _ enginecontract.Runner = (*Engine)(nil)

// Engine owns a compiled guest module. V1 either initializes synchronously per
// Run or checks out a never-served, single-use initialized module; every served
// module is closed instead of restored or returned to a pool.
type Engine struct {
	runtime                  wazerort.Runtime
	compiled                 wazerort.CompiledModule
	cowSnapshotShell         *cowSnapshotShellPlan
	config                   runtimeconfig.RunConfig
	brokerFactory            BrokerFactory
	observer                 Observer
	footprintSink            enginecontract.FootprintSink
	reclaimSink              enginecontract.ReclaimSink
	activeFootprints         *activeFootprintRegistry
	strategy                 enginecontract.ExecutionStrategy
	verifyCOWPreparedImage   bool
	preparedWarmupProfile    string
	preparedWarmupGeneration string
	stateCensus              ReactorStateCensus
	cowRuntime               cowPreparedRuntime
	pool                     *preparedPool
	workspaceLease           *workspace.Lease
	workspaceRun             chan struct{}
}

func New(ctx context.Context, wasm []byte, config runtimeconfig.RunConfig) (*Engine, error) {
	return newEngine(ctx, wasm, false, config, nil, nil, nil, nil, "", 0, 0, 0, "", false, nil, nil)
}

func NewWithBrokerFactory(
	ctx context.Context,
	wasm []byte,
	config runtimeconfig.RunConfig,
	brokerFactory BrokerFactory,
) (*Engine, error) {
	return newEngine(ctx, wasm, false, config, brokerFactory, nil, nil, nil, "", 0, 0, 0, "", false, nil, nil)
}

func newEngine(
	ctx context.Context,
	wasm []byte,
	cowSnapshotShell bool,
	config runtimeconfig.RunConfig,
	brokerFactory BrokerFactory,
	observer Observer,
	footprintSink enginecontract.FootprintSink,
	reclaimSink enginecontract.ReclaimSink,
	requestedStrategy enginecontract.ExecutionStrategy,
	preparedCapacity uint32,
	preparedMaxCapacity uint32,
	preparedRefillWorkers uint32,
	cowWarmupProfile string,
	verifyCOWPreparedImage bool,
	compilationCache *CompilationCache,
	workspaceLease *workspace.Lease,
) (*Engine, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid run config: %w", err)
	}
	strategy, err := resolveStrategy(requestedStrategy, preparedCapacity)
	if err != nil {
		return nil, err
	}
	if len(wasm) < 8 {
		return nil, errors.New("guest module is too short")
	}
	if err := ValidateCOWWarmupProfile(cowWarmupProfile); err != nil {
		return nil, err
	}
	if cowWarmupProfile != "" && strategy != enginecontract.StrategyCOWReadySingleUse && strategy != enginecontract.StrategySingleUsePrepared {
		return nil, errors.New("prepared warmup profile is inactive")
	}
	warmupGeneration := ""
	if cowWarmupProfile != "" {
		artifactDigest := sha256.Sum256(wasm)
		generationDigest := sha256.Sum256(append(artifactDigest[:], []byte("\x00"+cowWarmupProfile)...))
		warmupGeneration = fmt.Sprintf("%x", generationDigest[:])
	}
	compiledWasm := wasm
	var snapshotShell *cowSnapshotShellPlan
	if cowSnapshotShell {
		snapshotShell, err = buildCOWSnapshotShell(wasm)
		if err != nil {
			return nil, err
		}
		compiledWasm = snapshotShell.shell
	}

	runtimeConfig := wazerort.NewRuntimeConfig().
		WithCloseOnContextDone(true).
		WithMemoryLimitPages(config.MemoryLimitPages)
	var releaseCompilationCache func()
	if compilationCache != nil {
		inner, release, err := compilationCache.acquire()
		if err != nil {
			return nil, err
		}
		releaseCompilationCache = release
		defer func() {
			if releaseCompilationCache != nil {
				releaseCompilationCache()
			}
		}()
		runtimeConfig = runtimeConfig.WithCompilationCache(inner)
	}
	wasmRuntime := wazerort.NewRuntimeWithConfig(ctx, runtimeConfig)
	hostStarted := time.Now()
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, wasmRuntime); err != nil {
		observe(observer, "instantiate_host", hostStarted, err)
		_ = wasmRuntime.Close(ctx)
		return nil, fmt.Errorf("instantiate WASI imports: %w", err)
	}
	if err := instantiateCapabilityHost(ctx, wasmRuntime); err != nil {
		observe(observer, "instantiate_host", hostStarted, err)
		_ = wasmRuntime.Close(ctx)
		return nil, fmt.Errorf("instantiate capability imports: %w", err)
	}
	observe(observer, "instantiate_host", hostStarted, nil)
	compileStarted := time.Now()
	compiled, err := wasmRuntime.CompileModule(ctx, compiledWasm)
	observe(observer, "compile", compileStarted, err)
	if snapshotShell != nil {
		snapshotShell.shell = nil
	}
	if err != nil {
		if releaseCompilationCache != nil {
			releaseCompilationCache()
			releaseCompilationCache = nil
		}
		_ = wasmRuntime.Close(ctx)
		return nil, fmt.Errorf("compile guest: %w", err)
	}
	if releaseCompilationCache != nil {
		releaseCompilationCache()
		releaseCompilationCache = nil
	}
	engine := &Engine{
		runtime:                  wasmRuntime,
		compiled:                 compiled,
		cowSnapshotShell:         snapshotShell,
		config:                   config,
		brokerFactory:            brokerFactory,
		observer:                 observer,
		footprintSink:            footprintSink,
		reclaimSink:              reclaimSink,
		activeFootprints:         newActiveFootprintRegistry(),
		strategy:                 strategy,
		verifyCOWPreparedImage:   verifyCOWPreparedImage,
		preparedWarmupProfile:    cowWarmupProfile,
		preparedWarmupGeneration: warmupGeneration,
		stateCensus:              censusCompiledReactor(compiled, wasm),
		workspaceLease:           workspaceLease,
	}
	if workspaceLease != nil {
		engine.workspaceRun = make(chan struct{}, 1)
	}
	if err := engine.initializePreparedPool(preparedCapacity, preparedMaxCapacity, preparedRefillWorkers); err != nil {
		_ = engine.Close(context.Background())
		return nil, err
	}
	return engine, nil
}

func (engine *Engine) Properties() enginecontract.Properties {
	return enginecontract.Properties{
		Backend:           "wazero",
		ResetMode:         enginecontract.ResetModeFreshInstance,
		RequestedStrategy: engine.strategy,
		ActiveStrategy:    engine.strategy,
	}
}

// StateCensus returns a defensive copy of the compile-time reactor state
// census. Unknown state remains fail-closed until a later mechanism proves it.
func (engine *Engine) StateCensus() ReactorStateCensus {
	if engine == nil {
		return ReactorStateCensus{}
	}
	census := engine.stateCensus
	census.Artifact.ImportModules = append([]string{}, census.Artifact.ImportModules...)
	census.Artifact.Globals.ExportedMutableNames = append([]string{}, census.Artifact.Globals.ExportedMutableNames...)
	census.UnknownStateClasses = append([]string(nil), census.UnknownStateClasses...)
	census.Reasons = append([]string(nil), census.Reasons...)
	return census
}

func resolveStrategy(requested enginecontract.ExecutionStrategy, preparedCapacity uint32) (enginecontract.ExecutionStrategy, error) {
	if requested == "" {
		if preparedCapacity > 0 {
			return enginecontract.StrategySingleUsePrepared, nil
		}
		return enginecontract.StrategyFreshInstance, nil
	}
	switch requested {
	case enginecontract.StrategyFreshInstance:
		if preparedCapacity != 0 {
			return "", errors.New("fresh-instance strategy requires zero prepared capacity")
		}
		return requested, nil
	case enginecontract.StrategySingleUsePrepared:
		if preparedCapacity == 0 {
			return "", errors.New("single-use-preinitialized strategy requires positive prepared capacity")
		}
		return requested, nil
	case enginecontract.StrategyCOWReadySingleUse:
		if preparedCapacity == 0 {
			return "", errors.New("cow-ready-single-use strategy requires positive prepared capacity")
		}
		if !cowReadyStrategySupported() {
			return "", errors.New("cow-ready-single-use strategy is not supported on this platform")
		}
		return requested, nil
	case enginecontract.StrategyCOWFullRemapRestore, enginecontract.StrategyCOWLocality, enginecontract.StrategyCOWAdaptiveReset:
		return "", fmt.Errorf("execution strategy %q is not implemented", requested)
	default:
		return "", fmt.Errorf("unknown execution strategy %q", requested)
	}
}

func (engine *Engine) Close(ctx context.Context) error {
	if engine == nil || engine.runtime == nil {
		return nil
	}
	engine.closePreparedPool()
	runtimeErr := engine.runtime.Close(ctx)
	var cowErr error
	if engine.cowRuntime != nil {
		cowErr = engine.cowRuntime.close()
	}
	var workspaceErr error
	if engine.workspaceLease != nil {
		workspaceErr = engine.workspaceLease.Release()
	}
	return errors.Join(runtimeErr, cowErr, workspaceErr)
}

func (engine *Engine) Run(ctx context.Context, request []byte, trustedPrepare string) (payload []byte, runErr error) {
	if len(request) == 0 || uint64(len(request)) > uint64(engine.config.MaxRequestBytes) {
		return nil, errors.New("request exceeds configured bounds")
	}
	runRequest, err := runtimeconfig.DecodeRunRequest(request)
	if err != nil {
		return nil, err
	}
	var executionRef *runtimeconfig.ExecutionRef
	if invocationRef, ok := enginecontract.InvocationRefFromContext(ctx); ok {
		codeDigest := sha256.Sum256([]byte(runRequest.Code))
		candidate := runtimeconfig.ExecutionRef{
			InvocationRef: invocationRef, ExecutedCodeSHA256: fmt.Sprintf("sha256:%x", codeDigest[:]),
		}
		executionRef = &candidate
	}

	runContext, cancel := context.WithTimeout(ctx, engine.config.Timeout)
	defer cancel()
	if engine.workspaceRun != nil {
		select {
		case engine.workspaceRun <- struct{}{}:
			defer func() { <-engine.workspaceRun }()
		case <-runContext.Done():
			return nil, runContext.Err()
		}
	}
	var broker *capability.Broker
	if engine.brokerFactory != nil {
		var err error
		broker, err = engine.brokerFactory(runContext)
		if err != nil {
			return nil, fmt.Errorf("create capability broker: %w", err)
		}
		if broker == nil {
			return nil, errors.New("capability broker factory returned nil")
		}
		if executionRef != nil && broker.RunIdentity() != executionRef.ExecutionID {
			return nil, ErrExecutionIdentityMismatch
		}
		runContext = context.WithValue(runContext, brokerContextKey{}, broker)
	}
	runSucceeded := false
	finalizeReason := "execution_failed"
	if broker != nil {
		defer func() {
			if errors.Is(runContext.Err(), context.DeadlineExceeded) {
				finalizeReason = "timeout"
			} else if errors.Is(runContext.Err(), context.Canceled) {
				finalizeReason = "cancelled"
			}
			finalizeTimeout := engine.config.Timeout
			if finalizeTimeout < 5*time.Second {
				finalizeTimeout = 5 * time.Second
			}
			if finalizeTimeout > time.Minute {
				finalizeTimeout = time.Minute
			}
			finalizeContext, finalizeCancel := context.WithTimeout(context.Background(), finalizeTimeout)
			defer finalizeCancel()
			if finalizeErr := broker.FinalizeRun(finalizeContext, runSucceeded, finalizeReason); finalizeErr != nil {
				runErr = errors.Join(runErr, fmt.Errorf("finalize Host transaction: %w", finalizeErr))
				payload = nil
			}
			if closeErr := broker.CloseJournal(); closeErr != nil {
				runErr = errors.Join(runErr, fmt.Errorf("close Host transaction journal: %w", closeErr))
				payload = nil
			}
		}()
	}
	instance, err := engine.checkoutModule(runContext)
	if err != nil {
		return nil, err
	}
	module := instance.module
	guestStderr := instance.stderr
	activeAttemptID := ""
	activeRegistered := false
	defer func() {
		observePreparedFootprint(runContext, engine.footprintSink, engine.strategy, instance)
		closeStarted := time.Now()
		closeErr := engine.closeServedInstance(instance)
		observePreparedReclaim(runContext, engine.reclaimSink, engine.strategy, instance, time.Since(closeStarted), closeErr)
		if closeErr != nil {
			runSucceeded = false
			finalizeReason = "cleanup_failed"
			runErr = errors.Join(runErr, fmt.Errorf("close served instance: %w", closeErr))
			payload = nil
		}
		if activeRegistered {
			engine.unregisterActiveFootprint(activeAttemptID)
		}
	}()
	if instance.mounts != nil {
		if err := instance.mounts.activate(); err != nil {
			return nil, fmt.Errorf("activate module filesystems: %w", err)
		}
	}
	if attemptID, ok := enginecontract.AttemptIdentityFromContext(runContext); ok && instance.footprintSource != nil {
		if err := engine.registerActiveFootprint(attemptID, instance.footprintSource); err != nil {
			return nil, fmt.Errorf("register active footprint: %w", err)
		}
		activeAttemptID = attemptID
		activeRegistered = true
	}
	if instance.fromPool && engine.pool != nil {
		engine.pool.executing.Add(1)
		defer engine.pool.executing.Add(^uint32(0))
	}

	if trustedPrepare != "" {
		prepareStarted := time.Now()
		err = callStatusWithBytes(runContext, module, "runtime_prepare", []byte(trustedPrepare))
		observe(engine.observer, "prepare", prepareStarted, err)
		if err != nil {
			return nil, withGuestDiagnostic(err, guestStderr.String())
		}
	}
	executeStarted := time.Now()
	payload, err = callExecute(runContext, module, request, engine.config.MaxResponseBytes)
	observe(engine.observer, "execute", executeStarted, err)
	if err != nil {
		return nil, withGuestDiagnostic(err, guestStderr.String())
	}
	var receipts []receipt.Receipt
	var capabilityCalls uint32
	if broker != nil {
		receipts, capabilityCalls = broker.Receipts(), broker.CallCount()
	}
	_, guestValidationErr := runtimeconfig.DecodeAndValidateGuestRunResponse(runRequest, payload)
	if guestValidationErr != nil && !errors.Is(guestValidationErr, runtimeconfig.ErrRunResultSchemaMismatch) {
		finalizeReason = "invalid_output"
		return payload, guestValidationErr
	}
	payload, err = projectHostEvidence(payload, receipts, capabilityCalls, executionRef, engine.config.MaxResponseBytes)
	if err != nil {
		return nil, err
	}
	decodedResponse, validationErr := runtimeconfig.DecodeAndValidateRunResponse(runRequest, payload)
	if validationErr != nil {
		finalizeReason = "invalid_output"
		return payload, validationErr
	}
	if decodedResponse.Status == runtimeconfig.RunResponseError {
		finalizeReason = "guest_error"
	}
	runSucceeded = decodedResponse.Status == runtimeconfig.RunResponseOK
	return payload, nil
}

type brokerContextKey struct{}

const hostCallPayloadMax = 1024 * 1024

func instantiateCapabilityHost(ctx context.Context, runtime wazerort.Runtime) error {
	_, err := runtime.NewHostModuleBuilder("agent_runtime_v1").
		NewFunctionBuilder().
		WithFunc(hostCall).
		Export("host_call").
		Instantiate(ctx)
	return err
}

func hostCall(
	ctx context.Context,
	module api.Module,
	requestPointer uint32,
	requestLength uint32,
	responsePointer uint32,
	responseCapacity uint32,
) int32 {
	if requestLength == 0 || requestLength > hostCallPayloadMax ||
		responseCapacity == 0 || responseCapacity > hostCallPayloadMax {
		return -1
	}
	if recordInitializationHostCall(ctx) {
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
	response, err := broker.Call(ctx, request)
	if err != nil || len(response) > int(responseCapacity) {
		return -1
	}
	if !module.Memory().Write(responsePointer, response) {
		return -1
	}
	return int32(len(response))
}

var (
	ErrGuestClaimedExecutionRef  = errors.New("Guest response claimed Host execution reference")
	ErrExecutionIdentityMismatch = errors.New("Host execution identity mismatch")
)

func mergeHostEvidence(
	payload []byte,
	receipts []receipt.Receipt,
	capabilityCalls uint32,
	maxResponse uint32,
) ([]byte, error) {
	return projectHostEvidence(payload, receipts, capabilityCalls, nil, maxResponse)
}

func projectHostEvidence(
	payload []byte,
	receipts []receipt.Receipt,
	capabilityCalls uint32,
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
		case "status", "result", "receipts", "metrics", "error":
		case "execution_ref":
			return nil, ErrGuestClaimedExecutionRef
		default:
			return nil, fmt.Errorf("guest response contains non-canonical field %q", key)
		}
	}
	if receipts == nil {
		receipts = []receipt.Receipt{}
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
	results, release, err := callWithBytes(ctx, module, "execute", request)
	if release != nil {
		defer release()
	}
	if err != nil {
		return nil, err
	}
	if len(results) != 1 {
		return nil, errors.New("execute returned an unexpected result count")
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

func observe(observer Observer, phase string, started time.Time, err error) {
	if observer == nil {
		return
	}
	duration := time.Since(started)
	observer(Observation{Phase: phase, Duration: duration, Success: err == nil})
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

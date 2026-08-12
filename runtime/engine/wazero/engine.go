package wazero

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	"github.com/bkmashiro/agent-python-runtime/runtime/observe"
	"github.com/bkmashiro/agent-python-runtime/runtime/playback"
	"github.com/bkmashiro/agent-python-runtime/runtime/receipt"
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
}

func (Factory) Name() string { return "wazero" }

func (factory Factory) New(ctx context.Context, wasm []byte, config runtimeconfig.RunConfig) (enginecontract.Runner, error) {
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
	var lease *workspace.Lease
	if factory.WorkspaceManager != nil {
		var err error
		lease, err = factory.WorkspaceManager.Acquire(factory.WorkspaceRef, factory.WorkspaceOwner)
		if err != nil {
			return nil, err
		}
	}
	engine, err := newEngine(ctx, wasm, config, factory.BrokerFactory, lease)
	if err != nil && lease != nil {
		_ = lease.Release()
	}
	return engine, err
}

var _ enginecontract.Factory = Factory{}
var _ enginecontract.Runner = (*Engine)(nil)

type Engine struct {
	runtime        wazerort.Runtime
	compiled       wazerort.CompiledModule
	config         runtimeconfig.RunConfig
	brokerFactory  BrokerFactory
	workspaceLease *workspace.Lease
	workspaceRun   chan struct{}
	artifactSHA256 string
}

func New(ctx context.Context, wasm []byte, config runtimeconfig.RunConfig) (*Engine, error) {
	return newEngine(ctx, wasm, config, nil, nil)
}

func NewWithBrokerFactory(ctx context.Context, wasm []byte, config runtimeconfig.RunConfig, factory BrokerFactory) (*Engine, error) {
	return newEngine(ctx, wasm, config, factory, nil)
}

func newEngine(ctx context.Context, wasm []byte, config runtimeconfig.RunConfig, brokerFactory BrokerFactory, lease *workspace.Lease) (*Engine, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid run config: %w", err)
	}
	if len(wasm) < 8 {
		return nil, errors.New("guest module is too short")
	}
	if config.DeterministicVerification != nil {
		digest := sha256.Sum256(wasm)
		if fmt.Sprintf("sha256:%x", digest[:]) != config.DeterministicVerification.ArtifactSHA256() || lease != nil {
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
	artifactDigest := sha256.Sum256(wasm)
	engine := &Engine{
		runtime: wasmRuntime, compiled: compiled, config: config, brokerFactory: brokerFactory, workspaceLease: lease,
		artifactSHA256: fmt.Sprintf("sha256:%x", artifactDigest[:]),
	}
	if lease != nil {
		engine.workspaceRun = make(chan struct{}, 1)
	}
	return engine, nil
}

func (engine *Engine) Properties() enginecontract.Properties {
	properties := enginecontract.Properties{Backend: "wazero"}
	if engine.config.ExecutionProfile != nil {
		profile := engine.config.ExecutionProfile
		properties.ExecutionProfileID = profile.ID()
		properties.AllowedImports = profile.AllowedImports()
		properties.AvailableImports = profile.AvailableImports()
		properties.QualifiedImports = profile.QualifiedImports()
		properties.ArtifactSHA256 = profile.ArtifactSHA256()
		properties.ManifestSHA256 = profile.ManifestSHA256()
	}
	return properties
}

func (engine *Engine) Close(ctx context.Context) error {
	if engine == nil || engine.runtime == nil {
		return nil
	}
	runtimeErr := engine.runtime.Close(ctx)
	var workspaceErr error
	if engine.workspaceLease != nil {
		workspaceErr = engine.workspaceLease.Release()
	}
	return errors.Join(runtimeErr, workspaceErr)
}

func (engine *Engine) moduleConfig(stderr io.Writer) (wazerort.ModuleConfig, *workspace.Temporary, error) {
	config := wazerort.NewModuleConfig().WithName("").WithStderr(stderr)
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

func (engine *Engine) Run(ctx context.Context, request []byte, trustedPrepare string) (payload []byte, runErr error) {
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
	if engine.workspaceRun != nil {
		select {
		case engine.workspaceRun <- struct{}{}:
		case <-runContext.Done():
			return nil, runContext.Err()
		}
		defer func() { <-engine.workspaceRun }()
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
				terminalErr := observation.fail(context.Background(), "runtime_error")
				runErr = errors.Join(runErr, terminalErr)
			}
		}()
	}
	stderr := &bytes.Buffer{}
	moduleConfig, temporary, err := engine.moduleConfig(stderr)
	if err != nil {
		return nil, err
	}
	if temporary != nil {
		defer func() { runErr = errors.Join(runErr, temporary.Close()) }()
	}
	module, err := engine.runtime.InstantiateModule(runContext, engine.compiled, moduleConfig)
	if err != nil {
		return nil, fmt.Errorf("instantiate guest: %w", err)
	}
	moduleClosed := false
	defer func() {
		if !moduleClosed {
			runErr = errors.Join(runErr, module.Close(context.Background()))
		}
	}()
	if err := callNoArgs(runContext, module, "_initialize"); err != nil {
		return nil, withGuestDiagnostic(err, stderr.String())
	}
	if err := callStatusWithBytes(runContext, module, "runtime_init", []byte("{}")); err != nil {
		return nil, withGuestDiagnostic(err, stderr.String())
	}
	if trustedPrepare != "" {
		if err := callStatusWithBytes(runContext, module, "runtime_prepare", []byte(trustedPrepare)); err != nil {
			return nil, withGuestDiagnostic(err, stderr.String())
		}
	}
	if err := callSourceValidation(runContext, module, request); err != nil {
		return nil, withGuestDiagnostic(err, stderr.String())
	}
	var broker *capability.Broker
	if engine.brokerFactory != nil {
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
		if observation.started {
			if err := observation.capabilityPlan(runContext, broker.CapabilityPlanSHA256()); err != nil {
				return nil, err
			}
		}
		runContext = context.WithValue(runContext, brokerContextKey{}, broker)
	}
	payload, err = callExecute(runContext, module, request, engine.config.MaxResponseBytes)
	if err != nil {
		return nil, withGuestDiagnostic(err, stderr.String())
	}
	if broker != nil {
		if err := broker.Finalize(true); err != nil {
			return nil, fmt.Errorf("finalize capability broker: %w", err)
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
	ErrGuestClaimedExecutionRef    = errors.New("Guest response claimed Host execution reference")
	ErrGuestClaimedCapabilityPlan  = errors.New("Guest response claimed Host capability plan")
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
		case "status", "result", "receipts", "metrics", "error":
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

func callSourceValidation(ctx context.Context, module api.Module, request []byte) error {
	results, release, err := callWithBytes(ctx, module, "runtime_validate_source", request)
	if release != nil {
		defer release()
	}
	if err != nil {
		return err
	}
	if len(results) != 1 {
		return errors.New("runtime_validate_source returned an unexpected result count")
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

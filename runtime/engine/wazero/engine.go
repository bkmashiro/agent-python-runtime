package wazero

import (
	"context"
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

// Factory constructs wazero-backed runners behind the neutral engine contract.
type Factory struct {
	BrokerFactory    BrokerFactory
	Observer         Observer
	Strategy         enginecontract.ExecutionStrategy
	PreparedCapacity uint32
	CompilationCache *CompilationCache
}

func (Factory) Name() string { return "wazero" }

func (factory Factory) New(ctx context.Context, wasm []byte, config runtimeconfig.RunConfig) (enginecontract.Runner, error) {
	if factory.PreparedCapacity > maxPreparedCapacity {
		return nil, fmt.Errorf("prepared capacity %d exceeds hard bound %d", factory.PreparedCapacity, maxPreparedCapacity)
	}
	return newEngine(ctx, wasm, config, factory.BrokerFactory, factory.Observer, factory.Strategy, factory.PreparedCapacity, factory.CompilationCache)
}

var _ enginecontract.Factory = Factory{}
var _ enginecontract.Runner = (*Engine)(nil)

// Engine owns a compiled guest module. V1 either initializes synchronously per
// Run or checks out a never-served, single-use initialized module; every served
// module is closed instead of restored or returned to a pool.
type Engine struct {
	runtime       wazerort.Runtime
	compiled      wazerort.CompiledModule
	config        runtimeconfig.RunConfig
	brokerFactory BrokerFactory
	observer      Observer
	strategy      enginecontract.ExecutionStrategy
	stateCensus   ReactorStateCensus
	pool          *preparedPool
}

func New(ctx context.Context, wasm []byte, config runtimeconfig.RunConfig) (*Engine, error) {
	return newEngine(ctx, wasm, config, nil, nil, "", 0, nil)
}

func NewWithBrokerFactory(
	ctx context.Context,
	wasm []byte,
	config runtimeconfig.RunConfig,
	brokerFactory BrokerFactory,
) (*Engine, error) {
	return newEngine(ctx, wasm, config, brokerFactory, nil, "", 0, nil)
}

func newEngine(
	ctx context.Context,
	wasm []byte,
	config runtimeconfig.RunConfig,
	brokerFactory BrokerFactory,
	observer Observer,
	requestedStrategy enginecontract.ExecutionStrategy,
	preparedCapacity uint32,
	compilationCache *CompilationCache,
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
	compiled, err := wasmRuntime.CompileModule(ctx, wasm)
	observe(observer, "compile", compileStarted, err)
	if releaseCompilationCache != nil {
		releaseCompilationCache()
		releaseCompilationCache = nil
	}
	if err != nil {
		_ = wasmRuntime.Close(ctx)
		return nil, fmt.Errorf("compile guest: %w", err)
	}
	engine := &Engine{
		runtime:       wasmRuntime,
		compiled:      compiled,
		config:        config,
		brokerFactory: brokerFactory,
		observer:      observer,
		strategy:      strategy,
		stateCensus:   censusCompiledReactor(compiled),
	}
	engine.initializePreparedPool(preparedCapacity)
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
	case enginecontract.StrategyCOWReadySingleUse, enginecontract.StrategyCOWFullRemapRestore,
		enginecontract.StrategyCOWLocality, enginecontract.StrategyCOWAdaptiveReset:
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
	return engine.runtime.Close(ctx)
}

func (engine *Engine) Run(ctx context.Context, request []byte, trustedPrepare string) (payload []byte, runErr error) {
	if len(request) == 0 || uint64(len(request)) > uint64(engine.config.MaxRequestBytes) {
		return nil, errors.New("request exceeds configured bounds")
	}
	runRequest, err := runtimeconfig.DecodeRunRequest(request)
	if err != nil {
		return nil, err
	}

	runContext, cancel := context.WithTimeout(ctx, engine.config.Timeout)
	defer cancel()
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
	defer module.Close(context.Background())

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
	if broker != nil {
		payload, err = mergeHostEvidence(payload, broker.Receipts(), broker.CallCount(), engine.config.MaxResponseBytes)
		if err != nil {
			return nil, err
		}
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

func mergeHostEvidence(
	payload []byte,
	receipts []receipt.Receipt,
	capabilityCalls uint32,
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
		default:
			return nil, fmt.Errorf("guest response contains non-canonical field %q", key)
		}
	}
	if receipts == nil {
		receipts = []receipt.Receipt{}
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

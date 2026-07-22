package wazero

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazerort "github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// Factory constructs wazero-backed runners behind the neutral engine contract.
type Factory struct{}

func (Factory) Name() string { return "wazero" }

func (Factory) New(ctx context.Context, wasm []byte, config runtimeconfig.RunConfig) (enginecontract.Runner, error) {
	return New(ctx, wasm, config)
}

var _ enginecontract.Factory = Factory{}
var _ enginecontract.Runner = (*Engine)(nil)

// Engine owns a compiled guest module. V1 creates a fresh module instance for
// each Run; pooling and snapshot restoration are intentionally deferred.
type Engine struct {
	runtime  wazerort.Runtime
	compiled wazerort.CompiledModule
	config   runtimeconfig.RunConfig
}

func New(ctx context.Context, wasm []byte, config runtimeconfig.RunConfig) (*Engine, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid run config: %w", err)
	}
	if len(wasm) < 8 {
		return nil, errors.New("guest module is too short")
	}

	runtimeConfig := wazerort.NewRuntimeConfig().
		WithCloseOnContextDone(true).
		WithMemoryLimitPages(config.MemoryLimitPages)
	wasmRuntime := wazerort.NewRuntimeWithConfig(ctx, runtimeConfig)
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, wasmRuntime); err != nil {
		_ = wasmRuntime.Close(ctx)
		return nil, fmt.Errorf("instantiate WASI imports: %w", err)
	}
	compiled, err := wasmRuntime.CompileModule(ctx, wasm)
	if err != nil {
		_ = wasmRuntime.Close(ctx)
		return nil, fmt.Errorf("compile guest: %w", err)
	}
	return &Engine{runtime: wasmRuntime, compiled: compiled, config: config}, nil
}

func (engine *Engine) Properties() enginecontract.Properties {
	return enginecontract.Properties{
		Backend:   "wazero",
		ResetMode: enginecontract.ResetModeFreshInstance,
	}
}

func (engine *Engine) Close(ctx context.Context) error {
	if engine == nil || engine.runtime == nil {
		return nil
	}
	return engine.runtime.Close(ctx)
}

func (engine *Engine) Run(ctx context.Context, request []byte, trustedPrepare string) ([]byte, error) {
	if len(request) == 0 || uint64(len(request)) > uint64(engine.config.MaxRequestBytes) {
		return nil, errors.New("request exceeds configured bounds")
	}
	if _, err := runtimeconfig.DecodeRunRequest(request); err != nil {
		return nil, err
	}

	runContext, cancel := context.WithTimeout(ctx, engine.config.Timeout)
	defer cancel()
	var guestStderr bytes.Buffer
	module, err := engine.runtime.InstantiateModule(
		runContext,
		engine.compiled,
		wazerort.NewModuleConfig().WithName("").WithStderr(&guestStderr),
	)
	if err != nil {
		return nil, fmt.Errorf("instantiate guest: %w", err)
	}
	defer module.Close(context.Background())

	if err := callNoArgs(runContext, module, "_initialize"); err != nil {
		return nil, withGuestDiagnostic(err, guestStderr.String())
	}
	if err := callStatusWithBytes(runContext, module, "runtime_init", []byte("{}")); err != nil {
		return nil, withGuestDiagnostic(err, guestStderr.String())
	}
	if trustedPrepare != "" {
		if err := callStatusWithBytes(runContext, module, "runtime_prepare", []byte(trustedPrepare)); err != nil {
			return nil, withGuestDiagnostic(err, guestStderr.String())
		}
	}
	payload, err := callExecute(runContext, module, request, engine.config.MaxResponseBytes)
	if err != nil {
		return nil, withGuestDiagnostic(err, guestStderr.String())
	}
	return payload, nil
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

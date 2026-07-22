package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

type importRecord struct {
	Kind   string `json:"kind"`
	Module string `json:"module"`
	Name   string `json:"name"`
}

type report struct {
	SchemaVersion       int            `json:"schema_version"`
	Backend             string         `json:"backend"`
	Outcome             string         `json:"outcome"`
	WasmSHA256          string         `json:"wasm_sha256"`
	WasmSize            int            `json:"wasm_size"`
	Imports             []importRecord `json:"imports"`
	Exports             []string       `json:"exports"`
	CInitializeCalled   bool           `json:"c_initialize_called"`
	RegistrationCalled  bool           `json:"registration_called"`
	RegistrationExit    *uint64        `json:"registration_exit"`
	PythonInitialized   bool           `json:"python_initialized"`
	InitializerExecuted bool           `json:"initializer_executed"`
	ModuleImported      bool           `json:"module_imported"`
	Error               string         `json:"error,omitempty"`
}

func main() {
	wasmPath := flag.String("wasm", "", "path to the link-probe Wasm")
	outputPath := flag.String("output", "", "path for the JSON report")
	flag.Parse()
	if *wasmPath == "" || *outputPath == "" {
		fmt.Fprintln(os.Stderr, "-wasm and -output are required")
		os.Exit(2)
	}

	wasm, err := os.ReadFile(*wasmPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read Wasm: %v\n", err)
		os.Exit(2)
	}
	digest := sha256.Sum256(wasm)
	result := report{
		SchemaVersion: 1,
		Backend:       "wazero",
		Outcome:       "registration_failed",
		WasmSHA256:    hex.EncodeToString(digest[:]),
		WasmSize:      len(wasm),
		Imports:       []importRecord{},
		Exports:       []string{},
	}
	if err := runProbe(context.Background(), wasm, &result); err != nil {
		result.Error = boundedError(err)
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "encode report: %v\n", err)
		os.Exit(2)
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(*outputPath, encoded, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write report: %v\n", err)
		os.Exit(2)
	}
	_, _ = os.Stdout.Write(encoded)
}

func runProbe(ctx context.Context, wasm []byte, result *report) error {
	runtimeConfig := wazero.NewRuntimeConfig().
		WithCloseOnContextDone(true).
		WithMemoryLimitPages(8192)
	runtime := wazero.NewRuntimeWithConfig(ctx, runtimeConfig)
	defer runtime.Close(ctx)
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, runtime); err != nil {
		return fmt.Errorf("instantiate WASI: %w", err)
	}
	compiled, err := runtime.CompileModule(ctx, wasm)
	if err != nil {
		return fmt.Errorf("compile module: %w", err)
	}
	defer compiled.Close(ctx)
	for _, definition := range compiled.ImportedFunctions() {
		module, name, _ := definition.Import()
		result.Imports = append(result.Imports, importRecord{Kind: "function", Module: module, Name: name})
	}
	for _, definition := range compiled.ImportedMemories() {
		module, name, _ := definition.Import()
		result.Imports = append(result.Imports, importRecord{Kind: "memory", Module: module, Name: name})
	}
	for name := range compiled.ExportedFunctions() {
		result.Exports = append(result.Exports, name)
	}
	for name := range compiled.ExportedMemories() {
		result.Exports = append(result.Exports, name)
	}

	stderr := &bytes.Buffer{}
	module, err := runtime.InstantiateModule(
		ctx,
		compiled,
		wazero.NewModuleConfig().WithName("").WithStderr(stderr),
	)
	if err != nil {
		return fmt.Errorf("instantiate module: %w", err)
	}
	defer module.Close(ctx)

	initialize := module.ExportedFunction("_initialize")
	if initialize == nil {
		return errors.New("missing _initialize export")
	}
	if _, err := initialize.Call(ctx); err != nil {
		return fmt.Errorf("call _initialize: %w", err)
	}
	result.CInitializeCalled = true

	register := module.ExportedFunction("numpy_register_probe")
	if register == nil {
		return errors.New("missing numpy_register_probe export")
	}
	values, err := register.Call(ctx)
	result.RegistrationCalled = true
	if err != nil {
		return fmt.Errorf("call numpy_register_probe: %w", err)
	}
	if len(values) != 1 {
		return fmt.Errorf("registration returned %d values, want 1", len(values))
	}
	registrationExit := values[0]
	result.RegistrationExit = &registrationExit
	if registrationExit != 0 {
		return fmt.Errorf("PyImport_AppendInittab returned %d", registrationExit)
	}
	result.Outcome = "registration_succeeded"
	return nil
}

func boundedError(err error) string {
	const limit = 1000
	text := err.Error()
	if len(text) <= limit {
		return text
	}
	return text[:limit]
}

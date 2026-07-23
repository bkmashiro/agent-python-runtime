package main

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

type importRecord struct {
	Kind   string `json:"kind"`
	Module string `json:"module"`
	Name   string `json:"name"`
}

type report struct {
	SchemaVersion        int            `json:"schema_version"`
	FeatureProfile       string         `json:"feature_profile"`
	Backend              string         `json:"backend"`
	Outcome              string         `json:"outcome"`
	WasmSHA256           string         `json:"wasm_sha256"`
	WasmSize             int            `json:"wasm_size"`
	Imports              []importRecord `json:"imports"`
	Exports              []string       `json:"exports"`
	CInitializeCalled    bool           `json:"c_initialize_called"`
	RegistrationCalled   bool           `json:"registration_called"`
	RegistrationExit     *uint64        `json:"registration_exit"`
	ImportCalled         bool           `json:"import_called"`
	ImportExit           *uint64        `json:"import_exit"`
	PythonInitialized    bool           `json:"python_initialized"`
	InitializerExecution string         `json:"initializer_execution"`
	ModuleImported       bool           `json:"module_imported"`
	NumericCalled        bool           `json:"numeric_called"`
	NumericExit          *uint64        `json:"numeric_exit"`
	NumericValidated     bool           `json:"numeric_validated"`
	RandomCalled         bool           `json:"random_called"`
	RandomExit           *uint64        `json:"random_exit"`
	RandomValidated      bool           `json:"random_validated"`
	EntropySource        string         `json:"entropy_source"`
	EntropyCalled        bool           `json:"entropy_called"`
	EntropyExit          *uint64        `json:"entropy_exit"`
	EntropyValidated     bool           `json:"entropy_validated"`
	GuestStderr          string         `json:"guest_stderr,omitempty"`
	Error                string         `json:"error,omitempty"`
}

func main() {
	wasmPath := flag.String("wasm", "", "path to the link-probe Wasm")
	outputPath := flag.String("output", "", "path for the JSON report")
	profile := flag.String("profile", "", "resolved feature profile (core or random)")
	flag.Parse()
	if *wasmPath == "" || *outputPath == "" || (*profile != "core" && *profile != "random") {
		fmt.Fprintln(os.Stderr, "-wasm, -output, and -profile={core|random} are required")
		os.Exit(2)
	}

	wasm, err := os.ReadFile(*wasmPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read Wasm: %v\n", err)
		os.Exit(2)
	}
	digest := sha256.Sum256(wasm)
	result := report{
		SchemaVersion:        5,
		FeatureProfile:       *profile,
		Backend:              "wazero",
		EntropySource:        "host_crypto_rand",
		Outcome:              "registration_failed",
		WasmSHA256:           hex.EncodeToString(digest[:]),
		WasmSize:             len(wasm),
		Imports:              []importRecord{},
		Exports:              []string{},
		InitializerExecution: "not_attempted",
	}
	if err := runProbe(context.Background(), wasm, *profile, &result); err != nil {
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

func runProbe(ctx context.Context, wasm []byte, profile string, result *report) error {
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
	sort.Strings(result.Exports)

	stderr := &bytes.Buffer{}
	defer func() {
		result.GuestStderr = boundedText(stderr.String())
	}()
	module, err := runtime.InstantiateModule(
		ctx,
		compiled,
		wazero.NewModuleConfig().WithName("").WithStderr(stderr).WithRandSource(cryptorand.Reader),
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
	result.Outcome = "import_failed"

	importProbe := module.ExportedFunction("numpy_import_probe")
	if importProbe == nil {
		return errors.New("missing numpy_import_probe export")
	}
	values, err = importProbe.Call(ctx)
	result.ImportCalled = true
	result.InitializerExecution = "attempted_or_unknown"
	if err != nil {
		return fmt.Errorf("call numpy_import_probe: %w", err)
	}
	if len(values) != 1 {
		return fmt.Errorf("import returned %d values, want 1", len(values))
	}
	importExit := values[0]
	result.ImportExit = &importExit

	initialized := module.ExportedFunction("numpy_python_initialized_probe")
	if initialized == nil {
		return errors.New("missing numpy_python_initialized_probe export")
	}
	initializedValues, err := initialized.Call(ctx)
	if err != nil || len(initializedValues) != 1 {
		return fmt.Errorf("read Python initialization state: values=%d err=%v", len(initializedValues), err)
	}
	result.PythonInitialized = initializedValues[0] != 0
	if importExit != 0 {
		return fmt.Errorf("numpy_import_probe returned %d", importExit)
	}
	result.InitializerExecution = "succeeded"
	result.ModuleImported = true
	result.Outcome = "numeric_failed"

	numericProbe := module.ExportedFunction("numpy_numeric_probe")
	if numericProbe == nil {
		return errors.New("missing numpy_numeric_probe export")
	}
	values, err = numericProbe.Call(ctx)
	result.NumericCalled = true
	if err != nil {
		return fmt.Errorf("call numpy_numeric_probe: %w", err)
	}
	if len(values) != 1 {
		return fmt.Errorf("numeric probe returned %d values, want 1", len(values))
	}
	numericExit := values[0]
	result.NumericExit = &numericExit
	if numericExit != 0 {
		return fmt.Errorf("numpy_numeric_probe returned %d", numericExit)
	}
	result.NumericValidated = true
	result.Outcome = "numeric_succeeded"
	if profile != "random" {
		return nil
	}

	result.Outcome = "random_failed"
	randomProbe := module.ExportedFunction("numpy_random_probe")
	if randomProbe == nil {
		return errors.New("missing numpy_random_probe export")
	}
	values, err = randomProbe.Call(ctx)
	result.RandomCalled = true
	if err != nil {
		return fmt.Errorf("call numpy_random_probe: %w", err)
	}
	if len(values) != 1 {
		return fmt.Errorf("random probe returned %d values, want 1", len(values))
	}
	randomExit := values[0]
	result.RandomExit = &randomExit
	if randomExit != 0 {
		return fmt.Errorf("numpy_random_probe returned %d", randomExit)
	}
	result.RandomValidated = true
	result.Outcome = "random_succeeded"

	result.Outcome = "entropy_failed"
	entropyProbe := module.ExportedFunction("numpy_entropy_probe")
	if entropyProbe == nil {
		return errors.New("missing numpy_entropy_probe export")
	}
	values, err = entropyProbe.Call(ctx)
	result.EntropyCalled = true
	if err != nil {
		return fmt.Errorf("call numpy_entropy_probe: %w", err)
	}
	if len(values) != 1 {
		return fmt.Errorf("entropy probe returned %d values, want 1", len(values))
	}
	entropyExit := values[0]
	result.EntropyExit = &entropyExit
	if entropyExit != 0 {
		return fmt.Errorf("numpy_entropy_probe returned %d", entropyExit)
	}
	result.EntropyValidated = true
	result.Outcome = "entropy_succeeded"
	return nil
}

func boundedText(text string) string {
	const limit = 8192
	if len(text) <= limit {
		return text
	}
	return text[:limit]
}

func boundedError(err error) string {
	return boundedText(err.Error())
}

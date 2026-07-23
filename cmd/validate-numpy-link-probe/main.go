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
	goruntime "runtime"
	"sort"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

type importRecord struct {
	Kind   string `json:"kind"`
	Module string `json:"module"`
	Name   string `json:"name"`
}

type instanceEvidence struct {
	Index                  int     `json:"index"`
	FullValidation         bool    `json:"full_validation"`
	InstantiateDurationNS  int64   `json:"instantiate_duration_ns"`
	InitializeDurationNS   int64   `json:"initialize_duration_ns"`
	RegistrationDurationNS int64   `json:"registration_duration_ns"`
	ImportDurationNS       int64   `json:"import_duration_ns"`
	NumericDurationNS      int64   `json:"numeric_duration_ns,omitempty"`
	RandomDurationNS       int64   `json:"random_duration_ns,omitempty"`
	EntropyDurationNS      int64   `json:"entropy_duration_ns,omitempty"`
	FreshnessDurationNS    int64   `json:"freshness_duration_ns"`
	MemoryBytes            uint64  `json:"memory_bytes"`
	FreshnessExit          *uint64 `json:"freshness_exit"`
	EntropyWord            uint64  `json:"entropy_word"`
	GuestStderr            string  `json:"guest_stderr,omitempty"`
}

type report struct {
	SchemaVersion           int                `json:"schema_version"`
	FeatureProfile          string             `json:"feature_profile"`
	Backend                 string             `json:"backend"`
	Outcome                 string             `json:"outcome"`
	WasmSHA256              string             `json:"wasm_sha256"`
	WasmSize                int                `json:"wasm_size"`
	Imports                 []importRecord     `json:"imports"`
	Exports                 []string           `json:"exports"`
	CompileDurationNS       int64              `json:"compile_duration_ns"`
	RuntimeDurationNS       int64              `json:"runtime_duration_ns"`
	HostHeapAllocBytes      uint64             `json:"host_heap_alloc_bytes"`
	HostHeapSysBytes        uint64             `json:"host_heap_sys_bytes"`
	HostTotalAllocBytes     uint64             `json:"host_total_alloc_bytes"`
	FreshInstances          []instanceEvidence `json:"fresh_instances"`
	FreshInstancesValidated bool               `json:"fresh_instances_validated"`
	CInitializeCalled       bool               `json:"c_initialize_called"`
	RegistrationCalled      bool               `json:"registration_called"`
	RegistrationExit        *uint64            `json:"registration_exit"`
	ImportCalled            bool               `json:"import_called"`
	ImportExit              *uint64            `json:"import_exit"`
	PythonInitialized       bool               `json:"python_initialized"`
	InitializerExecution    string             `json:"initializer_execution"`
	ModuleImported          bool               `json:"module_imported"`
	NumericCalled           bool               `json:"numeric_called"`
	NumericExit             *uint64            `json:"numeric_exit"`
	NumericValidated        bool               `json:"numeric_validated"`
	RandomCalled            bool               `json:"random_called"`
	RandomExit              *uint64            `json:"random_exit"`
	RandomValidated         bool               `json:"random_validated"`
	EntropySource           string             `json:"entropy_source"`
	EntropyCalled           bool               `json:"entropy_called"`
	EntropyExit             *uint64            `json:"entropy_exit"`
	EntropyValidated        bool               `json:"entropy_validated"`
	GuestStderr             string             `json:"guest_stderr,omitempty"`
	Error                   string             `json:"error,omitempty"`
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
		SchemaVersion:        6,
		FeatureProfile:       *profile,
		Backend:              "wazero",
		EntropySource:        "host_crypto_rand",
		Outcome:              "registration_failed",
		WasmSHA256:           hex.EncodeToString(digest[:]),
		WasmSize:             len(wasm),
		Imports:              []importRecord{},
		Exports:              []string{},
		FreshInstances:       []instanceEvidence{},
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

func captureHostHeap(result *report) {
	var stats goruntime.MemStats
	goruntime.ReadMemStats(&stats)
	result.HostHeapAllocBytes = stats.HeapAlloc
	result.HostHeapSysBytes = stats.HeapSys
	result.HostTotalAllocBytes = stats.TotalAlloc
}

func callSingleResult(ctx context.Context, module api.Module, name string) (uint64, int64, error) {
	function := module.ExportedFunction(name)
	if function == nil {
		return 0, 0, fmt.Errorf("missing %s export", name)
	}
	started := time.Now()
	values, err := function.Call(ctx)
	duration := time.Since(started).Nanoseconds()
	if err != nil {
		return 0, duration, fmt.Errorf("call %s: %w", name, err)
	}
	if len(values) != 1 {
		return 0, duration, fmt.Errorf("%s returned %d values, want 1", name, len(values))
	}
	return values[0], duration, nil
}

func runProbe(ctx context.Context, wasm []byte, profile string, result *report) error {
	runtimeStarted := time.Now()
	defer func() { result.RuntimeDurationNS = time.Since(runtimeStarted).Nanoseconds() }()
	runtimeConfig := wazero.NewRuntimeConfig().
		WithCloseOnContextDone(true).
		WithMemoryLimitPages(8192)
	runtime := wazero.NewRuntimeWithConfig(ctx, runtimeConfig)
	defer runtime.Close(ctx)
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, runtime); err != nil {
		return fmt.Errorf("instantiate WASI: %w", err)
	}
	compileStarted := time.Now()
	compiled, err := runtime.CompileModule(ctx, wasm)
	result.CompileDurationNS = time.Since(compileStarted).Nanoseconds()
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
	firstEvidence := instanceEvidence{Index: 1, FullValidation: true}
	instantiateStarted := time.Now()
	module, err := runtime.InstantiateModule(
		ctx,
		compiled,
		wazero.NewModuleConfig().WithName("").WithStderr(stderr).WithRandSource(cryptorand.Reader),
	)
	if err != nil {
		return fmt.Errorf("instantiate module: %w", err)
	}
	firstEvidence.InstantiateDurationNS = time.Since(instantiateStarted).Nanoseconds()
	result.FreshInstances = append(result.FreshInstances, firstEvidence)
	defer module.Close(ctx)
	defer func() {
		result.FreshInstances[0].MemoryBytes = uint64(module.Memory().Size())
		result.FreshInstances[0].GuestStderr = boundedText(stderr.String())
	}()

	initialize := module.ExportedFunction("_initialize")
	if initialize == nil {
		return errors.New("missing _initialize export")
	}
	initializeStarted := time.Now()
	if _, err := initialize.Call(ctx); err != nil {
		return fmt.Errorf("call _initialize: %w", err)
	}
	result.FreshInstances[0].InitializeDurationNS = time.Since(initializeStarted).Nanoseconds()
	result.CInitializeCalled = true

	register := module.ExportedFunction("numpy_register_probe")
	if register == nil {
		return errors.New("missing numpy_register_probe export")
	}
	registrationStarted := time.Now()
	values, err := register.Call(ctx)
	result.FreshInstances[0].RegistrationDurationNS = time.Since(registrationStarted).Nanoseconds()
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
	importStarted := time.Now()
	values, err = importProbe.Call(ctx)
	result.FreshInstances[0].ImportDurationNS = time.Since(importStarted).Nanoseconds()
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
	numericStarted := time.Now()
	values, err = numericProbe.Call(ctx)
	result.FreshInstances[0].NumericDurationNS = time.Since(numericStarted).Nanoseconds()
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
	randomStarted := time.Now()
	values, err = randomProbe.Call(ctx)
	result.FreshInstances[0].RandomDurationNS = time.Since(randomStarted).Nanoseconds()
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
	entropyStarted := time.Now()
	values, err = entropyProbe.Call(ctx)
	result.FreshInstances[0].EntropyDurationNS = time.Since(entropyStarted).Nanoseconds()
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

	result.Outcome = "freshness_failed"
	firstFreshExit, firstFreshDuration, err := callSingleResult(ctx, module, "numpy_freshness_probe")
	result.FreshInstances[0].FreshnessDurationNS = firstFreshDuration
	result.FreshInstances[0].FreshnessExit = &firstFreshExit
	if err != nil {
		return err
	}
	if firstFreshExit != 0 {
		return fmt.Errorf("first numpy_freshness_probe returned %d", firstFreshExit)
	}
	firstWord, _, err := callSingleResult(ctx, module, "numpy_freshness_word_probe")
	if err != nil {
		return err
	}
	result.FreshInstances[0].EntropyWord = firstWord

	secondStderr := &bytes.Buffer{}
	second := instanceEvidence{Index: 2, FullValidation: false}
	secondInstantiateStarted := time.Now()
	secondModule, err := runtime.InstantiateModule(
		ctx,
		compiled,
		wazero.NewModuleConfig().WithName("").WithStderr(secondStderr).WithRandSource(cryptorand.Reader),
	)
	second.InstantiateDurationNS = time.Since(secondInstantiateStarted).Nanoseconds()
	result.FreshInstances = append(result.FreshInstances, second)
	if err != nil {
		result.FreshInstances[1].GuestStderr = boundedText(secondStderr.String())
		return fmt.Errorf("instantiate second fresh module: %w", err)
	}
	defer secondModule.Close(ctx)
	defer func() {
		result.FreshInstances[1].MemoryBytes = uint64(secondModule.Memory().Size())
		result.FreshInstances[1].GuestStderr = boundedText(secondStderr.String())
	}()

	secondInitialize := secondModule.ExportedFunction("_initialize")
	if secondInitialize == nil {
		return errors.New("second fresh module missing _initialize export")
	}
	secondInitializeStarted := time.Now()
	if _, err := secondInitialize.Call(ctx); err != nil {
		return fmt.Errorf("initialize second fresh module: %w", err)
	}
	result.FreshInstances[1].InitializeDurationNS = time.Since(secondInitializeStarted).Nanoseconds()

	secondRegistrationExit, secondRegistrationDuration, err := callSingleResult(ctx, secondModule, "numpy_register_probe")
	result.FreshInstances[1].RegistrationDurationNS = secondRegistrationDuration
	if err != nil {
		return err
	}
	if secondRegistrationExit != 0 {
		return fmt.Errorf("second numpy_register_probe returned %d", secondRegistrationExit)
	}

	secondImportExit, secondImportDuration, err := callSingleResult(ctx, secondModule, "numpy_import_probe")
	result.FreshInstances[1].ImportDurationNS = secondImportDuration
	if err != nil {
		return err
	}
	if secondImportExit != 0 {
		return fmt.Errorf("second numpy_import_probe returned %d", secondImportExit)
	}

	secondFreshExit, secondFreshDuration, err := callSingleResult(ctx, secondModule, "numpy_freshness_probe")
	result.FreshInstances[1].FreshnessDurationNS = secondFreshDuration
	result.FreshInstances[1].FreshnessExit = &secondFreshExit
	if err != nil {
		return err
	}
	if secondFreshExit != 0 {
		return fmt.Errorf("second numpy_freshness_probe returned %d", secondFreshExit)
	}
	secondWord, _, err := callSingleResult(ctx, secondModule, "numpy_freshness_word_probe")
	if err != nil {
		return err
	}
	result.FreshInstances[1].EntropyWord = secondWord
	if firstWord == secondWord {
		return fmt.Errorf("fresh instances produced identical unseeded random word %d", firstWord)
	}
	captureHostHeap(result)
	result.FreshInstancesValidated = true
	result.Outcome = "freshness_succeeded"
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

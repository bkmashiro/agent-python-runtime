package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/numpyproducer"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

type artifactBundle struct {
	wasm    []byte
	profile runtimeconfig.ExecutionProfile
}

type engineRunner struct {
	ctx        context.Context
	bundle     artifactBundle
	warm       bool
	linuxCOW   bool
	persistent *wazeroengine.Engine
}

type callTiming struct {
	ProvisionNanos uint64
	ExecutionNanos uint64
	GuestNanos     uint64
	COWSelected    bool
	Fallback       bool
	COWRequired    bool
}

type exactAnalysis struct {
	Verified semantic.VerifiedAnalysis
	Analysis semantic.Analysis
}

type guestEnvelope struct {
	Status        string          `json:"status"`
	Error         json.RawMessage `json:"error"`
	Result        json.RawMessage `json:"result"`
	ResultPresent bool            `json:"result_present"`
	Metrics       struct {
		CapabilityCalls uint64  `json:"capability_calls"`
		GuestTimeMS     float64 `json:"guest_time_ms"`
	} `json:"metrics"`
	SourceContract struct {
		ModelSourceSHA256 string `json:"model_source_sha256"`
	} `json:"source_contract"`
}

func newEngineRunner(ctx context.Context, bundle artifactBundle, warm, linuxCOW bool) (*engineRunner, error) {
	runner := &engineRunner{ctx: ctx, bundle: bundle, warm: warm, linuxCOW: linuxCOW}
	if warm {
		engine, err := runner.buildEngine()
		if err != nil {
			return nil, err
		}
		if linuxCOW {
			if err := engine.PrepareSemanticRuntime(ctx); err != nil {
				_ = engine.Close(ctx)
				return nil, err
			}
			probe := engine.COWProbe()
			if !probe.COWSelected || probe.Fallback || !probe.MemoryCOWCandidate {
				_ = engine.Close(ctx)
				return nil, errors.New("private COW unavailable")
			}
		}
		runner.persistent = engine
	}
	return runner, nil
}

func (runner *engineRunner) Close() error {
	if runner.persistent == nil {
		return nil
	}
	return runner.persistent.Close(runner.ctx)
}

func (runner *engineRunner) buildEngine() (*wazeroengine.Engine, error) {
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = 5 * time.Minute
	config.MaxRequestBytes = 16 * 1024 * 1024
	config.MaxResponseBytes = 16 * 1024 * 1024
	config.MemoryLimitPages = 16384
	config.ExecutionProfile = &runner.bundle.profile
	config.Mechanisms.SemanticAnalysis = true
	if runner.linuxCOW {
		config.Mechanisms.PreparedRuntime = true
		config.Mechanisms.MemoryCOW = true
	}
	return wazeroengine.New(runner.ctx, runner.bundle.wasm, config)
}

func (runner *engineRunner) acquire() (*wazeroengine.Engine, uint64, bool, error) {
	if runner.persistent != nil {
		return runner.persistent, 0, false, nil
	}
	started := time.Now()
	engine, err := runner.buildEngine()
	return engine, uint64(time.Since(started)), true, err
}

func (runner *engineRunner) Run(request []byte) ([]byte, guestEnvelope, callTiming, error) {
	engine, provision, closeAfter, err := runner.acquire()
	if err != nil {
		return nil, guestEnvelope{}, callTiming{}, err
	}
	if closeAfter {
		defer engine.Close(runner.ctx)
	}
	started := time.Now()
	response, err := engine.Run(runner.ctx, request, "")
	timing := callTiming{ProvisionNanos: provision, ExecutionNanos: uint64(time.Since(started)), COWRequired: runner.linuxCOW}
	probe := engine.COWProbe()
	timing.COWSelected, timing.Fallback = probe.COWSelected, probe.Fallback
	if err != nil {
		return nil, guestEnvelope{}, timing, err
	}
	var envelope guestEnvelope
	if json.Unmarshal(response, &envelope) != nil || envelope.Status != "ok" || !envelope.ResultPresent || envelope.Metrics.CapabilityCalls != 0 {
		return response, envelope, timing, errors.New("Guest response rejected")
	}
	if envelope.Metrics.GuestTimeMS > 0 {
		timing.GuestNanos = uint64(envelope.Metrics.GuestTimeMS * 1e6)
	}
	return response, envelope, timing, nil
}

func (runner *engineRunner) RunProducer(request []byte, admission numpyproducer.Admission) (numpyproducer.VerifiedExecution, guestEnvelope, callTiming, error) {
	engine, provision, closeAfter, err := runner.acquire()
	if err != nil {
		return numpyproducer.VerifiedExecution{}, guestEnvelope{}, callTiming{}, err
	}
	if closeAfter {
		defer engine.Close(runner.ctx)
	}
	started := time.Now()
	execution, err := numpyproducer.RunVerifiedExecution(runner.ctx, engine, request, "", admission)
	timing := callTiming{ProvisionNanos: provision, ExecutionNanos: uint64(time.Since(started)), COWRequired: runner.linuxCOW}
	probe := engine.COWProbe()
	timing.COWSelected, timing.Fallback = probe.COWSelected, probe.Fallback
	if err != nil {
		return numpyproducer.VerifiedExecution{}, guestEnvelope{}, timing, err
	}
	response, err := execution.Response()
	if err != nil {
		return numpyproducer.VerifiedExecution{}, guestEnvelope{}, timing, err
	}
	var envelope guestEnvelope
	if json.Unmarshal(response, &envelope) != nil || envelope.Status != "ok" || !envelope.ResultPresent || envelope.Metrics.CapabilityCalls != 0 {
		return numpyproducer.VerifiedExecution{}, envelope, timing, errors.New("Guest response rejected")
	}
	if envelope.Metrics.GuestTimeMS > 0 {
		timing.GuestNanos = uint64(envelope.Metrics.GuestTimeMS * 1e6)
	}
	return execution, envelope, timing, nil
}

func (runner *engineRunner) Analyze(source string, bindings numpyproducer.Bindings) (exactAnalysis, callTiming, error) {
	engine, provision, closeAfter, err := runner.acquire()
	if err != nil {
		return exactAnalysis{}, callTiming{}, err
	}
	if closeAfter {
		defer engine.Close(runner.ctx)
	}
	request, err := semantic.NewRequest(source, semantic.Bindings{
		ArtifactSHA256: bindings.ArtifactSHA256, ExecutionProfileSHA256: bindings.ExecutionProfileSHA256,
		ImportClosureSHA256: bindings.ImportClosureSHA256, CapabilityPlanSHA256: bindings.CapabilityPlanSHA256,
	}, nil)
	if err != nil {
		return exactAnalysis{}, callTiming{}, err
	}
	started := time.Now()
	verified, err := semantic.AnalyzeVerified(runner.ctx, engine, request)
	analysis, analysisErr := verified.Analysis()
	if err == nil {
		err = analysisErr
	}
	timing := callTiming{ProvisionNanos: provision, ExecutionNanos: uint64(time.Since(started)), COWRequired: runner.linuxCOW}
	probe := engine.COWProbe()
	timing.COWSelected, timing.Fallback = probe.COWSelected, probe.Fallback
	return exactAnalysis{Verified: verified, Analysis: analysis}, timing, err
}

func loadArtifactBundle(root string) (artifactBundle, error) {
	read := func(name string) ([]byte, error) { return os.ReadFile(filepath.Join(root, "dist", name)) }
	wasm, err := read("agent-python-runtime-numpy-core.wasm")
	if err != nil {
		return artifactBundle{}, err
	}
	manifest, err := read("manifest.json")
	if err != nil {
		return artifactBundle{}, err
	}
	inventory, err := read("import-inventory.json")
	if err != nil {
		return artifactBundle{}, err
	}
	qualification, err := read("import-qualification.json")
	if err != nil {
		return artifactBundle{}, err
	}
	identity, err := runtimeconfig.VerifyDistributionArtifact("agent-python-runtime-numpy-core.wasm", wasm, manifest, inventory, qualification)
	if err != nil {
		return artifactBundle{}, err
	}
	profile, err := runtimeconfig.NewExecutionProfile("numpy-core", []string{"base64", "datetime", "hashlib", "numpy"})
	if err != nil {
		return artifactBundle{}, err
	}
	profile, err = profile.BindVerifiedArtifact(identity)
	return artifactBundle{wasm: wasm, profile: profile}, err
}

func analysisBindings(profile runtimeconfig.ExecutionProfile, linuxCOW bool) (numpyproducer.Bindings, error) {
	config := runtimeconfig.DefaultRunConfig()
	config.ExecutionProfile = &profile
	config.Mechanisms.SemanticAnalysis = true
	if linuxCOW {
		config.Mechanisms.PreparedRuntime = true
		config.Mechanisms.MemoryCOW = true
	}
	profileSHA, err := runtimeconfig.ExecutionProfileBindingSHA256(config)
	if err != nil {
		return numpyproducer.Bindings{}, err
	}
	imports := profile.QualifiedImports()
	sort.Strings(imports)
	importsRaw, _ := json.Marshal(imports)
	return numpyproducer.Bindings{
		ArtifactSHA256: profile.ArtifactSHA256(), ExecutionProfileID: profile.ID(), ExecutionProfileSHA256: profileSHA,
		ImportClosureSHA256: digestBytes(importsRaw), CapabilityPlanSHA256: digestString("pysolate.numpy-reuse.empty-capability-plan.v1"),
	}, nil
}

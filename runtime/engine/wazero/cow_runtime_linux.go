//go:build linux

package wazero

import (
	"context"
	"errors"
	"fmt"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental"
)

type linuxCOWPreparedRuntime struct {
	image                      *cowImage
	trustedPrepareSHA256       string
	parentTrustedPrepareSHA256 string
}

func cowMaximumMemoryBytes(engine *Engine) (uint64, error) {
	if engine == nil || engine.compiled == nil {
		return 0, errors.New("COW maximum requires a compiled module")
	}
	memory, ok := engine.compiled.ExportedMemories()["memory"]
	if !ok {
		return 0, errors.New("COW maximum requires exported memory")
	}
	maximumPages, declared := memory.Max()
	if !declared || maximumPages < memory.Min() {
		return 0, errors.New("COW maximum requires bounded memory")
	}
	return uint64(maximumPages) * wasmLinearPageSize, nil
}

func instantiateCOWModule(ctx context.Context, runtime wazero.Runtime, compiled wazero.CompiledModule, config wazero.ModuleConfig, allocator *cowAllocator) (api.Module, error) {
	module, err := runtime.InstantiateModule(experimental.WithMemoryAllocator(ctx, allocator), compiled, config)
	if err != nil {
		allocator.releaseAllocation()
	}
	return module, err
}

func newCOWPreparedRuntime(ctx context.Context, engine *Engine) (cowPreparedRuntime, error) {
	return newCOWPreparedRuntimeWithTrustedSource(ctx, engine, "", "")
}

func newCOWPreparedRuntimeWithTrustedSource(ctx context.Context, engine *Engine, trustedSource, trustedIdentity string) (cowPreparedRuntime, error) {
	if engine == nil {
		return nil, errors.New("COW prepared runtime requires an engine")
	}
	probe := engine.COWProbe()
	if !probe.MemoryCOWCandidate {
		return nil, errors.New("artifact linear memory is not a bounded private COW candidate")
	}
	canonical, err := engine.newPrepared(ctx)
	if err != nil {
		return nil, fmt.Errorf("prepare canonical COW image: %w", err)
	}
	defer closePreparedInstance(canonical)
	if trustedSource != "" {
		identity, identityErr := trustedCOWPrepareIdentity(trustedSource)
		if identityErr != nil || identity != trustedIdentity {
			return nil, ErrTrustedCOWPrepareBinding
		}
		if err := callStatusWithBytes(ctx, canonical.module, "runtime_prepare", []byte(trustedSource)); err != nil {
			return nil, withGuestDiagnostic(err, canonical.stderr.String())
		}
	}
	return sealCOWPreparedRuntime(engine, canonical, trustedIdentity, "")
}

func (runtime *linuxCOWPreparedRuntime) derive(ctx context.Context, engine *Engine, trustedSource, trustedIdentity string) (cowPreparedRuntime, error) {
	identity, err := trustedCOWDerivedIdentity(trustedSource)
	if err != nil || identity != trustedIdentity {
		return nil, ErrTrustedCOWPrepareBinding
	}
	canonical, _, err := runtime.prepare(ctx, engine)
	if err != nil {
		return nil, fmt.Errorf("clone package COW baseline: %w", err)
	}
	defer closePreparedInstance(canonical)
	if err := callStatusWithBytes(ctx, canonical.module, "runtime_prepare", []byte(trustedSource)); err != nil {
		return nil, withGuestDiagnostic(err, canonical.stderr.String())
	}
	return sealCOWPreparedRuntime(engine, canonical, trustedIdentity, runtime.trustedPrepareSHA256)
}

func sealCOWPreparedRuntime(engine *Engine, canonical *preparedInstance, trustedIdentity, parentIdentity string) (cowPreparedRuntime, error) {
	maximumBytes, err := cowMaximumMemoryBytes(engine)
	if err != nil {
		return nil, err
	}
	memory := canonical.module.Memory()
	if memory == nil || memory.Size() == 0 {
		return nil, errors.New("canonical COW image has no linear memory")
	}
	baseline, ok := memory.Read(0, memory.Size())
	if !ok {
		return nil, errors.New("read canonical COW linear memory")
	}
	image, err := newCOWImageWithMaximum(baseline, maximumBytes)
	if err != nil {
		return nil, err
	}
	return &linuxCOWPreparedRuntime{image: image, trustedPrepareSHA256: trustedIdentity, parentTrustedPrepareSHA256: parentIdentity}, nil
}

func (runtime *linuxCOWPreparedRuntime) prepare(ctx context.Context, engine *Engine) (*preparedInstance, cowCloneLifecycle, error) {
	lifecycle := cowCloneLifecycle{}
	if runtime == nil || runtime.image == nil {
		return nil, lifecycle, errors.New("COW baseline is unavailable")
	}
	stderr := &boundedDiagnostic{}
	stdout := &forbiddenStdout{}
	allocator := runtime.image.newAllocator()
	moduleConfig, temporary, err := engine.moduleConfig(stderr, stdout)
	if err != nil {
		return nil, lifecycle, err
	}
	lifecycle.ModuleInstantiations++
	module, err := instantiateCOWModule(ctx, engine.runtime, engine.compiled, moduleConfig, allocator)
	if err != nil {
		if temporary != nil {
			_ = temporary.Close()
		}
		return nil, lifecycle, fmt.Errorf("instantiate COW guest: %w", err)
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
	memory := module.Memory()
	if memory == nil {
		return nil, lifecycle, errors.New("COW slot has no linear memory")
	}
	currentBytes := uint64(memory.Size())
	growthPages, err := cowBaselineGrowthPages(currentBytes, runtime.image.baselineSize)
	if err != nil {
		return nil, lifecycle, errors.New("COW slot memory shape drifted")
	}
	if growthPages != 0 {
		previousPages, ok := memory.Grow(growthPages)
		if !ok || uint64(previousPages)*wasmLinearPageSize != currentBytes {
			return nil, lifecycle, errors.New("grow COW slot to sealed baseline")
		}
	}
	lifecycle.InitializeCalls++
	if err := callNoArgs(ctx, module, "_initialize"); err != nil {
		return nil, lifecycle, withGuestDiagnostic(err, stderr.String())
	}
	allocation, err := allocator.Allocation()
	if err != nil {
		return nil, lifecycle, fmt.Errorf("resolve COW allocation: %w", err)
	}
	if err := allocation.restoreBaselineBeforeServe(); err != nil {
		return nil, lifecycle, fmt.Errorf("attach COW baseline: %w", err)
	}
	if memory == nil || uint64(memory.Size()) != runtime.image.baselineSize {
		return nil, lifecycle, errors.New("COW slot memory shape drifted")
	}
	stderr.Reset()
	var cold coldIOContinuation
	if engine.config.Mechanisms.ColdIOContinuation {
		continuation, continuationErr := newColdIOContinuation(allocation, *engine.config.ColdIO)
		if continuationErr != nil {
			return nil, lifecycle, fmt.Errorf("create cold I/O continuation: %w", continuationErr)
		}
		cold = continuation
	}
	failed = false
	return &preparedInstance{module: module, stderr: stderr, stdout: stdout, temporary: temporary, cold: cold}, lifecycle, nil
}

func (runtime *linuxCOWPreparedRuntime) close() error {
	if runtime == nil || runtime.image == nil {
		return nil
	}
	return runtime.image.Close()
}

func (runtime *linuxCOWPreparedRuntime) imageState() PreparedImageState {
	if runtime == nil || runtime.image == nil {
		return PreparedImageState{}
	}
	state := runtime.image.preparedImageState()
	state.TrustedPrepareSHA256 = runtime.trustedPrepareSHA256
	state.ParentTrustedPrepareSHA256 = runtime.parentTrustedPrepareSHA256
	return state
}

func closePreparedInstance(instance *preparedInstance) error {
	if instance == nil {
		return nil
	}
	moduleErr := instance.module.Close(context.Background())
	var temporaryErr error
	if instance.temporary != nil {
		temporaryErr = instance.temporary.Close()
	}
	return errors.Join(moduleErr, temporaryErr)
}

//go:build linux

package wazero

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/tetratelabs/wazero/experimental"
)

type linuxCOWPreparedRuntime struct {
	image            *cowImage
	verifyDigest     *[sha256.Size]byte
	canonicalGlobals preparedGlobalSnapshot
	warmupProfile    string
	warmupGeneration string
}

func cowReadyStrategySupported() bool { return true }

func newCOWPreparedRuntime(ctx context.Context, engine *Engine) (cowPreparedRuntime, error) {
	if engine == nil {
		return nil, errors.New("COW prepared runtime requires an engine")
	}
	if !engine.stateCensus.Artifact.ParseComplete {
		return nil, errors.New("COW prepared runtime requires a complete static artifact census")
	}
	if !engine.stateCensus.Memory.COWEligible {
		return nil, errors.New("reactor memory is not eligible for COW")
	}
	canonical, err := engine.newInitializedModule(ctx, "cow_image_")
	if err != nil {
		return nil, fmt.Errorf("prepare canonical COW image: %w", err)
	}
	defer canonical.module.Close(context.Background())
	if engine.cowWarmupProfile != "" {
		warmupContext, hostCallGuard := guardInitializationHostCalls(ctx)
		warmupStarted := time.Now()
		err = callStatusWithBytes(warmupContext, canonical.module, "runtime_warmup", []byte(engine.cowWarmupProfile))
		observe(engine.observer, "cow_image_warmup", warmupStarted, err)
		if err != nil {
			return nil, withGuestDiagnostic(err, canonical.stderr.String())
		}
		if err := verifyNoInitializationHostCalls(hostCallGuard); err != nil {
			return nil, err
		}
	}
	memory := canonical.module.Memory()
	if memory == nil || memory.Size() == 0 {
		return nil, errors.New("canonical COW image has no linear memory")
	}
	baseline, ok := memory.Read(0, memory.Size())
	if !ok {
		return nil, errors.New("read canonical COW memory")
	}
	canonicalGlobals, err := snapshotPreparedMutableGlobals(canonical.module, engine.stateCensus.Artifact.Globals.ExportedMutableNames)
	if err != nil {
		return nil, err
	}
	image, err := newCOWImage(baseline)
	if err != nil {
		return nil, err
	}
	runtime := &linuxCOWPreparedRuntime{
		image: image, canonicalGlobals: canonicalGlobals,
		warmupProfile: engine.cowWarmupProfile, warmupGeneration: engine.cowWarmupGeneration,
	}
	if engine.verifyCOWPreparedImage {
		digest := sha256.Sum256(baseline)
		runtime.verifyDigest = &digest
	}
	return runtime, nil
}

func (runtime *linuxCOWPreparedRuntime) prepare(ctx context.Context, engine *Engine, prefix string) (*preparedInstance, error) {
	if runtime == nil || runtime.image == nil {
		return nil, errors.New("COW prepared runtime is unavailable")
	}
	ctx, hostCallGuard := guardInitializationHostCalls(ctx)
	guestStderr := &bytes.Buffer{}
	allocator := runtime.image.newAllocator()
	instantiateStarted := time.Now()
	module, err := engine.runtime.InstantiateModule(
		experimental.WithMemoryAllocator(ctx, allocator),
		engine.compiled,
		newModuleConfig(guestStderr),
	)
	observe(engine.observer, prefix+"instantiate_guest", instantiateStarted, err)
	if err != nil {
		return nil, fmt.Errorf("instantiate COW guest: %w", err)
	}
	failed := true
	defer func() {
		if failed {
			_ = module.Close(context.Background())
		}
	}()

	initializeStarted := time.Now()
	err = callNoArgs(ctx, module, "_initialize")
	observe(engine.observer, prefix+"_initialize", initializeStarted, err)
	if err != nil {
		return nil, withGuestDiagnostic(err, guestStderr.String())
	}
	allocation, err := allocator.Allocation()
	if err != nil {
		return nil, fmt.Errorf("resolve COW allocation: %w", err)
	}
	restoreStarted := time.Now()
	err = allocation.Reset()
	observe(engine.observer, prefix+"cow_restore", restoreStarted, err)
	if err != nil {
		return nil, err
	}
	memory := module.Memory()
	if memory == nil || uint64(memory.Size()) != runtime.image.size {
		return nil, errors.New("COW slot memory shape drifted")
	}
	attachStarted := time.Now()
	if err := verifyPreparedMutableGlobals(module, runtime.canonicalGlobals); err != nil {
		observe(engine.observer, prefix+"attach_globals", attachStarted, err)
		return nil, err
	}
	observe(engine.observer, prefix+"attach_globals", attachStarted, nil)
	hostAttachStarted := time.Now()
	if err := verifyNoInitializationHostCalls(hostCallGuard); err != nil {
		observe(engine.observer, prefix+"attach_host_calls", hostAttachStarted, err)
		return nil, err
	}
	observe(engine.observer, prefix+"attach_host_calls", hostAttachStarted, nil)
	if runtime.verifyDigest != nil {
		verifyStarted := time.Now()
		view, ok := memory.Read(0, memory.Size())
		verified := ok && sha256.Sum256(view) == *runtime.verifyDigest
		var verifyErr error
		if !verified {
			verifyErr = errors.New("COW slot does not match canonical prepared image")
		}
		observe(engine.observer, prefix+"cow_verify", verifyStarted, verifyErr)
		if verifyErr != nil {
			return nil, verifyErr
		}
	}
	guestStderr.Reset()
	failed = false
	return &preparedInstance{module: module, stderr: guestStderr, memoryBytes: uint64(memory.Size())}, nil
}

func (runtime *linuxCOWPreparedRuntime) close() error {
	if runtime == nil || runtime.image == nil {
		return nil
	}
	return runtime.image.Close()
}

func (runtime *linuxCOWPreparedRuntime) preparedImageState() PreparedImageState {
	if runtime == nil || runtime.image == nil {
		return PreparedImageState{}
	}
	state := runtime.image.preparedImageState()
	state.WarmupProfile = runtime.warmupProfile
	state.WarmupGenerationSHA256 = runtime.warmupGeneration
	return state
}

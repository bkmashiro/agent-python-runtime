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

func (engine *Engine) newSeededInitializedModule(ctx context.Context, prefix string) (*preparedInstance, *cowImage, error) {
	seed, err := engine.cowSnapshotShell.materializeSeed()
	if err != nil {
		return nil, nil, err
	}
	seedStarted := time.Now()
	seedImage, err := newCOWImage(seed)
	seed = nil
	observe(engine.observer, prefix+"seed_image", seedStarted, err)
	if err != nil {
		return nil, nil, err
	}
	ctx, hostCallGuard := guardInitializationHostCalls(ctx)
	guestStderr := &bytes.Buffer{}
	allocator := seedImage.newAllocator()
	instantiateStarted := time.Now()
	module, err := engine.runtime.InstantiateModule(
		experimental.WithMemoryAllocator(ctx, allocator),
		engine.compiled,
		newModuleConfig(guestStderr),
	)
	observe(engine.observer, prefix+"instantiate_guest", instantiateStarted, err)
	if err != nil {
		_ = seedImage.Close()
		return nil, nil, fmt.Errorf("instantiate seeded COW guest: %w", err)
	}
	failed := true
	defer func() {
		if failed {
			_ = module.Close(context.Background())
			_ = seedImage.Close()
		}
	}()
	allocation, err := allocator.Allocation()
	if err != nil {
		return nil, nil, fmt.Errorf("resolve seeded COW allocation: %w", err)
	}
	attachSeedStarted := time.Now()
	err = allocation.Reset()
	observe(engine.observer, prefix+"seed_attach", attachSeedStarted, err)
	if err != nil {
		return nil, nil, fmt.Errorf("attach seeded COW memory: %w", err)
	}
	initializeStarted := time.Now()
	err = callNoArgs(ctx, module, "_initialize")
	observe(engine.observer, prefix+"_initialize", initializeStarted, err)
	if err != nil {
		return nil, nil, withGuestDiagnostic(err, guestStderr.String())
	}
	runtimeInitStarted := time.Now()
	err = callStatusWithBytes(ctx, module, "runtime_init", []byte("{}"))
	observe(engine.observer, prefix+"runtime_init", runtimeInitStarted, err)
	if err != nil {
		return nil, nil, withGuestDiagnostic(err, guestStderr.String())
	}
	attachStarted := time.Now()
	err = verifyNoInitializationHostCalls(hostCallGuard)
	observe(engine.observer, prefix+"attach_host_calls", attachStarted, err)
	if err != nil {
		return nil, nil, err
	}
	memory := module.Memory()
	if memory == nil || uint64(memory.Size()) != engine.cowSnapshotShell.seedSize {
		return nil, nil, errors.New("seeded COW module memory shape drifted")
	}
	guestStderr.Reset()
	failed = false
	return &preparedInstance{module: module, stderr: guestStderr, memoryBytes: uint64(memory.Size())}, seedImage, nil
}

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
	var canonical *preparedInstance
	var seedImage *cowImage
	var err error
	if engine.cowSnapshotShell != nil {
		canonical, seedImage, err = engine.newSeededInitializedModule(ctx, "cow_image_")
	} else {
		canonical, err = engine.newInitializedModule(ctx, "cow_image_")
	}
	if err != nil {
		return nil, fmt.Errorf("prepare canonical COW image: %w", err)
	}
	if seedImage != nil {
		defer seedImage.Close()
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
	imageStarted := time.Now()
	image, err := newCOWImage(baseline)
	observe(engine.observer, "cow_image_seal", imageStarted, err)
	if err != nil {
		return nil, err
	}
	runtime := &linuxCOWPreparedRuntime{
		image: image, canonicalGlobals: canonicalGlobals,
		warmupProfile: engine.cowWarmupProfile, warmupGeneration: engine.cowWarmupGeneration,
	}
	engine.cowSnapshotShell = nil
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
	return &preparedInstance{module: module, stderr: guestStderr, memoryBytes: uint64(memory.Size()), footprintSource: allocation}, nil
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

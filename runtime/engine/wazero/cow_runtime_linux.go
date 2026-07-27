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
	image        *cowImage
	verifyDigest *[sha256.Size]byte
}

func cowReadyStrategySupported() bool { return true }

func newCOWPreparedRuntime(ctx context.Context, engine *Engine) (cowPreparedRuntime, error) {
	if engine == nil || !engine.stateCensus.Memory.COWEligible {
		return nil, errors.New("reactor memory is not eligible for COW")
	}
	canonical, err := engine.newInitializedModule(ctx, "cow_image_")
	if err != nil {
		return nil, fmt.Errorf("prepare canonical COW image: %w", err)
	}
	defer canonical.module.Close(context.Background())
	memory := canonical.module.Memory()
	if memory == nil || memory.Size() == 0 {
		return nil, errors.New("canonical COW image has no linear memory")
	}
	baseline, ok := memory.Read(0, memory.Size())
	if !ok {
		return nil, errors.New("read canonical COW memory")
	}
	image, err := newCOWImage(baseline)
	if err != nil {
		return nil, err
	}
	runtime := &linuxCOWPreparedRuntime{image: image}
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

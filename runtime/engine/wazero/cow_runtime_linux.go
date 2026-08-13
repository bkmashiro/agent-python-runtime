//go:build linux

package wazero

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/tetratelabs/wazero/experimental"
)

type linuxCOWPreparedRuntime struct {
	image *cowImage
}

func newCOWPreparedRuntime(ctx context.Context, engine *Engine) (cowPreparedRuntime, error) {
	if engine == nil {
		return nil, errors.New("COW prepared runtime requires an engine")
	}
	probe := engine.COWProbe()
	if !probe.MemoryCOWCandidate {
		return nil, errors.New("artifact linear memory is not a fixed private COW candidate")
	}
	canonical, err := engine.newPrepared(ctx)
	if err != nil {
		return nil, fmt.Errorf("prepare canonical COW image: %w", err)
	}
	defer closePreparedInstance(canonical)
	memory := canonical.module.Memory()
	if memory == nil || memory.Size() == 0 {
		return nil, errors.New("canonical COW image has no linear memory")
	}
	baseline, ok := memory.Read(0, memory.Size())
	if !ok {
		return nil, errors.New("read canonical COW linear memory")
	}
	image, err := newCOWImage(baseline)
	if err != nil {
		return nil, err
	}
	return &linuxCOWPreparedRuntime{image: image}, nil
}

func (runtime *linuxCOWPreparedRuntime) prepare(ctx context.Context, engine *Engine) (*preparedInstance, error) {
	if runtime == nil || runtime.image == nil {
		return nil, errors.New("COW baseline is unavailable")
	}
	stderr := &bytes.Buffer{}
	allocator := runtime.image.newAllocator()
	moduleConfig, temporary, err := engine.moduleConfig(stderr)
	if err != nil {
		return nil, err
	}
	module, err := engine.runtime.InstantiateModule(experimental.WithMemoryAllocator(ctx, allocator), engine.compiled, moduleConfig)
	if err != nil {
		if temporary != nil {
			_ = temporary.Close()
		}
		return nil, fmt.Errorf("instantiate COW guest: %w", err)
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
	if err := callNoArgs(ctx, module, "_initialize"); err != nil {
		return nil, withGuestDiagnostic(err, stderr.String())
	}
	allocation, err := allocator.Allocation()
	if err != nil {
		return nil, fmt.Errorf("resolve COW allocation: %w", err)
	}
	if err := allocation.restoreBaselineBeforeServe(); err != nil {
		return nil, fmt.Errorf("attach COW baseline: %w", err)
	}
	memory := module.Memory()
	if memory == nil || uint64(memory.Size()) != runtime.image.size {
		return nil, errors.New("COW slot memory shape drifted")
	}
	stderr.Reset()
	failed = false
	return &preparedInstance{module: module, stderr: stderr, temporary: temporary}, nil
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
	return runtime.image.preparedImageState()
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

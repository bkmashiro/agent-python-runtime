package pysolate

import (
	"context"
	"errors"
	"fmt"

	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental"
)

// NewPrepared captures only our Guest's clean init, before any user code or tool.
// Each Run still has private allocated memory: this is full-copy, NOT page COW.
// This is not a generic Wasm checkpoint (Host resources/globals/tables aren't captured).
func NewPrepared(ctx context.Context, wasm []byte, manifest Manifest) (*Runner, error) {
	r, err := New(ctx, wasm, manifest)
	if err != nil {
		return nil, err
	}
	m, err := r.newGuest(ctx, &boundedText{}, &boundedText{})
	if err != nil {
		r.Close(context.Background())
		return nil, err
	}
	defer m.Close(context.Background())
	memory := m.Memory()
	data, ok := memory.Read(0, memory.Size())
	if !ok || len(data) == 0 {
		r.Close(context.Background())
		return nil, errors.New("cannot capture initialized Guest memory")
	}
	r.image = append([]byte(nil), data...)
	return r, nil
}

// NewPreparedCOW captures the initialized Guest in a sealed Linux memfd and
// attaches each fresh instance with MAP_PRIVATE at the original address. It
// shares only linear-memory pages; globals, tables, runtime objects and Host
// resources remain per-instance and are not part of this image.
func NewPreparedCOW(ctx context.Context, wasm []byte, manifest Manifest) (*Runner, error) {
	r, err := New(ctx, wasm, manifest)
	if err != nil {
		return nil, err
	}
	cow, err := newCOWRuntime()
	if err != nil {
		_ = r.Close(context.Background())
		return nil, err
	}
	r.cow = cow
	m, err := r.newGuest(ctx, &boundedText{}, &boundedText{})
	if err != nil {
		_ = r.Close(context.Background())
		return nil, err
	}
	if err := cow.capture(m.Memory()); err != nil {
		_ = m.Close(context.Background())
		_ = r.Close(context.Background())
		return nil, err
	}
	if err := m.Close(context.Background()); err != nil {
		_ = r.Close(context.Background())
		return nil, err
	}
	return r, nil
}

// All constructors use this lifecycle. Only CPython init vs memory restore differs.
func (r *Runner) newGuest(ctx context.Context, stdout, stderr *boundedText) (api.Module, error) {
	instantiateCtx := ctx
	var mapped cowMemory
	if r.cow != nil {
		var err error
		mapped, err = r.cow.allocator(r.code.ExportedMemories()["memory"])
		if err != nil {
			return nil, err
		}
		instantiateCtx = experimental.WithMemoryAllocator(instantiateCtx, mapped)
	}
	m, err := r.runtime.InstantiateModule(instantiateCtx, r.code, r.moduleConfig(ctx, stdout, stderr))
	if err != nil {
		if mapped != nil {
			mapped.Free() // Instantiation may fail before wazero takes ownership.
		}
		return nil, err
	}
	failed := true
	defer func() {
		if failed {
			m.Close(context.Background())
		}
	}()
	if _, err = m.ExportedFunction("_initialize").Call(ctx); err != nil {
		return nil, fmt.Errorf("initialize Guest: %w%s", err, stderr.String())
	}
	if r.cow != nil && r.cow.ready() {
		if err := r.cow.attach(m.Memory()); err != nil {
			return nil, err
		}
	} else if r.image == nil {
		status, err := m.ExportedFunction("init").Call(ctx)
		if err != nil {
			return nil, fmt.Errorf("init CPython: %w%s", err, stderr.String())
		}
		if status[0] != 0 {
			return nil, fmt.Errorf("CPython initialization failed: %s", stderr.String())
		}
	} else {
		// Instantiate/_initialize write data segments; restore the image AFTER them.
		memory := m.Memory()
		size := memory.Size()
		if int(size) > len(r.image) {
			return nil, errors.New("prepared memory shape changed")
		}
		if int(size) < len(r.image) {
			if _, ok := memory.Grow(uint32(len(r.image)-int(size)) / 65536); !ok {
				return nil, errors.New("cannot grow memory to prepared image")
			}
		}
		if !memory.Write(0, r.image) {
			return nil, errors.New("cannot copy prepared memory")
		}
	}
	failed = false
	return m, nil
}

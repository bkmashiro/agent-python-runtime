package pysolate

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/bkmashiro/agent-python-runtime/internal/cowmem"
	"github.com/bkmashiro/agent-python-runtime/internal/perfdiag"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental"
	experimentalsysfs "github.com/tetratelabs/wazero/experimental/sysfs"
)

// NewPrepared captures clean initialization for private full-copy restoration.
// This is not a checkpoint of user execution or Host resources.
func NewPrepared(ctx context.Context, wasm []byte, manifest Manifest) (*Runner, error) {
	return prepare(ctx, wasm, manifest, false, false, nil, false)
}

// COWOptions controls opt-in COW-only optimizations. Defaults preserve the
// original full-artifact COW path.
type COWOptions struct {
	DataImage bool
}

// NewPreparedCOW shares the clean image through private Linux mappings.
// Globals, tables and Host resources are still created independently for each attempt.
func NewPreparedCOW(ctx context.Context, wasm []byte, manifest Manifest, options ...COWOptions) (*Runner, error) {
	dataImage, err := cowDataImageOption(options)
	if err != nil {
		return nil, err
	}
	return prepare(ctx, wasm, manifest, true, false, nil, dataImage)
}

// NewPreparedWorkspace captures an image whose WASI preopen shape includes a
// private /workspace mount. Use the resulting Runner only with RunWorkspace.
func NewPreparedWorkspace(ctx context.Context, wasm []byte, manifest Manifest) (*Runner, error) {
	return prepare(ctx, wasm, manifest, false, true, nil, false)
}

// NewPreparedWorkspaceCOW is the Linux COW variant of NewPreparedWorkspace.
func NewPreparedWorkspaceCOW(ctx context.Context, wasm []byte, manifest Manifest) (*Runner, error) {
	return prepare(ctx, wasm, manifest, true, true, nil, false)
}

// NewPreparedWorkspaceCOWWithOptions is the opt-in variant for workspace images.
func NewPreparedWorkspaceCOWWithOptions(ctx context.Context, wasm []byte, manifest Manifest, options ...COWOptions) (*Runner, error) {
	dataImage, err := cowDataImageOption(options)
	if err != nil {
		return nil, err
	}
	return prepare(ctx, wasm, manifest, true, true, nil, dataImage)
}

// NewPreparedRecorded captures one seed for deterministic full-copy attempts.
func NewPreparedRecorded(ctx context.Context, wasm []byte, manifest Manifest, seed string) (*Runner, error) {
	return prepare(ctx, wasm, manifest, false, false, []string{seed}, false)
}

// NewPreparedRecordedCOW captures one seed for deterministic private COW attempts.
func NewPreparedRecordedCOW(ctx context.Context, wasm []byte, manifest Manifest, seed string, options ...COWOptions) (*Runner, error) {
	dataImage, err := cowDataImageOption(options)
	if err != nil {
		return nil, err
	}
	return prepare(ctx, wasm, manifest, true, false, []string{seed}, dataImage)
}

func cowDataImageOption(options []COWOptions) (bool, error) {
	if len(options) > 1 {
		return false, errors.New("COW accepts at most one options value")
	}
	return len(options) == 1 && options[0].DataImage, nil
}

func prepare(ctx context.Context, wasm []byte, manifest Manifest, cow, workspace bool, seed []string, dataImage bool) (*Runner, error) {
	if len(seed) > 1 || (len(seed) == 1 && seed[0] == "") {
		return nil, errors.New("preparation accepts one nonempty recording seed")
	}
	originalWasm := wasm
	var transformed *cowDataImage
	if cow && dataImage {
		image, transformErr := transformCOWDataImage(wasm)
		if transformErr != nil {
			return nil, transformErr
		}
		transformed = &image
		wasm = image.shell
	}
	r, err := newRunner(ctx, wasm, originalWasm, manifest)
	if err != nil {
		return nil, err
	}
	failed := true
	defer func() {
		if failed {
			_ = r.Close(context.Background())
		}
	}()
	var seedSegments []cowmem.Segment
	if cow {
		r.cow, err = cowmem.New()
		if err != nil {
			return nil, err
		}
		if transformed != nil {
			r.cowSeed, err = cowmem.New()
			if err != nil {
				return nil, err
			}
			seedSegments = make([]cowmem.Segment, len(transformed.active))
			for i, segment := range transformed.active {
				seedSegments[i] = cowmem.Segment{Offset: segment.offset, Data: segment.data}
			}
			if err := r.cowSeed.CaptureSegments(transformed.memorySize, seedSegments); err != nil {
				return nil, err
			}
		}
	}
	r.workspaceImage = workspace
	var rec *recording
	if len(seed) == 1 || workspace {
		state := newRun(ctx, r, false)
		defer state.close()
		if len(seed) == 1 {
			rec = newRecording(seed[0], nil)
			state.recording = rec
		}
		if workspace {
			empty, err := os.MkdirTemp("", "pysolate-workspace-shape-")
			if err != nil {
				return nil, err
			}
			defer os.RemoveAll(empty)
			state.fsConfig, err = workspaceFSConfig(experimentalsysfs.DirFS(empty))
			if err != nil {
				return nil, err
			}
		}
		ctx = state.ctx
	}
	m, err := r.newGuest(ctx, &boundedText{}, &boundedText{})
	if err != nil {
		return nil, err
	}
	defer m.Close(context.Background())
	if cow {
		err = r.cow.Capture(m.Memory())
	} else {
		data, ok := m.Memory().Read(0, m.Memory().Size())
		if !ok || len(data) == 0 {
			return nil, errors.New("cannot capture initialized Guest memory")
		}
		r.image = append([]byte(nil), data...)
	}
	if err != nil {
		return nil, err
	}
	if transformed != nil {
		// Capturing the final image reads every seed page, materializing sparse
		// memfd holes. Retire that mapping and reseal only the original segments
		// so replacement Guests do not retain those zero pages per Runner.
		if err := m.Close(context.Background()); err != nil {
			return nil, err
		}
		if err := r.cowSeed.Close(); err != nil {
			return nil, err
		}
		r.cowSeed, err = cowmem.New()
		if err != nil {
			return nil, err
		}
		if err := r.cowSeed.CaptureSegments(transformed.memorySize, seedSegments); err != nil {
			return nil, err
		}
	}
	if rec != nil {
		r.preparedState, err = rec.capture()
		if err != nil {
			return nil, err
		}
	}
	failed = false
	return r, nil
}

// All constructors use this lifecycle. Only CPython init vs memory restore differs.
func (r *Runner) newGuest(ctx context.Context, stdout, stderr *boundedText) (api.Module, error) {
	span := perfdiag.Start(ctx, "new_guest")
	defer span.End()
	instantiateCtx := ctx
	var mapped cowmem.Memory
	if r.cow != nil {
		var err error
		allocator := r.cow
		if r.cowSeed != nil {
			allocator = r.cowSeed
		}
		mapped, err = allocator.Allocator(r.code.ExportedMemories()["memory"])
		if err != nil {
			return nil, err
		}
		instantiateCtx = experimental.WithMemoryAllocator(instantiateCtx, cowmem.DeferredAllocator(mapped))
	}
	m, err := r.runtime.InstantiateModule(instantiateCtx, r.code, r.moduleConfig(ctx, stdout, stderr))
	if err != nil {
		if mapped != nil {
			mapped.Free() // Instantiation may fail before wazero takes ownership.
		}
		return nil, err
	}
	if mapped != nil {
		m = cowmem.WrapModule(m, mapped)
	}
	failed := true
	defer func() {
		if failed {
			m.Close(context.Background())
		}
	}()
	if r.cowSeed != nil {
		if err := r.cowSeed.Attach(m.Memory()); err != nil {
			return nil, err
		}
	}
	if _, err = m.ExportedFunction("_initialize").Call(ctx); err != nil {
		return nil, fmt.Errorf("initialize Guest: %w%s", err, stderr.String())
	}
	if r.cow != nil && r.cow.Ready() {
		if err := r.cow.Attach(m.Memory()); err != nil {
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
	if state, ok := ctx.Value(runKey{}).(*runState); ok && state.recording != nil && r.preparedState != nil {
		if err := state.recording.restore(r.preparedState); err != nil {
			return nil, err
		}
	}
	failed = false
	return m, nil
}

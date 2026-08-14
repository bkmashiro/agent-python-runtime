//go:build linux

package wazero

import (
	"context"
	"errors"
	"testing"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	wazerort "github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental"
)

func TestWazeroContinuationResumesAfterColdPageOut(t *testing.T) {
	ctx := context.Background()
	runtime := wazerort.NewRuntime(ctx)
	defer runtime.Close(ctx)
	var continuation *linuxColdIOContinuation
	_, err := runtime.NewHostModuleBuilder("test").NewFunctionBuilder().WithFunc(
		func(callContext context.Context, _ api.Module, _, _, _, _ uint32) uint32 {
			if continuation == nil {
				return 1
			}
			_, callErr := continuation.wait(callContext, func(context.Context) ([]byte, error) {
				time.Sleep(10 * time.Millisecond)
				return []byte("ok"), nil
			})
			if callErr != nil {
				return 2
			}
			return 0
		}).Export("wait").Instantiate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := runtime.CompileModule(ctx, coldContinuationTinyModule())
	if err != nil {
		t.Fatal(err)
	}
	defer compiled.Close(ctx)
	baseline := make([]byte, 2*wasmLinearPageSize)
	image, err := newCOWImage(baseline)
	if err != nil {
		t.Fatal(err)
	}
	allocator := image.newAllocator()
	module, err := runtime.InstantiateModule(
		experimental.WithMemoryAllocator(ctx, allocator), compiled,
		wazerort.NewModuleConfig().WithName("cold-continuation").WithStartFunctions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	allocation, err := allocator.Allocation()
	if err != nil {
		t.Fatal(err)
	}
	continuation, err = newColdIOContinuation(allocation, runtimeconfig.ColdIOPolicy{
		ColdAfter: time.Millisecond, PageOutAfter: 2 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	values, err := module.ExportedFunction("run").Call(withColdIOContinuation(ctx, continuation))
	if err != nil || len(values) != 1 || values[0] != 91 {
		t.Fatalf("values=%v err=%v", values, err)
	}
	evidence := continuation.finish()
	if err := evidence.Validate(); err != nil {
		t.Fatal(err)
	}
	if evidence.ColdAttempts != 1 || evidence.PageOutAttempts != 1 || evidence.Resumes != 1 {
		t.Fatalf("evidence=%+v", evidence)
	}
	if err := module.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := image.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestColdIOContinuationPreservesPrivateDirtyState(t *testing.T) {
	baseline := make([]byte, 2*wasmLinearPageSize)
	image, err := newCOWImage(baseline)
	if err != nil {
		t.Fatal(err)
	}
	allocator := image.newAllocator()
	linear := allocator.Allocate(uint64(len(baseline)), uint64(len(baseline)))
	memory, ok := linear.(*cowLinearMemory)
	if !ok {
		t.Fatalf("allocation type %T", linear)
	}
	view := memory.Reallocate(uint64(len(baseline)))
	view[wasmLinearPageSize+17] = 91

	continuation, err := newColdIOContinuation(memory, runtimeconfig.ColdIOPolicy{ColdAfter: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	result, err := continuation.wait(context.Background(), func(context.Context) ([]byte, error) {
		time.Sleep(10 * time.Millisecond)
		return []byte("done"), nil
	})
	if err != nil || string(result) != "done" {
		t.Fatalf("result=%q err=%v", result, err)
	}
	if view[wasmLinearPageSize+17] != 91 {
		t.Fatal("private dirty state was lost while cold")
	}
	evidence := continuation.finish()
	if err := evidence.Validate(); err != nil {
		t.Fatal(err)
	}
	if evidence.State != ColdIOTerminal || evidence.Waits != 1 || evidence.ColdAttempts != 1 || evidence.Resumes != 1 || evidence.AdvisedBytes == 0 {
		t.Fatalf("evidence=%+v", evidence)
	}
	memory.Free()
	fresh := image.newAllocator().Allocate(uint64(len(baseline)), uint64(len(baseline))).(*cowLinearMemory)
	freshView := fresh.Reallocate(uint64(len(baseline)))
	if freshView[wasmLinearPageSize+17] != 0 {
		t.Fatal("parked slot state leaked into a fresh mapping")
	}
	fresh.Free()
	if err := image.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestColdIOContinuationAttemptsPageOutAndResumesAfterCancellation(t *testing.T) {
	baseline := make([]byte, 2*wasmLinearPageSize)
	image, err := newCOWImage(baseline)
	if err != nil {
		t.Fatal(err)
	}
	allocator := image.newAllocator()
	memory := allocator.Allocate(uint64(len(baseline)), uint64(len(baseline))).(*cowLinearMemory)
	memory.Reallocate(uint64(len(baseline)))[wasmLinearPageSize] = 73
	continuation, err := newColdIOContinuation(memory, runtimeconfig.ColdIOPolicy{
		ColdAfter: time.Millisecond, PageOutAfter: 2 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = continuation.wait(ctx, func(context.Context) ([]byte, error) {
		time.Sleep(100 * time.Millisecond)
		return []byte("late"), nil
	})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) >= 50*time.Millisecond {
		t.Fatalf("wait error=%v elapsed=%s", err, time.Since(started))
	}
	if memory.Reallocate(uint64(len(baseline)))[wasmLinearPageSize] != 73 {
		t.Fatal("private state was lost on cancelled cold wait")
	}
	evidence := continuation.finish()
	if err := evidence.Validate(); err != nil {
		t.Fatal(err)
	}
	if evidence.Waits != 1 || evidence.ColdAttempts != 1 || evidence.PageOutAttempts != 1 || evidence.Resumes != 1 || evidence.State != ColdIOTerminal {
		t.Fatalf("evidence=%+v", evidence)
	}
	memory.Free()
	if err := image.Close(); err != nil {
		t.Fatal(err)
	}
}

func coldContinuationTinyModule() []byte {
	return []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x0d, 0x02,
		0x60, 0x04, 0x7f, 0x7f, 0x7f, 0x7f, 0x01, 0x7f,
		0x60, 0x00, 0x01, 0x7f,
		0x02, 0x0d, 0x01, 0x04, 't', 'e', 's', 't', 0x04, 'w', 'a', 'i', 't', 0x00, 0x00,
		0x03, 0x02, 0x01, 0x01,
		0x05, 0x04, 0x01, 0x01, 0x02, 0x02,
		0x07, 0x10, 0x02,
		0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00,
		0x03, 'r', 'u', 'n', 0x00, 0x01,
		0x0a, 0x20, 0x01, 0x1e, 0x00,
		0x41, 0x91, 0x80, 0x04,
		0x41, 0xdb, 0x00,
		0x3a, 0x00, 0x00,
		0x41, 0x00, 0x41, 0x00, 0x41, 0x00, 0x41, 0x00,
		0x10, 0x00, 0x1a,
		0x41, 0x91, 0x80, 0x04,
		0x2d, 0x00, 0x00,
		0x0b,
	}
}

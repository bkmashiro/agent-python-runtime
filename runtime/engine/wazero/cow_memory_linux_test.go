//go:build linux

package wazero

import (
	"context"
	"errors"
	"testing"

	wazerort "github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental"
	"golang.org/x/sys/unix"
)

func TestCOWImagePrivateMappingsIsolateAndReset(t *testing.T) {
	baseline := make([]byte, wasmLinearPageSize)
	baseline[0] = 7
	baseline[4096] = 8
	image, err := newCOWImage(baseline)
	if err != nil {
		t.Fatal(err)
	}
	state := image.preparedImageState()
	pageSize := uint64(unix.Getpagesize())
	wantPages := uint64(len(baseline)) / pageSize
	if !state.Available || state.VirtualBytes != uint64(len(baseline)) || state.PageSizeBytes != pageSize ||
		state.ZeroPages != wantPages-2 || state.NonZeroPages != 2 || state.SparsePotentialBytes != (wantPages-2)*pageSize ||
		state.AllocatedBytes == 0 || state.AllocatedBytes >= state.VirtualBytes {
		t.Fatalf("unexpected prepared image state: %+v", state)
	}
	firstAllocator := image.newAllocator()
	secondAllocator := image.newAllocator()
	first := firstAllocator.Allocate(wasmLinearPageSize, wasmLinearPageSize).Reallocate(wasmLinearPageSize)
	second := secondAllocator.Allocate(wasmLinearPageSize, wasmLinearPageSize).Reallocate(wasmLinearPageSize)
	if first == nil || second == nil || first[0] != 0 || second[0] != 0 {
		t.Fatalf("allocator did not provide fresh memory: first=%v second=%v", first, second)
	}
	firstMemory, err := firstAllocator.Allocation()
	if err != nil {
		t.Fatal(err)
	}
	secondMemory, err := secondAllocator.Allocation()
	if err != nil {
		t.Fatal(err)
	}
	if err := firstMemory.Reset(); err != nil {
		t.Fatal(err)
	}
	if err := secondMemory.Reset(); err != nil {
		t.Fatal(err)
	}
	if first[0] != 7 || second[0] != 7 {
		t.Fatalf("baseline attach failed: first=%v second=%v", first, second)
	}
	first[0], first[4096] = 90, 91
	address := &first[0]
	if second[0] != 7 || second[4096] != 8 {
		t.Fatalf("private write leaked to sibling: %d %d", second[0], second[4096])
	}
	if err := firstMemory.Reset(); err != nil {
		t.Fatal(err)
	}
	if first[0] != 7 || first[4096] != 8 {
		t.Fatalf("reset did not restore baseline: %d %d", first[0], first[4096])
	}
	if &first[0] != address {
		t.Fatal("reset changed the linear-memory address")
	}
	if err := image.Close(); !errors.Is(err, errCOWImageInUse) {
		t.Fatalf("closed image with live mappings: %v", err)
	}
	firstMemory.Free()
	firstMemory.Free()
	if got := firstMemory.Reallocate(1); got != nil {
		t.Fatal("freed COW memory reallocated")
	}
	secondMemory.Free()
	if err := image.Close(); err != nil {
		t.Fatal(err)
	}
	if err := image.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCOWImageAllZeroBaselineRemainsSparseAndSealed(t *testing.T) {
	image, err := newCOWImage(make([]byte, wasmLinearPageSize))
	if err != nil {
		t.Fatal(err)
	}
	state := image.preparedImageState()
	if !state.Available || state.AllocatedBytes != 0 || state.ZeroPages == 0 || state.NonZeroPages != 0 ||
		state.SparsePotentialBytes != state.VirtualBytes {
		t.Fatalf("unexpected all-zero sparse image state: %+v", state)
	}
	allocator := image.newAllocator()
	memory := allocator.Allocate(wasmLinearPageSize, wasmLinearPageSize)
	if memory == nil {
		t.Fatal("allocate sparse COW memory")
	}
	view := memory.Reallocate(wasmLinearPageSize)
	if view == nil {
		t.Fatal("reallocate sparse COW memory")
	}
	allocation, err := allocator.Allocation()
	if err != nil {
		t.Fatal(err)
	}
	if err := allocation.Reset(); err != nil {
		t.Fatal(err)
	}
	if view[0] != 0 || view[len(view)-1] != 0 {
		t.Fatal("sparse holes did not read back as zero")
	}
	allocation.Free()
	if err := image.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCOWImageIsSealedAndAllocatorRejectsShapeDrift(t *testing.T) {
	if _, err := newCOWImage(make([]byte, wasmLinearPageSize-1)); err == nil {
		t.Fatal("accepted a partial Wasm page")
	}
	image, err := newCOWImage(make([]byte, wasmLinearPageSize))
	if err != nil {
		t.Fatal(err)
	}
	seals, err := unix.FcntlInt(uintptr(image.fd), unix.F_GET_SEALS, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := unix.F_SEAL_SEAL | unix.F_SEAL_SHRINK | unix.F_SEAL_GROW | unix.F_SEAL_WRITE
	if seals&want != want {
		t.Fatalf("missing seals: got=%#x want=%#x", seals, want)
	}
	if _, err := unix.Pwrite(image.fd, []byte{1}, 0); err == nil {
		t.Fatal("sealed COW image remained writable")
	}
	allocator := image.newAllocator()
	if got := allocator.Allocate(wasmLinearPageSize, 2*wasmLinearPageSize).Reallocate(wasmLinearPageSize); got != nil {
		t.Fatal("allocator accepted maximum-size drift")
	}
	if _, err := allocator.Allocation(); !errors.Is(err, errCOWAllocationShape) {
		t.Fatalf("missing shape error: %v", err)
	}
	if err := image.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCOWImageRejectsReplacedFD(t *testing.T) {
	image, err := newCOWImage(make([]byte, wasmLinearPageSize))
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := unix.Open("/dev/null", unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Dup3(replacement, image.fd, unix.O_CLOEXEC); err != nil {
		_ = unix.Close(replacement)
		t.Fatal(err)
	}
	_ = unix.Close(replacement)
	allocator := image.newAllocator()
	if got := allocator.Allocate(wasmLinearPageSize, wasmLinearPageSize).Reallocate(wasmLinearPageSize); got != nil {
		t.Fatal("allocator accepted a replaced image FD")
	}
	if _, err := allocator.Allocation(); !errors.Is(err, errCOWImageIdentity) {
		t.Fatalf("missing image identity error: %v", err)
	}
	if err := image.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWazeroTinyModuleUsesCOWImageAndExactReset(t *testing.T) {
	ctx := context.Background()
	runtime := wazerort.NewRuntime(ctx)
	defer runtime.Close(ctx)
	compiled, err := runtime.CompileModule(ctx, cowTinyModule())
	if err != nil {
		t.Fatal(err)
	}
	baseline := make([]byte, wasmLinearPageSize)
	baseline[0] = 7
	image, err := newCOWImage(baseline)
	if err != nil {
		t.Fatal(err)
	}
	firstAllocator := image.newAllocator()
	secondAllocator := image.newAllocator()
	first, err := runtime.InstantiateModule(
		experimental.WithMemoryAllocator(ctx, firstAllocator), compiled,
		wazerort.NewModuleConfig().WithName("cow-first").WithStartFunctions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := runtime.InstantiateModule(
		experimental.WithMemoryAllocator(ctx, secondAllocator), compiled,
		wazerort.NewModuleConfig().WithName("cow-second").WithStartFunctions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	firstMemory, err := firstAllocator.Allocation()
	if err != nil {
		t.Fatal(err)
	}
	secondMemory, err := secondAllocator.Allocation()
	if err != nil {
		t.Fatal(err)
	}
	if err := firstMemory.Reset(); err != nil {
		t.Fatal(err)
	}
	if err := secondMemory.Reset(); err != nil {
		t.Fatal(err)
	}
	readFirst := first.ExportedFunction("read0")
	readSecond := second.ExportedFunction("read0")
	writeFirst := first.ExportedFunction("write0")
	if readFirst == nil || readSecond == nil || writeFirst == nil {
		t.Fatal("tiny module exports are missing")
	}
	assertRead := func(functionName string, function api.Function, want uint64) {
		t.Helper()
		values, err := function.Call(ctx)
		if err != nil || len(values) != 1 || values[0] != want {
			t.Fatalf("%s: values=%v err=%v want=%d", functionName, values, err, want)
		}
	}
	assertRead("first baseline", readFirst, 7)
	assertRead("second baseline", readSecond, 7)
	if _, err := writeFirst.Call(ctx, 99); err != nil {
		t.Fatal(err)
	}
	assertRead("first private", readFirst, 99)
	assertRead("second isolated", readSecond, 7)
	if err := firstMemory.Reset(); err != nil {
		t.Fatal(err)
	}
	assertRead("first reset", readFirst, 7)
	if err := first.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := second.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := image.Close(); err != nil {
		t.Fatal(err)
	}
}

func cowTinyModule() []byte {
	return []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x09, 0x02, 0x60, 0x00, 0x01, 0x7f, 0x60, 0x01, 0x7f, 0x00,
		0x03, 0x03, 0x02, 0x00, 0x01,
		0x05, 0x04, 0x01, 0x01, 0x01, 0x01,
		0x07, 0x1b, 0x03,
		0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00,
		0x05, 'r', 'e', 'a', 'd', '0', 0x00, 0x00,
		0x06, 'w', 'r', 'i', 't', 'e', '0', 0x00, 0x01,
		0x0a, 0x13, 0x02,
		0x07, 0x00, 0x41, 0x00, 0x2d, 0x00, 0x00, 0x0b,
		0x09, 0x00, 0x41, 0x00, 0x20, 0x00, 0x3a, 0x00, 0x00, 0x0b,
	}
}

//go:build linux

package wazero

import (
	"bytes"
	"context"
	"errors"
	"testing"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	wazerort "github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental"
	"golang.org/x/sys/unix"
)

func TestInstantiateCOWModuleFailureReleasesMapping(t *testing.T) {
	ctx := context.Background()
	runtime := wazerort.NewRuntime(ctx)
	defer runtime.Close(ctx)
	compiled, err := runtime.CompileModule(ctx, cowStartTrapModule())
	if err != nil {
		t.Fatal(err)
	}
	image, err := newCOWImageWithMaximum(make([]byte, wasmLinearPageSize), 2*wasmLinearPageSize)
	if err != nil {
		t.Fatal(err)
	}
	allocator := image.newAllocator()
	_, err = instantiateCOWModule(ctx, runtime, compiled, wazerort.NewModuleConfig().WithName("cow-trap"), allocator)
	if err == nil {
		t.Fatal("start trap unexpectedly instantiated")
	}
	if err := image.Close(); err != nil {
		t.Fatalf("failed instantiation leaked mapping: %v", err)
	}
}

func TestCOWProbeAcceptsBoundedGrowableMemory(t *testing.T) {
	config := runtimeconfig.DefaultRunConfig()
	config.Mechanisms = runtimeconfig.MechanismSet{PreparedRuntime: true, MemoryCOW: true}
	engine, err := New(context.Background(), cowGrowableTinyModule(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close(context.Background())
	probe := engine.COWProbe()
	if probe.MemoryFixed || !probe.MemoryMaximumDeclared || !probe.MemoryCOWCandidate {
		t.Fatalf("growable bounded memory probe=%+v", probe)
	}
	maximum, err := cowMaximumMemoryBytes(engine)
	if err != nil || maximum != 2*wasmLinearPageSize {
		t.Fatalf("maximum=%d err=%v", maximum, err)
	}
}

func TestCOWImageGrowableMappingsExposeZeroTailAndIsolate(t *testing.T) {
	baseline := make([]byte, wasmLinearPageSize)
	baseline[0] = 7
	image, err := newCOWImageWithMaximum(baseline, 2*wasmLinearPageSize)
	if err != nil {
		t.Fatal(err)
	}
	defer image.Close()
	state := image.preparedImageState()
	if state.BaselineBytes != wasmLinearPageSize || state.VirtualBytes != 2*wasmLinearPageSize || state.SparsePotentialBytes != wasmLinearPageSize || state.AllocatedBytes == 0 || state.AllocatedBytes >= state.BaselineBytes || state.PageSizeBytes == 0 || state.ZeroPages+state.NonZeroPages != state.BaselineBytes/state.PageSizeBytes || state.NonZeroPages == 0 {
		t.Fatalf("image state=%+v", state)
	}

	firstAllocator := image.newAllocator()
	secondAllocator := image.newAllocator()
	firstMemory := firstAllocator.Allocate(wasmLinearPageSize, 2*wasmLinearPageSize)
	secondMemory := secondAllocator.Allocate(wasmLinearPageSize, 2*wasmLinearPageSize)
	first := firstMemory.Reallocate(2 * wasmLinearPageSize)
	second := secondMemory.Reallocate(2 * wasmLinearPageSize)
	if first == nil || second == nil {
		t.Fatal("growable COW allocation failed")
	}
	if first[0] != 0 || first[wasmLinearPageSize] != 0 || second[wasmLinearPageSize] != 0 {
		t.Fatalf("anonymous initialization mismatch: first=%d/%d second-tail=%d", first[0], first[wasmLinearPageSize], second[wasmLinearPageSize])
	}
	firstAllocation, err := firstAllocator.Allocation()
	if err != nil {
		t.Fatal(err)
	}
	secondAllocation, err := secondAllocator.Allocation()
	if err != nil {
		t.Fatal(err)
	}
	if err := firstAllocation.restoreBaselineBeforeServe(); err != nil {
		t.Fatal(err)
	}
	if err := secondAllocation.restoreBaselineBeforeServe(); err != nil {
		t.Fatal(err)
	}
	if first[0] != 7 || first[wasmLinearPageSize] != 0 || second[wasmLinearPageSize] != 0 {
		t.Fatalf("baseline/tail mismatch: first=%d/%d second-tail=%d", first[0], first[wasmLinearPageSize], second[wasmLinearPageSize])
	}
	first[wasmLinearPageSize] = 91
	if second[wasmLinearPageSize] != 0 {
		t.Fatal("grown private page leaked to sibling")
	}
	if err := firstAllocation.restoreBaselineBeforeServe(); err != nil {
		t.Fatal(err)
	}
	if first[0] != 7 || first[wasmLinearPageSize] != 0 {
		t.Fatalf("growable baseline restore=%d/%d", first[0], first[wasmLinearPageSize])
	}
	if got := firstMemory.Reallocate(3 * wasmLinearPageSize); got != nil {
		t.Fatal("allocation grew beyond declared maximum")
	}
	firstMemory.Free()
	secondMemory.Free()
}

func TestCOWImagePrivateMappingsIsolateAndDiscard(t *testing.T) {
	baseline := make([]byte, wasmLinearPageSize)
	baseline[0] = 7
	baseline[4096] = 8
	image, err := newCOWImage(baseline)
	if err != nil {
		t.Fatal(err)
	}
	firstAllocator := image.newAllocator()
	secondAllocator := image.newAllocator()
	first := firstAllocator.Allocate(wasmLinearPageSize, wasmLinearPageSize).Reallocate(wasmLinearPageSize)
	second := secondAllocator.Allocate(wasmLinearPageSize, wasmLinearPageSize).Reallocate(wasmLinearPageSize)
	if first == nil || second == nil || first[0] != 0 || second[0] != 0 {
		t.Fatalf("anonymous initialization failed: first=%v second=%v", first, second)
	}
	firstMemory, err := firstAllocator.Allocation()
	if err != nil {
		t.Fatal(err)
	}
	secondMemory, err := secondAllocator.Allocation()
	if err != nil {
		t.Fatal(err)
	}
	if err := firstMemory.restoreBaselineBeforeServe(); err != nil {
		t.Fatal(err)
	}
	if err := secondMemory.restoreBaselineBeforeServe(); err != nil {
		t.Fatal(err)
	}
	if first[0] != 7 || second[0] != 7 {
		t.Fatalf("baseline mapping failed: first=%v second=%v", first, second)
	}
	first[0] = 22 // Simulate Wazero data-segment writes during instantiation.
	if err := firstMemory.restoreBaselineBeforeServe(); err != nil {
		t.Fatal(err)
	}
	if first[0] != 7 {
		t.Fatalf("pre-serve restore did not expose baseline: %d", first[0])
	}
	first[0], first[4096] = 90, 91 // Simulate request-private writes.
	if second[0] != 7 || second[4096] != 8 {
		t.Fatalf("private write leaked to sibling: %d %d", second[0], second[4096])
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

func TestCOWInitializationStartsWithZeroBSSAndRestoresSnapshot(t *testing.T) {
	ctx := context.Background()
	runtime := wazerort.NewRuntime(ctx)
	defer runtime.Close(ctx)
	compiled, err := runtime.CompileModule(ctx, cowInitializationOrderModule())
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := runtime.InstantiateModule(ctx, compiled, wazerort.NewModuleConfig().WithName("cow-init-canonical"))
	if err != nil {
		t.Fatal(err)
	}
	defer canonical.Close(ctx)
	initialize := canonical.ExportedFunction("_initialize")
	if initialize == nil {
		t.Fatal("_initialize export is missing")
	}
	if _, err := initialize.Call(ctx); err != nil {
		t.Fatalf("canonical _initialize: %v", err)
	}
	// Model work performed after the module constructor, before snapshotting.
	if !canonical.Memory().WriteByte(1, 99) {
		t.Fatal("write prepared value")
	}
	initial, ok := canonical.Memory().Read(0, 2)
	if !ok || !bytes.Equal(initial, []byte{7, 99}) {
		t.Fatalf("canonical initialization=%v ok=%v", initial, ok)
	}
	snapshot := make([]byte, wasmLinearPageSize)
	copy(snapshot, initial)
	image, err := newCOWImage(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	defer image.Close()

	allocator := image.newAllocator()
	clone, err := runtime.InstantiateModule(experimental.WithMemoryAllocator(ctx, allocator), compiled, wazerort.NewModuleConfig().WithName("cow-init-clone"))
	if err != nil {
		t.Fatalf("instantiate clone: %v", err)
	}
	defer clone.Close(ctx)
	if _, err := clone.ExportedFunction("_initialize").Call(ctx); err != nil {
		t.Fatalf("clone _initialize did not see zero BSS: %v", err)
	}
	allocation, err := allocator.Allocation()
	if err != nil {
		t.Fatal(err)
	}
	if err := allocation.restoreBaselineBeforeServe(); err != nil {
		t.Fatal(err)
	}
	restored, ok := clone.Memory().Read(0, 2)
	if !ok || !bytes.Equal(restored, initial) {
		t.Fatalf("restored snapshot=%v want=%v ok=%v", restored, initial, ok)
	}
}

func TestCOWImageIsSealedAndAllocatorRejectsShapeDrift(t *testing.T) {
	if _, err := newCOWImage(make([]byte, wasmLinearPageSize-1)); err == nil {
		t.Fatal("accepted a partial Wasm page")
	}
	baseline := make([]byte, wasmLinearPageSize)
	if _, err := newCOWImageWithMaximum(baseline, wasmLinearPageSize-1); err == nil {
		t.Fatal("accepted maximum below baseline")
	}
	if _, err := newCOWImageWithMaximum(baseline, wasmLinearPageSize+1); err == nil {
		t.Fatal("accepted partial-page maximum")
	}
	image, err := newCOWImage(baseline)
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

func TestWazeroGrowableModuleUsesIsolatedCOWTail(t *testing.T) {
	ctx := context.Background()
	runtime := wazerort.NewRuntime(ctx)
	defer runtime.Close(ctx)
	compiled, err := runtime.CompileModule(ctx, cowGrowableTinyModule())
	if err != nil {
		t.Fatal(err)
	}
	baseline := make([]byte, wasmLinearPageSize)
	baseline[0] = 7
	image, err := newCOWImageWithMaximum(baseline, 2*wasmLinearPageSize)
	if err != nil {
		t.Fatal(err)
	}
	first, err := runtime.InstantiateModule(experimental.WithMemoryAllocator(ctx, image.newAllocator()), compiled, wazerort.NewModuleConfig().WithName("grow-first").WithStartFunctions())
	if err != nil {
		t.Fatal(err)
	}
	second, err := runtime.InstantiateModule(experimental.WithMemoryAllocator(ctx, image.newAllocator()), compiled, wazerort.NewModuleConfig().WithName("grow-second").WithStartFunctions())
	if err != nil {
		t.Fatal(err)
	}
	if previous, ok := first.Memory().Grow(1); !ok || previous != 1 {
		t.Fatalf("first grow previous=%d ok=%v", previous, ok)
	}
	if !first.Memory().WriteByte(wasmLinearPageSize, 91) {
		t.Fatal("write grown page")
	}
	if previous, ok := second.Memory().Grow(1); !ok || previous != 1 {
		t.Fatalf("second grow previous=%d ok=%v", previous, ok)
	}
	if value, ok := second.Memory().ReadByte(wasmLinearPageSize); !ok || value != 0 {
		t.Fatalf("grown page leaked value=%d ok=%v", value, ok)
	}
	if _, ok := first.Memory().Grow(1); ok {
		t.Fatal("grew beyond declared maximum")
	}
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

func TestWazeroTinyModuleUsesIsolatedSingleUseCOWMappings(t *testing.T) {
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
	if err := firstMemory.restoreBaselineBeforeServe(); err != nil {
		t.Fatal(err)
	}
	if err := secondMemory.restoreBaselineBeforeServe(); err != nil {
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

func cowInitializationOrderModule() []byte {
	return []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x04, 0x01, 0x60, 0x00, 0x00,
		0x03, 0x02, 0x01, 0x00,
		0x05, 0x04, 0x01, 0x01, 0x01, 0x01,
		0x07, 0x18, 0x02,
		0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00,
		0x0b, '_', 'i', 'n', 'i', 't', 'i', 'a', 'l', 'i', 'z', 'e', 0x00, 0x00,
		0x0a, 0x16, 0x01, 0x14, 0x00,
		0x41, 0x01, 0x2d, 0x00, 0x00, 0x45, 0x04, 0x40, 0x05, 0x00, 0x0b,
		0x41, 0x01, 0x41, 0x2a, 0x3a, 0x00, 0x00, 0x0b,
		0x0b, 0x07, 0x01, 0x00, 0x41, 0x00, 0x0b, 0x01, 0x07,
	}
}

func cowStartTrapModule() []byte {
	return []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x04, 0x01, 0x60, 0x00, 0x00,
		0x03, 0x02, 0x01, 0x00,
		0x05, 0x04, 0x01, 0x01, 0x01, 0x02,
		0x08, 0x01, 0x00,
		0x0a, 0x05, 0x01, 0x03, 0x00, 0x00, 0x0b,
	}
}

func cowGrowableTinyModule() []byte {
	module := append([]byte(nil), cowTinyModule()...)
	memorySection := []byte{0x05, 0x04, 0x01, 0x01, 0x01, 0x01}
	for index := 0; index <= len(module)-len(memorySection); index++ {
		if string(module[index:index+len(memorySection)]) == string(memorySection) {
			module[index+len(memorySection)-1] = 0x02
			return module
		}
	}
	panic("tiny module memory section not found")
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

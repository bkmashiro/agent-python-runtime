//go:build linux

package wazero

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
		Strategy: runtimeconfig.ColdIOFixed, ColdAfter: time.Millisecond, PageOutAfter: 2 * time.Millisecond,
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

	continuation, err := newColdIOContinuation(memory, runtimeconfig.ColdIOPolicy{Strategy: runtimeconfig.ColdIOFixed, ColdAfter: time.Millisecond})
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
		Strategy: runtimeconfig.ColdIOFixed, ColdAfter: time.Millisecond, PageOutAfter: 2 * time.Millisecond,
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

func TestCgroupPressureProbeWalksFiniteAncestorBudgets(t *testing.T) {
	root := t.TempDir()
	leaf := filepath.Join(root, "tenant", "job")
	parent := filepath.Join(root, "tenant")
	for _, directory := range []string{leaf, parent, root} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, value string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(leaf, "memory.current"), "40")
	write(filepath.Join(leaf, "memory.high"), "max")
	write(filepath.Join(leaf, "memory.max"), "100")
	write(filepath.Join(parent, "memory.current"), "40")
	write(filepath.Join(parent, "memory.high"), "max")
	write(filepath.Join(parent, "memory.max"), "100")
	write(filepath.Join(root, "memory.current"), "1")
	write(filepath.Join(root, "memory.high"), "max")
	write(filepath.Join(root, "memory.max"), "max")
	cgroupFile := filepath.Join(t.TempDir(), "cgroup")
	mountInfoFile := filepath.Join(t.TempDir(), "mountinfo")
	if err := os.WriteFile(cgroupFile, []byte("0::/tenant/job\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mountInfoFile, []byte("42 1 0:42 / "+root+" rw - cgroup2 cgroup rw\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	probe, err := discoverCgroupPressureProbeFromFiles(cgroupFile, mountInfoFile)
	if err != nil {
		t.Fatal(err)
	}
	pressured, err := probe.pressured(.8)
	if err != nil || pressured {
		t.Fatalf("low pressure=%v err=%v", pressured, err)
	}
	write(filepath.Join(parent, "memory.current"), "90")
	pressured, err = probe.pressured(.8)
	if err != nil || !pressured {
		t.Fatalf("ancestor pressure=%v err=%v", pressured, err)
	}
	if err := os.Remove(filepath.Join(root, "memory.high")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "memory.max")); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(parent, "memory.current"), "40")
	probe, err = discoverCgroupPressureProbeFromFiles(cgroupFile, mountInfoFile)
	if err != nil {
		t.Fatal(err)
	}
	pressured, err = probe.pressured(.8)
	if err != nil || pressured {
		t.Fatalf("root without controller pressure=%v err=%v", pressured, err)
	}
}

func TestCgroupPressureProbeRejectsUnknownLimitAndReadFailure(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "cgroup"), []byte("0::/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mountRoot := filepath.Join(root, "mount")
	if err := os.MkdirAll(mountRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, value string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(mountRoot, name), []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("memory.current", "1")
	write("memory.high", "max")
	write("memory.max", "max")
	mountInfo := filepath.Join(root, "mountinfo")
	if err := os.WriteFile(mountInfo, []byte("42 1 0:42 / "+mountRoot+" rw - cgroup2 cgroup rw\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	probe, err := discoverCgroupPressureProbeFromFiles(filepath.Join(root, "cgroup"), mountInfo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := probe.pressured(.5); !errors.Is(err, errPressureUnknownLimit) {
		t.Fatalf("unknown limit error=%v", err)
	}
	write("memory.max", "100")
	if err := os.Remove(filepath.Join(mountRoot, "memory.current")); err != nil {
		t.Fatal(err)
	}
	if _, err := probe.pressured(.5); err == nil {
		t.Fatal("missing current file accepted")
	}
}

func TestPressureColdIOAdvisesWhenPressureAppearsLate(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"memory.current", "memory.high", "memory.max"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("1"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "memory.high"), []byte("max"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "memory.max"), []byte("100"), 0o644); err != nil {
		t.Fatal(err)
	}
	probe := &cgroupPressureProbe{budgets: []cgroupBudget{{
		current: filepath.Join(root, "memory.current"),
		high:    filepath.Join(root, "memory.high"), max: filepath.Join(root, "memory.max"),
	}}}
	baseline := make([]byte, 2*wasmLinearPageSize)
	image, err := newCOWImage(baseline)
	if err != nil {
		t.Fatal(err)
	}
	defer image.Close()
	memory := image.newAllocator().Allocate(uint64(len(baseline)), uint64(len(baseline))).(*cowLinearMemory)
	defer memory.Free()
	continuation, err := newColdIOContinuation(memory, runtimeconfig.ColdIOPolicy{
		Strategy: runtimeconfig.ColdIOPressure, ColdAfter: 50 * time.Millisecond, PageOutAfter: 100 * time.Millisecond, PressureThreshold: .5,
	})
	if err != nil {
		t.Fatal(err)
	}
	continuation.probe = probe
	result, err := continuation.wait(context.Background(), func(context.Context) ([]byte, error) {
		time.Sleep(80 * time.Millisecond)
		return []byte("low"), nil
	})
	if err != nil || string(result) != "low" {
		t.Fatalf("low-pressure result=%q err=%v", result, err)
	}
	if continuation.evidence.PressureChecks == 0 || continuation.evidence.PressureHits != 0 || continuation.evidence.ColdAttempts != 0 {
		t.Fatalf("low-pressure evidence=%+v", continuation.evidence)
	}
	if err := os.WriteFile(filepath.Join(root, "memory.current"), []byte("100"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err = continuation.wait(context.Background(), func(context.Context) ([]byte, error) {
		time.Sleep(160 * time.Millisecond)
		return []byte("high"), nil
	})
	if err != nil || string(result) != "high" {
		t.Fatalf("high-pressure result=%q err=%v", result, err)
	}
	evidence := continuation.finish()
	if err := evidence.Validate(); err != nil {
		t.Fatal(err)
	}
	if evidence.Waits != 2 || evidence.PressureChecks < 2 || evidence.PressureHits == 0 || evidence.ColdAttempts != 1 || evidence.PageOutAttempts != 1 {
		t.Fatalf("pressure evidence=%+v", evidence)
	}
}

func TestPressureProbeErrorOnlyStopsCurrentWait(t *testing.T) {
	root := t.TempDir()
	write := func(name, value string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("memory.high", "max")
	write("memory.max", "100")
	probe := &cgroupPressureProbe{budgets: []cgroupBudget{{
		current: filepath.Join(root, "memory.current"),
		high:    filepath.Join(root, "memory.high"), max: filepath.Join(root, "memory.max"),
	}}}
	image, err := newCOWImage(make([]byte, 2*wasmLinearPageSize))
	if err != nil {
		t.Fatal(err)
	}
	defer image.Close()
	memory := image.newAllocator().Allocate(2*wasmLinearPageSize, 2*wasmLinearPageSize).(*cowLinearMemory)
	defer memory.Free()
	continuation, err := newColdIOContinuation(memory, runtimeconfig.ColdIOPolicy{
		Strategy: runtimeconfig.ColdIOPressure, ColdAfter: 20 * time.Millisecond, PressureThreshold: .5,
	})
	if err != nil {
		t.Fatal(err)
	}
	continuation.probe = probe
	if _, err := continuation.wait(context.Background(), func(context.Context) ([]byte, error) {
		time.Sleep(60 * time.Millisecond)
		return []byte("first"), nil
	}); err != nil {
		t.Fatal(err)
	}
	if continuation.evidence.PressureErrors != 1 || continuation.evidence.ColdAttempts != 0 {
		t.Fatalf("first wait evidence=%+v", continuation.evidence)
	}
	write("memory.current", "100")
	if _, err := continuation.wait(context.Background(), func(context.Context) ([]byte, error) {
		time.Sleep(80 * time.Millisecond)
		return []byte("second"), nil
	}); err != nil {
		t.Fatal(err)
	}
	evidence := continuation.finish()
	if err := evidence.Validate(); err != nil {
		t.Fatal(err)
	}
	if evidence.PressureErrors != 1 || evidence.PressureHits != 1 || evidence.ColdAttempts != 1 {
		t.Fatalf("retry evidence=%+v", evidence)
	}
}

func TestColdIOCancellationWinsWhenResultIsReady(t *testing.T) {
	image, err := newCOWImage(make([]byte, 2*wasmLinearPageSize))
	if err != nil {
		t.Fatal(err)
	}
	memory := image.newAllocator().Allocate(2*wasmLinearPageSize, 2*wasmLinearPageSize).(*cowLinearMemory)
	continuation, err := newColdIOContinuation(memory, runtimeconfig.ColdIOPolicy{Strategy: runtimeconfig.ColdIONatural})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, waitErr := continuation.wait(ctx, func(context.Context) ([]byte, error) {
			close(started)
			<-release
			return []byte("late"), nil
		})
		done <- waitErr
	}()
	<-started
	cancel()
	close(release)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("wait error=%v", err)
	}
	continuation.finish()
	memory.Free()
	if err := image.Close(); err != nil {
		t.Fatal(err)
	}
}

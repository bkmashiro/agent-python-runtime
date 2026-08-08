package wazero

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	wabinbinary "github.com/tetratelabs/wabin/binary"
	"github.com/tetratelabs/wabin/leb128"
	wabinwasm "github.com/tetratelabs/wabin/wasm"
	wazeroruntime "github.com/tetratelabs/wazero"
)

const (
	e1DefaultMemoryPages = uint32(256)
	e1WasmPageBytes      = uint64(65536)
)

func TestE1DirtyLoopReactorRunsUntilCancellation(t *testing.T) {
	ctx := context.Background()
	runtime := wazeroruntime.NewRuntimeWithConfig(ctx, wazeroruntime.NewRuntimeConfig().WithCloseOnContextDone(true))
	defer runtime.Close(ctx)
	compiled, err := runtime.CompileModule(ctx, e1DirtyLoopReactor(1, 4096, 2048))
	if err != nil {
		t.Fatal(err)
	}
	module, err := runtime.InstantiateModule(ctx, compiled, wazeroruntime.NewModuleConfig())
	if err != nil {
		t.Fatal(err)
	}
	execute := module.ExportedFunction("execute")
	if execute == nil {
		t.Fatal("execute export missing")
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		_, callErr := execute.Call(runCtx, 0, 0)
		done <- callErr
	}()
	select {
	case callErr := <-done:
		t.Fatalf("dirty loop returned before cancellation: %v", callErr)
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	select {
	case callErr := <-done:
		if callErr == nil || !errors.Is(callErr, context.Canceled) && !strings.Contains(callErr.Error(), "context canceled") {
			t.Fatalf("cancel error = %v", callErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("dirty loop ignored cancellation")
	}
}

func e1DirtyLoopReactor(dirtyPercent int, pageSize uint64, memoryPages uint32) []byte {
	i32 := wabinwasm.ValueTypeI32
	executeBody := make([]byte, 0, 512*1024)
	memoryBytes := uint64(memoryPages) * e1WasmPageBytes
	dirtyPages := (memoryBytes * uint64(dirtyPercent) / 100) / pageSize
	for page := uint64(0); page < dirtyPages; page++ {
		executeBody = append(executeBody, byte(wabinwasm.OpcodeI32Const))
		executeBody = append(executeBody, leb128.EncodeInt32(int32(page*pageSize))...)
		executeBody = append(executeBody,
			byte(wabinwasm.OpcodeI32Const), 1,
			byte(wabinwasm.OpcodeI32Store8), 0, 0,
		)
	}
	executeBody = append(executeBody,
		byte(wabinwasm.OpcodeLoop), 0x40,
		byte(wabinwasm.OpcodeBr), 0,
		byte(wabinwasm.OpcodeEnd),
		byte(wabinwasm.OpcodeI32Const), 0,
		byte(wabinwasm.OpcodeEnd),
	)
	return wabinbinary.EncodeModule(&wabinwasm.Module{
		TypeSection: []*wabinwasm.FunctionType{
			{},
			{Params: []wabinwasm.ValueType{i32, i32}, Results: []wabinwasm.ValueType{i32}},
			{Params: []wabinwasm.ValueType{i32}, Results: []wabinwasm.ValueType{i32}},
			{Params: []wabinwasm.ValueType{i32}},
		},
		FunctionSection: []wabinwasm.Index{0, 1, 1, 2, 3, 1},
		MemorySection:   &wabinwasm.Memory{Min: memoryPages, Max: memoryPages, IsMaxEncoded: true},
		ExportSection: []*wabinwasm.Export{
			{Name: "memory", Type: wabinwasm.ExternTypeMemory, Index: 0},
			{Name: "_initialize", Type: wabinwasm.ExternTypeFunc, Index: 0},
			{Name: "runtime_init", Type: wabinwasm.ExternTypeFunc, Index: 1},
			{Name: "runtime_prepare", Type: wabinwasm.ExternTypeFunc, Index: 2},
			{Name: "runtime_warmup", Type: wabinwasm.ExternTypeFunc, Index: 2},
			{Name: "alloc", Type: wabinwasm.ExternTypeFunc, Index: 3},
			{Name: "dealloc", Type: wabinwasm.ExternTypeFunc, Index: 4},
			{Name: "execute", Type: wabinwasm.ExternTypeFunc, Index: 5},
		},
		CodeSection: []*wabinwasm.Code{
			{Body: []byte{byte(wabinwasm.OpcodeEnd)}},
			{Body: []byte{byte(wabinwasm.OpcodeI32Const), 0, byte(wabinwasm.OpcodeEnd)}},
			{Body: []byte{byte(wabinwasm.OpcodeI32Const), 0, byte(wabinwasm.OpcodeEnd)}},
			{Body: []byte{byte(wabinwasm.OpcodeI32Const), 8, byte(wabinwasm.OpcodeEnd)}},
			{Body: []byte{byte(wabinwasm.OpcodeEnd)}},
			{Body: executeBody},
		},
	})
}

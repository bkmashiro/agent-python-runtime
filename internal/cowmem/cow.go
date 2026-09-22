// Package cowmem owns the Linux-specific prepared-memory backend.
package cowmem

import (
	"context"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental"
)

// Runtime owns the immutable image; each instance owns its mapping.
// Implementations are intentionally Linux-specific; other platforms return an
// unsupported-constructor error rather than silently falling back to copying.
type Runtime interface {
	Allocator(api.MemoryDefinition) (Memory, error)
	Capture(api.Memory) error
	CaptureSegments(size uint64, segments []Segment) error
	Attach(api.Memory) error
	Ready() bool
	Close() error
}

// Segment is one ordered initialization write used to create a sparse seed.
// Later segments intentionally overwrite earlier ones, matching Wasm order.
type Segment struct {
	Offset uint64
	Data   []byte
}

// Memory is one privately mapped Guest linear-memory allocation.
type Memory interface {
	experimental.MemoryAllocator
	experimental.LinearMemory
}

// Wazero may close a module inside a Host call (park/cancel), before Wasm
// unwinds. Keep mmap-backed stack/data alive until the owning call returns.
type deferredFree struct{ Memory }

func (m deferredFree) Allocate(_, _ uint64) experimental.LinearMemory { return m }
func (m deferredFree) Free()                                          {}

// DeferredAllocator lets the outer call owner, rather than module close, free memory.
func DeferredAllocator(memory Memory) experimental.MemoryAllocator {
	return deferredFree{Memory: memory}
}

type guest struct {
	api.Module
	memory Memory
}

func (g *guest) Close(ctx context.Context) error {
	err := g.Module.Close(ctx)
	g.memory.Free()
	return err
}

// WrapModule binds a module's lifecycle to its private memory mapping.
func WrapModule(module api.Module, memory Memory) api.Module {
	return &guest{Module: module, memory: memory}
}

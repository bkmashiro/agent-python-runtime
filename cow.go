package pysolate

import (
	"context"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental"
)

// cowRuntime owns the immutable image; each instance owns its mapping.
// Implementations are intentionally Linux-specific; other platforms return an
// unsupported-constructor error rather than silently falling back to copying.
type cowMemory interface {
	experimental.MemoryAllocator
	experimental.LinearMemory
}

type cowRuntime interface {
	allocator(api.MemoryDefinition) (cowMemory, error)
	capture(api.Memory) error
	attach(api.Memory) error
	ready() bool
	close() error
}

// Wazero may close a module inside a Host call (park/cancel), before Wasm
// unwinds. Keep mmap-backed stack/data alive until the owning call returns.
type deferredCOWFree struct{ cowMemory }

func (m deferredCOWFree) Allocate(_, _ uint64) experimental.LinearMemory { return m }
func (m deferredCOWFree) Free()                                          {}

type cowGuest struct {
	api.Module
	memory cowMemory
}

func (g *cowGuest) Close(ctx context.Context) error {
	err := g.Module.Close(ctx)
	g.memory.Free()
	return err
}

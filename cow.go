package pysolate

import (
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

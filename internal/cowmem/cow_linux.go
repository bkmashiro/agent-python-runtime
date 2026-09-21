//go:build linux

package cowmem

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental"
	"golang.org/x/sys/unix"
)

const wasmPageSize = 65536

var cowPageSize = unix.Getpagesize()

type linuxCOW struct {
	fd        int
	imageSize uint64
	closed    bool
}

type linuxCOWMemory struct {
	mapping []byte
	max     int
	mu      sync.Mutex
}

func New() (Runtime, error) {
	fd, err := unix.MemfdCreate("pysolate-spine-cow", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		return nil, fmt.Errorf("create COW image memfd: %w", err)
	}
	return &linuxCOW{fd: fd}, nil
}

func (c *linuxCOW) Allocator(def api.MemoryDefinition) (Memory, error) {
	if def == nil {
		return nil, errors.New("COW needs an exported memory named memory")
	}
	maximum, bounded := def.Max()
	if !bounded {
		return nil, errors.New("COW needs a declared memory maximum")
	}
	return newLinuxCOWMemory(uint64(maximum) * wasmPageSize)
}

func newLinuxCOWMemory(maximum uint64) (*linuxCOWMemory, error) {
	if maximum == 0 || maximum > uint64(maxInt()) {
		return nil, fmt.Errorf("invalid COW memory maximum: %d", maximum)
	}
	mapping, err := unix.Mmap(-1, 0, int(maximum), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_PRIVATE|unix.MAP_ANONYMOUS)
	if err != nil {
		return nil, fmt.Errorf("reserve COW linear memory (%d bytes): %w", maximum, err)
	}
	return &linuxCOWMemory{mapping: mapping, max: int(maximum)}, nil
}

// The one-memory Guest gets the range reserved before InstantiateModule.
func (m *linuxCOWMemory) Allocate(_, _ uint64) experimental.LinearMemory {
	return m
}

func (m *linuxCOWMemory) Reallocate(size uint64) []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.mapping == nil {
		return nil
	}
	if size > uint64(m.max) || size > uint64(maxInt()) {
		return nil
	}
	return m.mapping[:int(size):m.max]
}

func (m *linuxCOWMemory) Free() {
	// Wazero's allocator hook has no error return.
	_ = m.release()
}

func (m *linuxCOWMemory) release() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.mapping != nil {
		if err := unix.Munmap(m.mapping); err != nil {
			return err
		}
		m.mapping = nil
	}
	return nil
}

func (c *linuxCOW) Capture(memory api.Memory) error {
	if memory == nil || memory.Size() == 0 {
		return errors.New("cannot capture empty COW memory")
	}
	data, ok := memory.Read(0, memory.Size())
	if !ok {
		return errors.New("cannot read initialized Guest memory for COW image")
	}
	return c.captureBytes(data)
}

// captureBytes writes directly from the allocator-backed mapping into the
// sealed memfd. It deliberately does not retain a Go-owned image copy.
func (c *linuxCOW) captureBytes(data []byte) error {
	if len(data) == 0 || len(data)%cowPageSize != 0 || len(data)%wasmPageSize != 0 {
		return fmt.Errorf("COW image size must be Wasm-page aligned: %d", len(data))
	}
	if c.closed {
		return errors.New("COW image is closed")
	}
	if c.imageSize != 0 {
		return errors.New("COW image already captured")
	}
	if err := unix.Ftruncate(c.fd, int64(len(data))); err != nil {
		return fmt.Errorf("size COW image: %w", err)
	}
	for offset := 0; offset < len(data); {
		n, err := unix.Pwrite(c.fd, data[offset:], int64(offset))
		if err != nil {
			return fmt.Errorf("write COW image: %w", err)
		}
		if n == 0 {
			return errors.New("short write while creating COW image")
		}
		offset += n
	}
	seals := unix.F_SEAL_SHRINK | unix.F_SEAL_GROW | unix.F_SEAL_WRITE | unix.F_SEAL_SEAL
	if _, err := unix.FcntlInt(uintptr(c.fd), unix.F_ADD_SEALS, seals); err != nil {
		return fmt.Errorf("seal COW image: %w", err)
	}
	c.imageSize = uint64(len(data))
	return nil
}

func (c *linuxCOW) Ready() bool {
	return !c.closed && c.imageSize != 0
}

func (c *linuxCOW) Attach(memory api.Memory) error {
	if memory == nil {
		return errors.New("cannot attach COW image to nil memory")
	}
	if uint64(memory.Size()) < c.imageSize {
		pages := uint32((c.imageSize - uint64(memory.Size())) / wasmPageSize)
		if _, ok := memory.Grow(pages); !ok {
			return errors.New("cannot grow memory to COW image")
		}
	}
	data, ok := memory.Read(0, memory.Size())
	if !ok {
		return errors.New("cannot inspect Guest memory for COW attach")
	}
	return c.attachBytes(data)
}

// attachBytes replaces exactly the current mapping range. MAP_PRIVATE keeps
// the sealed file pages shared until a Guest write faults a page private.
func (c *linuxCOW) attachBytes(data []byte) error {
	if c.closed {
		return errors.New("COW image is closed")
	}
	if c.imageSize == 0 || uint64(len(data)) != c.imageSize {
		return fmt.Errorf("COW memory shape changed: got %d, want %d", len(data), c.imageSize)
	}
	if len(data) == 0 || uintptr(unsafe.Pointer(unsafe.SliceData(data)))%uintptr(cowPageSize) != 0 {
		return errors.New("COW memory address is not page-aligned")
	}
	address := unsafe.Pointer(unsafe.SliceData(data))
	mapped, err := unix.MmapPtr(c.fd, 0, address, uintptr(len(data)), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_PRIVATE|unix.MAP_FIXED)
	if err != nil {
		return fmt.Errorf("map sealed COW image at existing address: %w", err)
	}
	if mapped != address {
		_ = unix.MunmapPtr(mapped, uintptr(len(data)))
		return errors.New("COW mapping address changed")
	}
	runtime.KeepAlive(data)
	return nil
}

func (c *linuxCOW) Close() error {
	if c.closed {
		return nil
	}
	c.closed = true
	if err := unix.Close(c.fd); err != nil {
		return fmt.Errorf("close COW image: %w", err)
	}
	c.fd = -1
	return nil
}

func maxInt() int {
	return int(^uint(0) >> 1)
}

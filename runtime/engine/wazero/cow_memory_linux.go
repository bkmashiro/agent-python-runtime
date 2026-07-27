//go:build linux

package wazero

import (
	"errors"
	"fmt"
	"sync"

	"github.com/tetratelabs/wazero/experimental"
	"golang.org/x/sys/unix"
)

var (
	errCOWImageClosed       = errors.New("COW image is closed")
	errCOWImageInUse        = errors.New("COW image still has live mappings")
	errCOWAllocationShape   = errors.New("COW allocation shape does not match image")
	errCOWAllocatorConsumed = errors.New("COW allocator already allocated memory")
)

const wasmLinearPageSize = 64 * 1024

type cowImage struct {
	mu       sync.Mutex
	fd       int
	size     uint64
	mappings int
	closed   bool
}

func newCOWImage(baseline []byte) (*cowImage, error) {
	if len(baseline) == 0 || len(baseline)%wasmLinearPageSize != 0 {
		return nil, errors.New("COW image baseline must contain whole Wasm pages")
	}
	fd, err := unix.MemfdCreate("apyrun-cow-image", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		return nil, fmt.Errorf("create COW memfd: %w", err)
	}
	failed := true
	defer func() {
		if failed {
			_ = unix.Close(fd)
		}
	}()
	if err := unix.Ftruncate(fd, int64(len(baseline))); err != nil {
		return nil, fmt.Errorf("size COW memfd: %w", err)
	}
	for written := 0; written < len(baseline); {
		n, err := unix.Pwrite(fd, baseline[written:], int64(written))
		if err != nil {
			return nil, fmt.Errorf("write COW baseline: %w", err)
		}
		if n == 0 {
			return nil, errors.New("write COW baseline made no progress")
		}
		written += n
	}
	seals := unix.F_SEAL_SEAL | unix.F_SEAL_SHRINK | unix.F_SEAL_GROW | unix.F_SEAL_WRITE
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_ADD_SEALS, seals); err != nil {
		return nil, fmt.Errorf("seal COW memfd: %w", err)
	}
	failed = false
	return &cowImage{fd: fd, size: uint64(len(baseline))}, nil
}

func (image *cowImage) newAllocator() *cowAllocator {
	return &cowAllocator{image: image}
}

func (image *cowImage) mapPrivate() (*cowLinearMemory, error) {
	image.mu.Lock()
	defer image.mu.Unlock()
	if image.closed {
		return nil, errCOWImageClosed
	}
	if image.size > uint64(^uint(0)>>1) {
		return nil, errCOWAllocationShape
	}
	buffer, err := unix.Mmap(image.fd, 0, int(image.size), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_PRIVATE)
	if err != nil {
		return nil, fmt.Errorf("map private COW memory: %w", err)
	}
	image.mappings++
	return &cowLinearMemory{image: image, buffer: buffer, max: image.size}, nil
}

func (image *cowImage) releaseMapping() {
	image.mu.Lock()
	defer image.mu.Unlock()
	if image.mappings > 0 {
		image.mappings--
	}
}

func (image *cowImage) Close() error {
	image.mu.Lock()
	defer image.mu.Unlock()
	if image.closed {
		return nil
	}
	if image.mappings != 0 {
		return errCOWImageInUse
	}
	if err := unix.Close(image.fd); err != nil {
		return fmt.Errorf("close COW image: %w", err)
	}
	image.closed = true
	image.fd = -1
	return nil
}

type cowAllocator struct {
	mu         sync.Mutex
	image      *cowImage
	allocation *cowLinearMemory
	err        error
}

var _ experimental.MemoryAllocator = (*cowAllocator)(nil)

func (allocator *cowAllocator) Allocate(capacity, maximum uint64) experimental.LinearMemory {
	allocator.mu.Lock()
	defer allocator.mu.Unlock()
	if allocator.allocation != nil || allocator.err != nil {
		allocator.err = errCOWAllocatorConsumed
		return &failedLinearMemory{}
	}
	if allocator.image == nil || maximum != allocator.image.size || capacity > maximum {
		allocator.err = errCOWAllocationShape
		return &failedLinearMemory{}
	}
	memory, err := allocator.image.mapPrivate()
	if err != nil {
		allocator.err = err
		return &failedLinearMemory{}
	}
	allocator.allocation = memory
	return memory
}

func (allocator *cowAllocator) Allocation() (*cowLinearMemory, error) {
	allocator.mu.Lock()
	defer allocator.mu.Unlock()
	if allocator.err != nil {
		return nil, allocator.err
	}
	if allocator.allocation == nil {
		return nil, errors.New("COW allocator has no allocation")
	}
	return allocator.allocation, nil
}

type failedLinearMemory struct{}

func (*failedLinearMemory) Reallocate(uint64) []byte { return nil }
func (*failedLinearMemory) Free()                    {}

type cowLinearMemory struct {
	mu     sync.Mutex
	image  *cowImage
	buffer []byte
	max    uint64
	freed  bool
}

var _ experimental.LinearMemory = (*cowLinearMemory)(nil)

func (memory *cowLinearMemory) Reallocate(size uint64) []byte {
	memory.mu.Lock()
	defer memory.mu.Unlock()
	if memory.freed || size > memory.max {
		return nil
	}
	return memory.buffer[:int(size)]
}

func (memory *cowLinearMemory) Reset() error {
	memory.mu.Lock()
	defer memory.mu.Unlock()
	if memory.freed {
		return errors.New("reset freed COW memory")
	}
	if err := unix.Madvise(memory.buffer, unix.MADV_DONTNEED); err != nil {
		return fmt.Errorf("discard private COW pages: %w", err)
	}
	return nil
}

func (memory *cowLinearMemory) Free() {
	_ = memory.close()
}

func (memory *cowLinearMemory) close() error {
	memory.mu.Lock()
	defer memory.mu.Unlock()
	if memory.freed {
		return nil
	}
	if err := unix.Munmap(memory.buffer); err != nil {
		return fmt.Errorf("unmap private COW memory: %w", err)
	}
	memory.buffer = nil
	memory.freed = true
	memory.image.releaseMapping()
	return nil
}

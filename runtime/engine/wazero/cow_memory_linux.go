//go:build linux

package wazero

import (
	"errors"
	"fmt"
	"os"
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
	mu             sync.Mutex
	fd             int
	baselineSize   uint64
	size           uint64
	allocatedBytes uint64
	pageSizeBytes  uint64
	zeroPages      uint64
	nonZeroPages   uint64
	mappings       int
	closed         bool
}

func pageAllZero(page []byte) bool {
	for _, value := range page {
		if value != 0 {
			return false
		}
	}
	return true
}

func newCOWImage(baseline []byte) (*cowImage, error) {
	return newCOWImageWithMaximum(baseline, uint64(len(baseline)))
}

func newCOWImageWithMaximum(baseline []byte, maximum uint64) (*cowImage, error) {
	if len(baseline) == 0 || len(baseline)%wasmLinearPageSize != 0 {
		return nil, errors.New("COW image baseline must contain whole Wasm pages")
	}
	if maximum < uint64(len(baseline)) || maximum%wasmLinearPageSize != 0 || maximum > uint64(^uint(0)>>1) {
		return nil, errors.New("COW image maximum must be a whole Wasm page range containing the baseline")
	}
	pageSize := os.Getpagesize()
	var zeroPages, nonZeroPages uint64
	for start := 0; start < len(baseline); start += pageSize {
		end := min(start+pageSize, len(baseline))
		if pageAllZero(baseline[start:end]) {
			zeroPages++
		} else {
			nonZeroPages++
		}
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
	if err := unix.Ftruncate(fd, int64(maximum)); err != nil {
		return nil, fmt.Errorf("size COW memfd: %w", err)
	}
	for start := 0; start < len(baseline); start += pageSize {
		end := min(start+pageSize, len(baseline))
		page := baseline[start:end]
		if pageAllZero(page) {
			continue
		}
		for written := 0; written < len(page); {
			n, err := unix.Pwrite(fd, page[written:], int64(start+written))
			if err != nil {
				return nil, fmt.Errorf("write COW baseline: %w", err)
			}
			if n == 0 {
				return nil, errors.New("write COW baseline made no progress")
			}
			written += n
		}
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return nil, fmt.Errorf("stat COW memfd: %w", err)
	}
	allocatedBytes := uint64(stat.Blocks) * 512
	seals := unix.F_SEAL_SEAL | unix.F_SEAL_SHRINK | unix.F_SEAL_GROW | unix.F_SEAL_WRITE
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_ADD_SEALS, seals); err != nil {
		return nil, fmt.Errorf("seal COW memfd: %w", err)
	}
	failed = false
	return &cowImage{
		fd: fd, baselineSize: uint64(len(baseline)), size: maximum,
		allocatedBytes: allocatedBytes, pageSizeBytes: uint64(pageSize), zeroPages: zeroPages, nonZeroPages: nonZeroPages,
	}, nil
}

func (image *cowImage) newAllocator() *cowAllocator {
	return &cowAllocator{image: image}
}

func (image *cowImage) preparedImageState() PreparedImageState {
	image.mu.Lock()
	defer image.mu.Unlock()
	if image.closed {
		return PreparedImageState{}
	}
	return PreparedImageState{
		Available: true, BaselineBytes: image.baselineSize, VirtualBytes: image.size,
		AllocatedBytes: image.allocatedBytes, PageSizeBytes: image.pageSizeBytes,
		ZeroPages: image.zeroPages, NonZeroPages: image.nonZeroPages,
		SparsePotentialBytes: image.size - image.baselineSize,
	}
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

func (allocator *cowAllocator) releaseAllocation() {
	allocator.mu.Lock()
	allocation := allocator.allocation
	allocator.mu.Unlock()
	if allocation != nil {
		allocation.Free()
	}
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
	size   uint64
	freed  bool
}

var _ experimental.LinearMemory = (*cowLinearMemory)(nil)

func (memory *cowLinearMemory) Reallocate(size uint64) []byte {
	memory.mu.Lock()
	defer memory.mu.Unlock()
	if memory.freed || size > memory.max {
		return nil
	}
	memory.size = size
	return memory.buffer[:int(size)]
}

func (memory *cowLinearMemory) advise(advice int) (uint64, error) {
	memory.mu.Lock()
	defer memory.mu.Unlock()
	if memory.freed || memory.size == 0 {
		return 0, errCOWAllocationShape
	}
	view := memory.buffer[:int(memory.size)]
	if err := unix.Madvise(view, advice); err != nil {
		return memory.size, err
	}
	return memory.size, nil
}

func (memory *cowLinearMemory) restoreBaselineBeforeServe() error {
	memory.mu.Lock()
	defer memory.mu.Unlock()
	if memory.freed {
		return errors.New("restore freed COW memory")
	}
	if err := unix.Madvise(memory.buffer, unix.MADV_DONTNEED); err != nil {
		return fmt.Errorf("restore sealed COW baseline before serve: %w", err)
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

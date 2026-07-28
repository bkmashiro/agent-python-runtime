//go:build linux

package wazero

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"unsafe"

	"github.com/tetratelabs/wazero/experimental"
	"golang.org/x/sys/unix"
)

var (
	errCOWImageClosed       = errors.New("COW image is closed")
	errCOWImageInUse        = errors.New("COW image still has live mappings")
	errCOWImageIdentity     = errors.New("COW image identity or seals changed")
	errCOWAllocationShape   = errors.New("COW allocation shape does not match image")
	errCOWAllocatorConsumed = errors.New("COW allocator already allocated memory")
)

const wasmLinearPageSize = 64 * 1024

const requiredCOWSeals = unix.F_SEAL_SEAL | unix.F_SEAL_SHRINK | unix.F_SEAL_GROW | unix.F_SEAL_WRITE

type cowImage struct {
	mu           sync.Mutex
	fd           int
	size         uint64
	device       uint64
	inode        uint64
	mappings     int
	closed       bool
	pageSize     uint64
	zeroPages    uint64
	nonZeroPages uint64
}

func newCOWImage(baseline []byte) (*cowImage, error) {
	if len(baseline) == 0 || len(baseline)%wasmLinearPageSize != 0 {
		return nil, errors.New("COW image baseline must contain whole Wasm pages")
	}
	pageSize := unix.Getpagesize()
	zeroPage := make([]byte, pageSize)
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
	var zeroPages, nonZeroPages uint64
	nonZeroRunStart := -1
	flushNonZeroRun := func(end int) error {
		if nonZeroRunStart < 0 {
			return nil
		}
		for written := nonZeroRunStart; written < end; {
			n, err := unix.Pwrite(fd, baseline[written:end], int64(written))
			if err != nil {
				return fmt.Errorf("write COW baseline extent: %w", err)
			}
			if n == 0 {
				return errors.New("write COW baseline extent made no progress")
			}
			written += n
		}
		nonZeroRunStart = -1
		return nil
	}
	for offset := 0; offset < len(baseline); offset += pageSize {
		end := offset + pageSize
		if end > len(baseline) {
			end = len(baseline)
		}
		if bytes.Equal(baseline[offset:end], zeroPage[:end-offset]) {
			zeroPages++
			if err := flushNonZeroRun(offset); err != nil {
				return nil, err
			}
		} else {
			nonZeroPages++
			if nonZeroRunStart < 0 {
				nonZeroRunStart = offset
			}
		}
	}
	if err := flushNonZeroRun(len(baseline)); err != nil {
		return nil, err
	}
	if _, err := unix.FcntlInt(uintptr(fd), unix.F_ADD_SEALS, requiredCOWSeals); err != nil {
		return nil, fmt.Errorf("seal COW memfd: %w", err)
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return nil, fmt.Errorf("stat COW memfd: %w", err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Size != int64(len(baseline)) {
		return nil, fmt.Errorf("%w: invalid initial file shape", errCOWImageIdentity)
	}
	failed = false
	return &cowImage{
		fd: fd, size: uint64(len(baseline)), device: uint64(stat.Dev), inode: stat.Ino,
		pageSize: uint64(pageSize), zeroPages: zeroPages, nonZeroPages: nonZeroPages,
	}, nil
}

func (image *cowImage) preparedImageState() PreparedImageState {
	image.mu.Lock()
	defer image.mu.Unlock()
	if image.closed {
		return PreparedImageState{}
	}
	var stat unix.Stat_t
	if err := unix.Fstat(image.fd, &stat); err != nil {
		return PreparedImageState{}
	}
	var allocated uint64
	if stat.Blocks > 0 {
		allocated = uint64(stat.Blocks) * 512
	}
	return PreparedImageState{
		Available: true, VirtualBytes: image.size, AllocatedBytes: allocated,
		PageSizeBytes: image.pageSize, ZeroPages: image.zeroPages, NonZeroPages: image.nonZeroPages,
		SparsePotentialBytes: image.zeroPages * image.pageSize,
	}
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
	if err := image.verifyIdentityLocked(); err != nil {
		return nil, err
	}
	if image.size > uint64(^uint(0)>>1) {
		return nil, errCOWAllocationShape
	}
	buffer, err := unix.Mmap(-1, 0, int(image.size), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_PRIVATE|unix.MAP_ANON)
	if err != nil {
		return nil, fmt.Errorf("map fresh private linear memory: %w", err)
	}
	image.mappings++
	return &cowLinearMemory{image: image, buffer: buffer, max: image.size}, nil
}

func (image *cowImage) attachPrivateAt(buffer []byte) error {
	image.mu.Lock()
	defer image.mu.Unlock()
	if image.closed {
		return errCOWImageClosed
	}
	if err := image.verifyIdentityLocked(); err != nil {
		return err
	}
	if len(buffer) == 0 || uint64(len(buffer)) != image.size {
		return errCOWAllocationShape
	}
	address := uintptr(unsafe.Pointer(&buffer[0]))
	mapped, _, errno := unix.Syscall6(
		unix.SYS_MMAP,
		address,
		uintptr(len(buffer)),
		uintptr(unix.PROT_READ|unix.PROT_WRITE),
		uintptr(unix.MAP_PRIVATE|unix.MAP_FIXED),
		uintptr(image.fd),
		0,
	)
	if errno != 0 {
		return fmt.Errorf("attach private COW image: %w", errno)
	}
	if mapped != address {
		return errors.New("attach private COW image changed linear-memory address")
	}
	return nil
}

func (image *cowImage) verifyIdentityLocked() error {
	var stat unix.Stat_t
	if err := unix.Fstat(image.fd, &stat); err != nil {
		return fmt.Errorf("%w: stat: %v", errCOWImageIdentity, err)
	}
	if uint64(stat.Dev) != image.device || stat.Ino != image.inode || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Size != int64(image.size) {
		return errCOWImageIdentity
	}
	seals, err := unix.FcntlInt(uintptr(image.fd), unix.F_GET_SEALS, 0)
	if err != nil || seals&requiredCOWSeals != requiredCOWSeals {
		return fmt.Errorf("%w: seals", errCOWImageIdentity)
	}
	return nil
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
	mu       sync.Mutex
	image    *cowImage
	buffer   []byte
	max      uint64
	attached bool
	freed    bool
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
	if !memory.attached {
		if err := memory.image.attachPrivateAt(memory.buffer); err != nil {
			return err
		}
		memory.attached = true
		return nil
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

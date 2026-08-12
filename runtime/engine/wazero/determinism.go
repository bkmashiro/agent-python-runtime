package wazero

import (
	"crypto/sha256"
	"encoding/binary"
	"sync"
)

type deterministicReader struct {
	mu      sync.Mutex
	seed    []byte
	counter uint64
	block   []byte
}

func newDeterministicReader(seed []byte) *deterministicReader {
	return &deterministicReader{seed: append([]byte(nil), seed...)}
}

func (reader *deterministicReader) Read(destination []byte) (int, error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	written := 0
	for written < len(destination) {
		if len(reader.block) == 0 {
			hash := sha256.New()
			hash.Write([]byte("pysolate-deterministic-random-v1"))
			hash.Write([]byte{0})
			hash.Write(reader.seed)
			hash.Write([]byte{0})
			var counter [8]byte
			binary.BigEndian.PutUint64(counter[:], reader.counter)
			hash.Write(counter[:])
			reader.counter++
			reader.block = hash.Sum(nil)
		}
		count := copy(destination[written:], reader.block)
		written += count
		reader.block = reader.block[count:]
	}
	return written, nil
}

type deterministicClock struct {
	mu        sync.Mutex
	wall      int64
	monotonic int64
	step      int64
}

func newDeterministicClock(wall, monotonic, step int64) *deterministicClock {
	return &deterministicClock{wall: wall, monotonic: monotonic, step: step}
}

func (clock *deterministicClock) walltime() (seconds int64, nanoseconds int32) {
	clock.mu.Lock()
	value := clock.wall
	clock.wall += clock.step
	clock.mu.Unlock()
	return value / 1_000_000_000, int32(value % 1_000_000_000)
}

func (clock *deterministicClock) nanotime() int64 {
	clock.mu.Lock()
	value := clock.monotonic
	clock.monotonic += clock.step
	clock.mu.Unlock()
	return value
}

func (clock *deterministicClock) nanosleep(nanoseconds int64) {
	if nanoseconds <= 0 {
		return
	}
	clock.mu.Lock()
	steps := (nanoseconds + clock.step - 1) / clock.step
	clock.monotonic += steps * clock.step
	clock.wall += steps * clock.step
	clock.mu.Unlock()
}

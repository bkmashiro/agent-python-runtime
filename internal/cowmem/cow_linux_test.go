//go:build linux

package cowmem

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

func TestLinuxCOWMappingSealsAddressAndIsolation(t *testing.T) {
	resource, err := New()
	if err != nil {
		t.Fatal(err)
	}
	cow := resource.(*linuxCOW)
	defer cow.Close()

	seed := testCOWMemory(t, 4*wasmPageSize)
	baseline := seed.Reallocate(2 * wasmPageSize)
	if baseline == nil {
		t.Fatal("seed allocation failed")
	}
	for i := range baseline {
		baseline[i] = byte((i / cowPageSize) + 17)
	}
	if err := cow.captureBytes(baseline); err != nil {
		t.Fatal(err)
	}
	seals, err := unix.FcntlInt(uintptr(cow.fd), unix.F_GET_SEALS, 0)
	if err != nil {
		t.Fatal(err)
	}
	wantSeals := unix.F_SEAL_SHRINK | unix.F_SEAL_GROW | unix.F_SEAL_WRITE | unix.F_SEAL_SEAL
	if seals&wantSeals != wantSeals {
		t.Fatalf("seals=%#x want all %#x", seals, wantSeals)
	}
	seed.Free()

	first := testCOWMemory(t, 4*wasmPageSize)
	second := testCOWMemory(t, 4*wasmPageSize)
	defer first.Free()
	defer second.Free()
	firstBytes := first.Reallocate(2 * wasmPageSize)
	secondBytes := second.Reallocate(2 * wasmPageSize)
	firstAddr := sliceAddress(firstBytes)
	if err := cow.attachBytes(firstBytes); err != nil {
		t.Fatal(err)
	}
	if got := sliceAddress(firstBytes); got != firstAddr {
		t.Fatalf("mapping address changed: before=%#x after=%#x", firstAddr, got)
	}
	if err := cow.attachBytes(secondBytes); err != nil {
		t.Fatal(err)
	}
	if firstBytes[0] != 17 || secondBytes[0] != 17 || secondBytes[cowPageSize] != 18 {
		t.Fatalf("attached mapping did not expose sealed baseline")
	}
	sharedClean, sharedDirty := smapsForAddress(firstAddr)
	if smapsKB(sharedClean)+smapsKB(sharedDirty) == 0 {
		t.Fatalf("smaps showed no shared clean/dirty pages before private writes")
	}
	t.Logf("smaps shared baseline before writes: Shared_Clean=%s Shared_Dirty=%s", sharedClean, sharedDirty)
	firstBytes[0] = 99
	firstBytes[cowPageSize] = 100
	if secondBytes[0] != 17 || secondBytes[cowPageSize] != 18 {
		t.Fatalf("private write leaked across mappings: second=%d,%d", secondBytes[0], secondBytes[cowPageSize])
	}
	sealed := make([]byte, 2*wasmPageSize)
	n, err := unix.Pread(cow.fd, sealed, 0)
	if err != nil || n != len(sealed) {
		t.Fatalf("read sealed baseline: n=%d err=%v", n, err)
	}
	if sealed[0] != 17 || sealed[cowPageSize] != 18 {
		t.Fatalf("private writes changed sealed baseline")
	}
	grown := first.Reallocate(4 * wasmPageSize)
	if sliceAddress(grown) != firstAddr || grown[2*wasmPageSize] != 0 {
		t.Fatalf("fixed-range growth moved or dirtied the anonymous tail")
	}
	if err := first.release(); err != nil {
		t.Fatalf("unmap COW prefix and anonymous tail: %v", err)
	}
	if first.Reallocate(wasmPageSize) != nil {
		t.Fatal("released mapping remains usable")
	}

}

func testCOWMemory(t *testing.T, maximum uint64) *linuxCOWMemory {
	t.Helper()
	m, err := newLinuxCOWMemory(maximum)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Free)
	return m
}

func TestLinuxCOWAllocationFailureAndFree(t *testing.T) {
	if _, err := newLinuxCOWMemory(0); err == nil {
		t.Fatal("empty reservation must fail")
	}
	// An impossible address-space request exercises the real mmap error path.
	if m, err := newLinuxCOWMemory(uint64(maxInt()) & ^uint64(wasmPageSize-1)); err == nil {
		m.Free()
		t.Fatal("impossible reservation unexpectedly succeeded")
	}
	m := testCOWMemory(t, 2*wasmPageSize)
	data := m.Reallocate(2 * wasmPageSize)
	address := sliceAddress(data)
	if err := m.release(); err != nil {
		t.Fatalf("munmap failed: %v", err)
	}
	m.Free()
	if m.Reallocate(wasmPageSize) != nil {
		t.Fatal("freed memory remains available")
	}
	clean, dirty := smapsForAddress(address)
	if clean != "" || dirty != "" {
		maps, _ := os.ReadFile("/proc/self/maps")
		for _, line := range strings.Split(string(maps), "\n") {
			var start, end uintptr
			if _, err := fmt.Sscanf(line, "%x-%x", &start, &end); err == nil && address >= start && address < end {
				t.Logf("freed address %#x currently in %s", address, line)
			}
		}
		t.Log("address was reused after successful munmap; this is permitted")
	}
}

func sliceAddress(data []byte) uintptr {
	return uintptr(unsafe.Pointer(unsafe.SliceData(data)))
}

func smapsForAddress(address uintptr) (sharedClean, sharedDirty string) {
	file, err := os.Open("/proc/self/smaps")
	if err != nil {
		return "", ""
	}
	defer file.Close()
	var inRange bool
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if fields := strings.Fields(line); len(fields) > 0 && strings.Contains(fields[0], "-") {
			var start, end uintptr
			if _, err := fmt.Sscanf(fields[0], "%x-%x", &start, &end); err == nil {
				inRange = address >= start && address < end
			}
			continue
		}
		if !inRange {
			continue
		}
		if strings.HasPrefix(line, "Shared_Clean:") {
			sharedClean = strings.TrimSpace(strings.TrimPrefix(line, "Shared_Clean:"))
		}
		if strings.HasPrefix(line, "Shared_Dirty:") {
			sharedDirty = strings.TrimSpace(strings.TrimPrefix(line, "Shared_Dirty:"))
		}
		if sharedClean != "" && sharedDirty != "" {
			return sharedClean, sharedDirty
		}
	}
	return sharedClean, sharedDirty
}

func smapsKB(value string) int {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return 0
	}
	kb, _ := strconv.Atoi(fields[0])
	return kb
}

func TestLinuxCOWFreeWaitsForCallOwner(t *testing.T) {
	memory, err := newLinuxCOWMemory(2 * wasmPageSize)
	if err != nil {
		t.Fatal(err)
	}
	deferred := deferredFree{Memory: memory}
	linear := deferred.Allocate(wasmPageSize, 2*wasmPageSize)
	data := linear.Reallocate(wasmPageSize)
	linear.Free() // A Host import can logically close the module before Wasm unwinds.
	if memory.mapping == nil {
		t.Fatal("module close released an active mapping")
	}
	data[0] = 42
	memory.Free() // The outer Call owner releases it after return.
	if memory.mapping != nil {
		t.Fatal("call owner leaked mapping")
	}
	memory.Free()
}

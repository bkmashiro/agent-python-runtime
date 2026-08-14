//go:build linux

package wazero

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	goruntime "runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

type coldMappingMetrics struct {
	RSSKiB          uint64 `json:"rss_kib"`
	PSSKiB          uint64 `json:"pss_kib"`
	SharedCleanKiB  uint64 `json:"shared_clean_kib"`
	PrivateDirtyKiB uint64 `json:"private_dirty_kib"`
	SwapKiB         uint64 `json:"swap_kib"`
}

type coldResourceObservation struct {
	Mode         string             `json:"mode"`
	Bytes        uint64             `json:"bytes"`
	Before       coldMappingMetrics `json:"before"`
	After        coldMappingMetrics `json:"after"`
	AfterResume  coldMappingMetrics `json:"after_resume"`
	ResumeMicros int64              `json:"resume_micros"`
}

func TestColdIOResourceObservation(t *testing.T) {
	mode := os.Getenv("PYSOLATE_COLD_IO_OBSERVE")
	if mode == "" {
		t.Skip("set PYSOLATE_COLD_IO_OBSERVE=control|cold|pageout")
	}
	if mode != "control" && mode != "cold" && mode != "pageout" {
		t.Fatal("invalid observation mode")
	}
	const size = 128 << 20
	image, err := newCOWImage(make([]byte, size))
	if err != nil {
		t.Fatal(err)
	}
	memory := image.newAllocator().Allocate(size, size).(*cowLinearMemory)
	view := memory.Reallocate(size)
	for offset := 0; offset < 96<<20; offset += os.Getpagesize() {
		view[offset] = 37
	}
	goruntime.GC()
	before, err := readColdMappingMetrics()
	if err != nil {
		t.Fatal(err)
	}
	if mode == "cold" || mode == "pageout" {
		if _, err := memory.advise(unix.MADV_COLD); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "pageout" {
		if _, err := memory.advise(unix.MADV_PAGEOUT); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(500 * time.Millisecond)
	after, err := readColdMappingMetrics()
	if err != nil {
		t.Fatal(err)
	}
	resumeStarted := time.Now()
	var sum uint64
	for offset := 0; offset < 96<<20; offset += os.Getpagesize() {
		sum += uint64(view[offset])
	}
	resumeMicros := time.Since(resumeStarted).Microseconds()
	if sum != 37*uint64((96<<20)/os.Getpagesize()) {
		t.Fatal("private dirty state did not survive observation")
	}
	afterResume, err := readColdMappingMetrics()
	if err != nil {
		t.Fatal(err)
	}
	observation := coldResourceObservation{
		Mode: mode, Bytes: size, Before: before, After: after,
		AfterResume: afterResume, ResumeMicros: resumeMicros,
	}
	encoded, err := json.Marshal(observation)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("COLD_IO_OBSERVATION %s\n", encoded)
	memory.Free()
	if err := image.Close(); err != nil {
		t.Fatal(err)
	}
}

func readColdMappingMetrics() (coldMappingMetrics, error) {
	file, err := os.Open("/proc/self/smaps")
	if err != nil {
		return coldMappingMetrics{}, err
	}
	defer file.Close()
	metrics := coldMappingMetrics{}
	active := false
	found := false
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) > 0 && strings.Contains(fields[0], "-") {
			active = strings.Contains(line, "memfd:apyrun-cow-image")
			found = found || active
			continue
		}
		if !active || len(fields) < 2 {
			continue
		}
		var target *uint64
		switch fields[0] {
		case "Rss:":
			target = &metrics.RSSKiB
		case "Pss:":
			target = &metrics.PSSKiB
		case "Shared_Clean:":
			target = &metrics.SharedCleanKiB
		case "Private_Dirty:":
			target = &metrics.PrivateDirtyKiB
		case "Swap:":
			target = &metrics.SwapKiB
		default:
			continue
		}
		value, parseErr := strconv.ParseUint(fields[1], 10, 64)
		if parseErr != nil {
			return coldMappingMetrics{}, parseErr
		}
		*target += value
	}
	if err := scanner.Err(); err != nil {
		return coldMappingMetrics{}, err
	}
	if !found {
		return coldMappingMetrics{}, fmt.Errorf("COW mapping absent from smaps")
	}
	return metrics, nil
}

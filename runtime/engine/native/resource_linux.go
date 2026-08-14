//go:build linux

package native

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type resourceAggregate struct {
	Samples           uint64
	MemoryCurrentPeak uint64
	PSSPeak           uint64
	PrivateDirtyPeak  uint64
	ReadBytes         uint64
	WriteBytes        uint64
	PidsPeak          uint64
}

func sampleCgroup(ctx context.Context, cgroup string) <-chan resourceAggregate {
	result := make(chan resourceAggregate, 1)
	go func() {
		defer close(result)
		var aggregate resourceAggregate
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			sampleResources(cgroup, &aggregate)
			select {
			case <-ctx.Done():
				sampleResources(cgroup, &aggregate)
				result <- aggregate
				return
			case <-ticker.C:
			}
		}
	}()
	return result
}

func sampleResources(cgroup string, aggregate *resourceAggregate) {
	memory, err := readUint(filepath.Join(cgroup, "memory.current"))
	if err != nil {
		return
	}
	aggregate.Samples++
	if memory > aggregate.MemoryCurrentPeak {
		aggregate.MemoryCurrentPeak = memory
	}
	if pids, err := readUint(filepath.Join(cgroup, "pids.current")); err == nil && pids > aggregate.PidsPeak {
		aggregate.PidsPeak = pids
	}
	if raw, err := os.ReadFile(filepath.Join(cgroup, "io.stat")); err == nil {
		var readBytes, writeBytes uint64
		for _, field := range strings.Fields(string(raw)) {
			if strings.HasPrefix(field, "rbytes=") {
				value, _ := strconv.ParseUint(strings.TrimPrefix(field, "rbytes="), 10, 64)
				readBytes += value
			}
			if strings.HasPrefix(field, "wbytes=") {
				value, _ := strconv.ParseUint(strings.TrimPrefix(field, "wbytes="), 10, 64)
				writeBytes += value
			}
		}
		if readBytes > aggregate.ReadBytes {
			aggregate.ReadBytes = readBytes
		}
		if writeBytes > aggregate.WriteBytes {
			aggregate.WriteBytes = writeBytes
		}
	}
	pidsRaw, err := os.ReadFile(filepath.Join(cgroup, "cgroup.procs"))
	if err != nil {
		return
	}
	var pss, privateDirty uint64
	for _, pid := range strings.Fields(string(pidsRaw)) {
		file, err := os.Open(filepath.Join("/proc", pid, "smaps_rollup"))
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) < 2 {
				continue
			}
			value, _ := strconv.ParseUint(fields[1], 10, 64)
			switch fields[0] {
			case "Pss:":
				pss += value * 1024
			case "Private_Dirty:":
				privateDirty += value * 1024
			}
		}
		_ = file.Close()
	}
	if pss > aggregate.PSSPeak {
		aggregate.PSSPeak = pss
	}
	if privateDirty > aggregate.PrivateDirtyPeak {
		aggregate.PrivateDirtyPeak = privateDirty
	}
}

func readUint(path string) (uint64, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
}

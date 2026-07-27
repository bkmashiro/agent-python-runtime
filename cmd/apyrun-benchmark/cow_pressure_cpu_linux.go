//go:build linux

package main

import (
	"fmt"

	"golang.org/x/sys/unix"
)

type pressureCPUPoint struct {
	userNS   uint64
	systemNS uint64
}

func collectPressureCPU() (pressureCPUPoint, error) {
	var usage unix.Rusage
	if err := unix.Getrusage(unix.RUSAGE_SELF, &usage); err != nil {
		return pressureCPUPoint{}, fmt.Errorf("collect process CPU usage: %w", err)
	}
	return pressureCPUPoint{
		userNS:   uint64(usage.Utime.Sec)*1_000_000_000 + uint64(usage.Utime.Usec)*1_000,
		systemNS: uint64(usage.Stime.Sec)*1_000_000_000 + uint64(usage.Stime.Usec)*1_000,
	}, nil
}

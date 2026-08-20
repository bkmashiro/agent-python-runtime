//go:build darwin || linux

package semanticspeculation

import (
	"runtime"
	"syscall"
)

func phase5ProcessResidentBytes() uint64 {
	var usage syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &usage) != nil || usage.Maxrss <= 0 {
		return 0
	}
	measured := uint64(usage.Maxrss)
	if runtime.GOOS == "linux" {
		measured *= 1024
	}
	return measured
}

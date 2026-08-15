//go:build unix

package workflowbench

import "syscall"

func processCPUTimeNanos() (uint64, bool) {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		return 0, false
	}
	user := uint64(usage.Utime.Sec)*1_000_000_000 + uint64(usage.Utime.Usec)*1_000
	system := uint64(usage.Stime.Sec)*1_000_000_000 + uint64(usage.Stime.Usec)*1_000
	return user + system, true
}

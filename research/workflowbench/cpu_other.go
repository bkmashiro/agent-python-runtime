//go:build !unix

package workflowbench

func processCPUTimeNanos() (uint64, bool) { return 0, false }

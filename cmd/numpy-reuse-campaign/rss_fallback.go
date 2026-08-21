//go:build (darwin && !cgo) || (!darwin && !linux)

package main

import "runtime"

func currentRSSBytes() uint64 {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.Sys
}

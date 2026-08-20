//go:build !darwin && !linux

package semanticspeculation

import "runtime"

func phase5ProcessResidentBytes() uint64 {
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	return memory.Sys
}

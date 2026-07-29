//go:build linux

package scheduler

import (
	"os"
	"time"
)

func NewCurrentCgroupV2MemoryReader() (*CgroupV2MemoryReader, error) {
	membership, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return nil, err
	}
	root, err := DiscoverCgroupV2MemoryRoot(membership, "/sys/fs/cgroup")
	if err != nil {
		return nil, err
	}
	return NewCgroupV2MemoryReader(CgroupV2MemoryReaderConfig{Root: root, Clock: time.Now})
}

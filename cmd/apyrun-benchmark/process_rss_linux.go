//go:build linux

package main

import (
	"fmt"
	"os"
)

func defaultProcessRSSBytes(pid int) (uint64, error) {
	content, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0, err
	}
	return parseLinuxProcessRSSStatus(content)
}

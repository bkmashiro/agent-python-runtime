//go:build linux

package perfdiag

import (
	"os"
	"strings"
	"syscall"
)

// COWStorage measures allocated blocks of the current process's sealed-image
// files, including pages retained while not mapped. It is not process RSS.
func COWStorage() (int, int64, error) {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return 0, 0, err
	}
	count := 0
	var allocated int64
	for _, entry := range entries {
		path := "/proc/self/fd/" + entry.Name()
		name, err := os.Readlink(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return 0, 0, err
		}
		if !strings.Contains(name, "memfd:pysolate-spine-cow") {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			return 0, 0, err
		}
		stat := info.Sys().(*syscall.Stat_t)
		count++
		allocated += stat.Blocks * 512
	}
	return count, allocated, nil
}

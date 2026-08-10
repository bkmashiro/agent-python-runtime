//go:build darwin || linux

package workspacecapsule

import (
	"os"
	"runtime"
)

func openDescriptorCount() (int, bool) {
	path := "/dev/fd"
	if runtime.GOOS == "linux" {
		path = "/proc/self/fd"
	}
	directory, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	names, readErr := directory.Readdirnames(-1)
	closeErr := directory.Close()
	if readErr != nil || closeErr != nil {
		return 0, false
	}
	return len(names), true
}

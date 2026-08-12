//go:build windows

package labstore

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func acquireAdvisoryLock(path string, exclusive bool) (*os.File, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open labstore lock identity: %w", err)
	}
	flags := uint32(windows.LOCKFILE_FAIL_IMMEDIATELY)
	if exclusive {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	var overlapped windows.Overlapped
	if err := windows.LockFileEx(windows.Handle(file.Fd()), flags, 0, 1, 0, &overlapped); err != nil {
		_ = file.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, ErrBusy
		}
		return nil, fmt.Errorf("acquire labstore advisory lock: %w", err)
	}
	return file, nil
}

func releaseAdvisoryLock(file *os.File) error {
	if file == nil {
		return nil
	}
	var overlapped windows.Overlapped
	unlockErr := windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped)
	closeErr := file.Close()
	return errors.Join(unlockErr, closeErr)
}

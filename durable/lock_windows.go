//go:build windows

package durable

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func rejectHardLinkedDatabase(file *os.File, _ os.FileInfo) error {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info); err != nil {
		return fmt.Errorf("inspect durable database links: %w", err)
	}
	if info.NumberOfLinks > 1 {
		return fmt.Errorf("open durable database: hard-linked SQLite files are unsupported")
	}
	return nil
}

func openRunLock(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open durable run lock: %w", err)
	}
	var overlapped windows.Overlapped
	flags := uint32(windows.LOCKFILE_FAIL_IMMEDIATELY | windows.LOCKFILE_EXCLUSIVE_LOCK)
	if err := windows.LockFileEx(windows.Handle(file.Fd()), flags, 0, 1, 0, &overlapped); err != nil {
		_ = file.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, ErrBusy
		}
		return nil, fmt.Errorf("acquire durable run lock: %w", err)
	}
	return file, nil
}

func releaseRunLock(file *os.File) error {
	if file == nil {
		return nil
	}
	var overlapped windows.Overlapped
	unlockErr := windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped)
	closeErr := file.Close()
	return errors.Join(unlockErr, closeErr)
}

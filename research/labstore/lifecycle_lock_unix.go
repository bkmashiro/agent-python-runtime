//go:build unix

package labstore

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func acquireAdvisoryLock(path string, exclusive bool) (*os.File, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open labstore lock identity: %w", err)
	}
	operation := unix.LOCK_SH
	if exclusive {
		operation = unix.LOCK_EX
	}
	if err := unix.Flock(int(file.Fd()), operation|unix.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
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
	unlockErr := unix.Flock(int(file.Fd()), unix.LOCK_UN)
	closeErr := file.Close()
	return errors.Join(unlockErr, closeErr)
}

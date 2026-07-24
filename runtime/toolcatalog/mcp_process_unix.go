//go:build !windows

package toolcatalog

import (
	"errors"
	"os/exec"
	"syscall"
	"time"
)

func configureSubprocess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error { return terminateSubprocess(command) }
	command.WaitDelay = 100 * time.Millisecond
}

func terminateSubprocess(command *exec.Cmd) error {
	if command == nil || command.Process == nil {
		return nil
	}
	err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

//go:build windows

package toolcatalog

import (
	"os/exec"
	"time"
)

func configureSubprocess(command *exec.Cmd) {
	command.WaitDelay = 100 * time.Millisecond
}

func terminateSubprocess(command *exec.Cmd) error {
	if command == nil || command.Process == nil {
		return nil
	}
	return command.Process.Kill()
}

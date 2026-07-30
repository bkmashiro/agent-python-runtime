//go:build darwin || linux

package hermesbridge

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"syscall"
)

func ListenUnix(socketPath string) (*net.UnixListener, error) {
	if socketPath == "" || !filepath.IsAbs(socketPath) || filepath.Clean(socketPath) != socketPath || len(socketPath) > 100 {
		return nil, errors.New("Unix socket path must be clean, absolute, and bounded")
	}
	parent := filepath.Dir(socketPath)
	parentInfo, err := os.Lstat(parent)
	if err != nil || !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 || parentInfo.Mode().Perm() != 0o700 {
		return nil, errors.New("Unix socket parent must be a private directory")
	}
	if stat, ok := parentInfo.Sys().(*syscall.Stat_t); !ok || int(stat.Uid) != os.Getuid() {
		return nil, errors.New("Unix socket parent must be owned by the current user")
	}
	if _, err := os.Lstat(socketPath); !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("Unix socket path already exists")
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		return nil, err
	}
	listener.SetUnlinkOnClose(true)
	if err := os.Chmod(socketPath, 0o600); err != nil {
		_ = listener.Close()
		return nil, err
	}
	return listener, nil
}

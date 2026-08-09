package wazero

import (
	cryptorand "crypto/rand"
	"errors"
	"io"

	wazerort "github.com/tetratelabs/wazero"
	experimentalsys "github.com/tetratelabs/wazero/experimental/sys"
	experimentalsysfs "github.com/tetratelabs/wazero/experimental/sysfs"
)

func newModuleConfig(stderr io.Writer, workspaceFS experimentalsys.FS) (wazerort.ModuleConfig, *workspaceGate, error) {
	config := wazerort.NewModuleConfig().
		WithName("").
		WithRandSource(cryptorand.Reader).
		WithSysWalltime().
		WithSysNanotime().
		WithSysNanosleep()
	if stderr != nil {
		config = config.WithStderr(stderr)
	}
	var gate *workspaceGate
	if workspaceFS != nil {
		base := wazerort.NewFSConfig()
		extended, ok := base.(experimentalsysfs.FSConfig)
		if !ok {
			return nil, nil, errors.New("wazero does not support rooted workspace mounts")
		}
		gate = newWorkspaceGate(workspaceFS)
		config = config.WithFSConfig(extended.WithSysFSMount(gate, "workspace"))
	}
	return config, gate, nil
}

func (engine *Engine) moduleConfig(stderr io.Writer) (wazerort.ModuleConfig, *workspaceGate, error) {
	var filesystem experimentalsys.FS
	if engine.workspaceLease != nil {
		filesystem = engine.workspaceLease.FS()
	}
	return newModuleConfig(stderr, filesystem)
}

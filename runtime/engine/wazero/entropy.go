package wazero

import (
	cryptorand "crypto/rand"
	"errors"
	"io"

	wazerort "github.com/tetratelabs/wazero"
	experimentalsys "github.com/tetratelabs/wazero/experimental/sys"
	experimentalsysfs "github.com/tetratelabs/wazero/experimental/sysfs"
)

func newModuleConfig(stderr io.Writer, workspaceFS experimentalsys.FS, temporaryFactory temporaryMountFactory) (wazerort.ModuleConfig, *moduleMounts, error) {
	config := wazerort.NewModuleConfig().
		WithName("").
		WithRandSource(cryptorand.Reader).
		WithSysWalltime().
		WithSysNanotime().
		WithSysNanosleep()
	if stderr != nil {
		config = config.WithStderr(stderr)
	}
	if (workspaceFS == nil) != (temporaryFactory == nil) {
		return nil, nil, errors.New("workspace and temporary mounts must be configured together")
	}
	if workspaceFS == nil {
		return config, nil, nil
	}
	mounts, err := newModuleMounts(workspaceFS, temporaryFactory)
	if err != nil {
		return nil, nil, err
	}
	base := wazerort.NewFSConfig()
	extended, ok := base.(experimentalsysfs.FSConfig)
	if !ok {
		return nil, nil, errors.New("wazero does not support rooted workspace mounts")
	}
	withWorkspace := extended.WithSysFSMount(mounts.workspace, "workspace")
	extended, ok = withWorkspace.(experimentalsysfs.FSConfig)
	if !ok {
		return nil, nil, errors.New("wazero does not support multiple rooted mounts")
	}
	config = config.WithFSConfig(extended.WithSysFSMount(mounts.temporary, "tmp"))
	return config, mounts, nil
}

func (engine *Engine) moduleConfig(stderr io.Writer) (wazerort.ModuleConfig, *moduleMounts, error) {
	if engine.workspaceLease == nil {
		return newModuleConfig(stderr, nil, nil)
	}
	return newModuleConfig(stderr, engine.workspaceLease.FS(), func() (temporaryMount, error) {
		return engine.workspaceLease.NewTemporary()
	})
}

package wazero

import (
	cryptorand "crypto/rand"
	"io"

	wazerort "github.com/tetratelabs/wazero"
)

func newModuleConfig(stderr io.Writer) wazerort.ModuleConfig {
	config := wazerort.NewModuleConfig().
		WithName("").
		WithRandSource(cryptorand.Reader).
		WithSysWalltime().
		WithSysNanotime().
		WithSysNanosleep()
	if stderr != nil {
		config = config.WithStderr(stderr)
	}
	return config
}

package pysolate

import (
	"crypto/rand"
	"crypto/sha256"
	mathrand "math/rand/v2"

	"github.com/tetratelabs/wazero"
)

// executionEnvironment owns deterministic WASI inputs for one attempt. It is
// deliberately independent of Journal, which only intercepts Host tool calls.
type executionEnvironment struct {
	seed       string
	random     *mathrand.ChaCha8
	wall, mono int64
}

// The image captures Python memory; these are the WASI inputs consumed during init.
// One immutable state belongs to one seed, not a per-Run image cache.
type preparedEnvironmentState struct {
	seed       string
	random     []byte
	wall, mono int64
}

func newExecutionEnvironment(seed string) *executionEnvironment {
	return &executionEnvironment{
		seed:   seed,
		random: mathrand.NewChaCha8(sha256.Sum256([]byte(seed))),
		wall:   1_700_000_000_000_000_000,
	}
}

func (env *executionEnvironment) capture() (*preparedEnvironmentState, error) {
	data, err := env.random.MarshalBinary()
	if err != nil {
		return nil, err
	}
	return &preparedEnvironmentState{seed: env.seed, random: data, wall: env.wall, mono: env.mono}, nil
}

func (env *executionEnvironment) restore(saved *preparedEnvironmentState) error {
	if err := env.random.UnmarshalBinary(saved.random); err != nil {
		return err
	}
	env.wall, env.mono = saved.wall, saved.mono
	return nil
}

// moduleConfig configures ordinary WASI resources and environmental inputs;
// callers may pass a deterministic environment without supplying any Journal.
func moduleConfig(stdout, stderr *boundedText, env *executionEnvironment, fsConfig wazero.FSConfig) wazero.ModuleConfig {
	config := wazero.NewModuleConfig().WithName("").WithStartFunctions().WithStdout(stdout).WithStderr(stderr)
	if fsConfig != nil {
		config = config.WithFSConfig(fsConfig)
	}
	if env == nil {
		return config.WithRandSource(rand.Reader).WithSysWalltime().WithSysNanotime().WithSysNanosleep()
	}
	return config.WithRandSource(env.random).
		WithWalltime(func() (int64, int32) {
			now := env.wall
			env.wall += 1_000_000
			return now / 1_000_000_000, int32(now % 1_000_000_000)
		}, 1_000_000).
		WithNanotime(func() int64 { now := env.mono; env.mono += 1_000_000; return now }, 1_000_000).
		WithNanosleep(func(ns int64) {
			if ns > 0 {
				env.wall += ns
				env.mono += ns
			}
		})
}

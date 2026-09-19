package pysolate

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	mathrand "math/rand/v2"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// Journal wraps a logical call. Its response must be durable before it is returned.
// next invokes the Host tool using the supplied context; tool errors are encoded outcomes.
type Journal interface {
	Call(context.Context, string, json.RawMessage, func(context.Context) []byte) ([]byte, error)
}

type recording struct {
	seed    string
	journal Journal
}

const journalExitCode = 125

// RunRecorded starts a fresh deterministic attempt. It deliberately excludes PLM
// and prepared images: both change when random/clock input or physical work occurs.
func (r *Runner) RunRecorded(ctx context.Context, source string, inputs any, seed string, journal Journal) (Output, error) {
	if journal == nil || seed == "" || r.image != nil || r.cow != nil {
		return Output{}, errors.New("recorded runs require a journal, seed and fresh runner")
	}
	return r.run(ctx, source, inputs, false, nil, &recording{seed: seed, journal: journal})
}

func (r *Runner) moduleConfig(ctx context.Context, stdout, stderr *boundedText) wazero.ModuleConfig {
	config := wazero.NewModuleConfig().WithName("").WithStartFunctions().WithStdout(stdout).WithStderr(stderr)
	state, _ := ctx.Value(runKey{}).(*runState)
	if state == nil || state.recording == nil {
		return config.WithRandSource(rand.Reader).WithSysWalltime().WithSysNanotime().WithSysNanosleep()
	}
	// A per-attempt stream and logical clocks are installed before CPython init.
	random := mathrand.NewChaCha8(sha256.Sum256([]byte(state.recording.seed)))
	wall, mono := int64(1_700_000_000_000_000_000), int64(0)
	return config.WithRandSource(random).
		WithWalltime(func() (int64, int32) {
			now := wall
			wall += 1_000_000
			return now / 1_000_000_000, int32(now % 1_000_000_000)
		}, 1_000_000).
		WithNanotime(func() int64 { now := mono; mono += 1_000_000; return now }, 1_000_000).
		WithNanosleep(func(ns int64) {
			if ns > 0 {
				wall += ns
				mono += ns
			}
		})
}

func stopForJournal(ctx context.Context, m api.Module) bool {
	state := ctx.Value(runKey{}).(*runState)
	if state.controlErr == nil {
		return false
	}
	_ = m.CloseWithExitCode(ctx, journalExitCode)
	return true
}

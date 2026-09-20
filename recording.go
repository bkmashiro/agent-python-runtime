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
	seed       string
	journal    Journal
	random     *mathrand.ChaCha8
	wall, mono int64
}

const journalExitCode = 125

// RunRecorded starts a deterministic attempt. A prepared runner must have been
// captured with this exact seed; PLM remains excluded.
func (r *Runner) RunRecorded(ctx context.Context, source string, inputs any, seed string, journal Journal) (Output, error) {
	if journal == nil || seed == "" {
		return Output{}, errors.New("recorded runs require a journal and seed")
	}
	if (r.image != nil || r.cow != nil) && (r.preparedState == nil || r.preparedState.seed != seed) {
		return Output{}, errors.New("prepared recording seed does not match")
	}
	return r.run(ctx, source, inputs, false, nil, newRecording(seed, journal), nil)
}

func (r *Runner) moduleConfig(ctx context.Context, stdout, stderr *boundedText) wazero.ModuleConfig {
	config := wazero.NewModuleConfig().WithName("").WithStartFunctions().WithStdout(stdout).WithStderr(stderr)
	state, _ := ctx.Value(runKey{}).(*runState)
	if state != nil && state.fsConfig != nil {
		config = config.WithFSConfig(state.fsConfig)
	}
	if state == nil || state.recording == nil {
		return config.WithRandSource(rand.Reader).WithSysWalltime().WithSysNanotime().WithSysNanosleep()
	}
	// A per-attempt stream and logical clocks are installed before CPython init.
	rec := state.recording
	return config.WithRandSource(rec.random).
		WithWalltime(func() (int64, int32) {
			now := rec.wall
			rec.wall += 1_000_000
			return now / 1_000_000_000, int32(now % 1_000_000_000)
		}, 1_000_000).
		WithNanotime(func() int64 { now := rec.mono; rec.mono += 1_000_000; return now }, 1_000_000).
		WithNanosleep(func(ns int64) {
			if ns > 0 {
				rec.wall += ns
				rec.mono += ns
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

// The image captures Python memory; these are the WASI inputs consumed during init.
// One immutable state belongs to one seed, not a per-Run image cache.
type preparedRecording struct {
	seed       string
	random     []byte
	wall, mono int64
}

func newRecording(seed string, journal Journal) *recording {
	return &recording{seed: seed, journal: journal, random: mathrand.NewChaCha8(sha256.Sum256([]byte(seed))), wall: 1_700_000_000_000_000_000}
}
func (rec *recording) capture() (*preparedRecording, error) {
	data, err := rec.random.MarshalBinary()
	if err != nil {
		return nil, err
	}
	return &preparedRecording{seed: rec.seed, random: data, wall: rec.wall, mono: rec.mono}, nil
}
func (rec *recording) restore(saved *preparedRecording) error {
	if err := rec.random.UnmarshalBinary(saved.random); err != nil {
		return err
	}
	rec.wall, rec.mono = saved.wall, saved.mono
	return nil
}

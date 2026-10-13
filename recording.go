package pysolate

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/tetratelabs/wazero/api"
)

// Journal wraps a logical call. Its response must be durable before it is returned.
// next invokes the Host tool using the supplied context; tool errors are encoded outcomes.
type Journal interface {
	Call(context.Context, string, json.RawMessage, func(context.Context) []byte) ([]byte, error)
}

const journalExitCode = 125

// RunRecorded starts a deterministic attempt. A prepared runner must have been
// captured with this exact seed; early-read execution remains excluded.
func (r *Runner) RunRecorded(ctx context.Context, source string, inputs any, seed string, journal Journal) (Output, error) {
	if journal == nil || seed == "" {
		return Output{}, errors.New("recorded runs require a journal and seed")
	}
	if (r.image != nil || r.cow != nil) && (r.preparedState == nil || r.preparedState.seed != seed) {
		return Output{}, errors.New("prepared recording seed does not match")
	}
	return r.run(ctx, source, inputs, runOptions{environment: newExecutionEnvironment(seed), journal: journal})
}

func stopForJournal(ctx context.Context, m api.Module) bool {
	state := ctx.Value(runKey{}).(*runState)
	if state.controlErr == nil {
		return false
	}
	_ = m.CloseWithExitCode(ctx, journalExitCode)
	return true
}

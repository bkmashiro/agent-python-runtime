package pysolate

import (
	"bytes"
	"context"
	"errors"
	"sync"

	"github.com/tetratelabs/wazero"
)

// One owner per Run. Only the Guest thread edits the map; workers publish via done.
// Early tools promise a stable, read-only snapshot for this run and must honor ctx.
type runState struct {
	recording  *recording
	controlErr error
	calls      uint32
	runner     *Runner
	ctx        context.Context
	cancel     context.CancelFunc
	enabled    bool
	next       uint32
	futures    map[uint32]*future
	workers    sync.WaitGroup
	fsConfig   wazero.FSConfig
}

type future struct {
	request  callRequest
	response []byte
	done     chan struct{}
	cancel   context.CancelFunc
}

type runKey struct{}

func newRun(ctx context.Context, r *Runner, enabled bool) *runState {
	ctx, cancel := context.WithCancel(ctx)
	state := &runState{runner: r, cancel: cancel, enabled: enabled, futures: make(map[uint32]*future)}
	state.ctx = context.WithValue(ctx, runKey{}, state)
	return state
}

func (s *runState) close() {
	s.cancel()
	s.workers.Wait() // Cooperative tools finish before Run returns, including discarded reads.
}

func (s *runState) prepare(request callRequest) uint32 {
	// 64 pending reads is a demo resource policy, not a Wasm limit.
	spec, ok := s.runner.manifest[request.Tool]
	if !s.enabled || !ok || !spec.AllowEarlyRead || len(s.futures) >= 64 || s.calls >= maxToolCalls {
		return 0 // Explicitly not prepared; original call will execute normally.
	}
	s.calls++
	ctx, cancel := context.WithCancel(s.ctx)
	f := &future{request: request, done: make(chan struct{}), cancel: cancel}
	s.next++
	id := s.next
	s.futures[id] = f
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		defer cancel()
		defer close(f.done)
		value, err := spec.Call(ctx, request.Args)
		f.response = encodeResponse(value, err)
	}()
	return id
}

func (s *runState) resolve(id uint32, request callRequest) []byte {
	if id == 0 {
		return s.runner.invoke(s.ctx, request)
	}
	f := s.futures[id]
	if f == nil {
		return encodeResponse(nil, errors.New("unknown or consumed future"))
	}
	delete(s.futures, id) // Single-use handle, not a static source-site slot.
	defer f.cancel()
	if f.request.Tool != request.Tool || !bytes.Equal(f.request.Args, request.Args) {
		f.cancel()
		// Only registered snapshot reads were started early. Never reuse mismatched data.
		return s.runner.invoke(s.ctx, request)
	}
	select {
	case <-f.done:
		return f.response // Includes tool errors: do not retry a failed operation.
	case <-s.ctx.Done():
		return encodeResponse(nil, s.ctx.Err())
	}
}

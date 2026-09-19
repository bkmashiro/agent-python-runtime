package durable

import (
	"context"
	"errors"
	"sync"

	pysolate "github.com/bkmashiro/agent-python-runtime"
)

var (
	ErrQueueFull = errors.New("durable executor queue is full")
	ErrClosed    = errors.New("durable executor is closed")
)

type Limits struct {
	MaxActive int
	MaxQueued int
}

type Executor struct {
	resume    func(context.Context, string) (pysolate.Output, error)
	cancel    func(context.Context, string) error
	maxActive int
	maxQueued int

	mu       sync.Mutex
	closed   bool
	drained  bool
	drain    chan struct{}
	queue    []*Attempt
	active   int
	attempts map[string]*Attempt
}

type Attempt struct {
	runID  string
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	stop   func() bool
	out    pysolate.Output
	err    error
	state  attemptState
}

type attemptState uint8

const (
	attemptQueued attemptState = iota + 1
	attemptActive
	attemptDone
)

func NewExecutor(runner *Runner, limits Limits) (*Executor, error) {
	if runner == nil || limits.MaxActive <= 0 || limits.MaxQueued < 0 {
		return nil, ErrInvalidRunner
	}
	return &Executor{
		resume:    runner.Resume,
		cancel:    runner.Cancel,
		maxActive: limits.MaxActive,
		maxQueued: limits.MaxQueued,
		drain:     make(chan struct{}),
		attempts:  make(map[string]*Attempt),
	}, nil
}

func (e *Executor) Submit(ctx context.Context, runID string) (*Attempt, error) {
	if e == nil || e.resume == nil || runID == "" {
		return nil, ErrInvalidRunner
	}
	if ctx == nil {
		return nil, errors.New("nil submit context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	aCtx, cancel := context.WithCancel(ctx)
	a := &Attempt{runID: runID, ctx: aCtx, cancel: cancel, done: make(chan struct{})}

	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		cancel()
		return nil, ErrClosed
	}
	if _, exists := e.attempts[runID]; exists {
		e.mu.Unlock()
		cancel()
		return nil, ErrBusy
	}
	e.attempts[runID] = a
	a.stop = context.AfterFunc(ctx, func() { e.abort(a, ctx.Err()) })
	if e.active < e.maxActive {
		a.state = attemptActive
		e.active++
		e.mu.Unlock()
		go e.run(a)
		return a, nil
	}
	if len(e.queue) >= e.maxQueued {
		delete(e.attempts, runID)
		a.stop()
		e.mu.Unlock()
		cancel()
		return nil, ErrQueueFull
	}
	a.state = attemptQueued
	e.queue = append(e.queue, a)
	e.mu.Unlock()
	return a, nil
}

func (a *Attempt) Wait(ctx context.Context) (pysolate.Output, error) {
	if a == nil {
		return pysolate.Output{}, ErrInvalidRunner
	}
	if ctx == nil {
		return pysolate.Output{}, errors.New("nil wait context")
	}
	select {
	case <-a.done:
		out := a.out
		out.Value = append([]byte(nil), out.Value...)
		return out, a.err
	case <-ctx.Done():
		return pysolate.Output{}, ctx.Err()
	}
}

func (e *Executor) Cancel(ctx context.Context, runID string) error {
	if e == nil || e.resume == nil || runID == "" {
		return ErrInvalidRunner
	}
	if ctx == nil {
		return errors.New("nil cancel context")
	}
	if err := e.cancel(ctx, runID); err != nil {
		return err
	}
	e.mu.Lock()
	a := e.attempts[runID]
	if a == nil {
		e.mu.Unlock()
		return nil
	}
	if a.state == attemptQueued {
		e.dropQueuedLocked(a, ErrCancelled)
		e.maybeDrainLocked()
		e.mu.Unlock()
		return nil
	}
	if a.state == attemptActive {
		a.cancel()
	}
	e.mu.Unlock()
	return nil
}

func (e *Executor) Close(ctx context.Context) error {
	if e == nil {
		return nil
	}
	if ctx == nil {
		return errors.New("nil close context")
	}
	e.mu.Lock()
	e.closed = true
	e.maybeDrainLocked()
	drain := e.drain
	e.mu.Unlock()
	select {
	case <-drain:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *Executor) abort(a *Attempt, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	switch a.state {
	case attemptQueued:
		e.dropQueuedLocked(a, err)
		e.maybeDrainLocked()
	case attemptActive:
		a.cancel()
	case attemptDone:
	}
}

func (e *Executor) run(a *Attempt) {
	out, err := e.resume(a.ctx, a.runID)
	e.finish(a, out, err)
}

func (e *Executor) finish(a *Attempt, out pysolate.Output, err error) {
	var starts []*Attempt
	e.mu.Lock()
	if a.state != attemptActive {
		e.mu.Unlock()
		return
	}
	a.state = attemptDone
	e.active--
	delete(e.attempts, a.runID)
	a.cancel()
	a.complete(out, err)

	for e.active < e.maxActive && len(e.queue) > 0 {
		next := e.queue[0]
		e.queue[0] = nil
		e.queue = e.queue[1:]
		if next.state != attemptQueued {
			continue
		}
		if err := next.ctx.Err(); err != nil {
			e.dropQueuedLocked(next, err)
			continue
		}
		next.state = attemptActive
		e.active++
		starts = append(starts, next)
	}
	e.maybeDrainLocked()
	e.mu.Unlock()
	for _, next := range starts {
		go e.run(next)
	}
}

func (a *Attempt) complete(out pysolate.Output, err error) {
	// All terminal transitions hold the Executor mutex.
	a.stop()
	a.out, a.err = out, err
	close(a.done)
}

func (e *Executor) removeQueuedLocked(target *Attempt) {
	for i, a := range e.queue {
		if a == target {
			copy(e.queue[i:], e.queue[i+1:])
			e.queue[len(e.queue)-1] = nil
			e.queue = e.queue[:len(e.queue)-1]
			return
		}
	}
}

func (e *Executor) dropQueuedLocked(a *Attempt, err error) {
	e.removeQueuedLocked(a)
	a.state = attemptDone
	delete(e.attempts, a.runID)
	a.cancel()
	a.complete(pysolate.Output{}, err)
}

func (e *Executor) maybeDrainLocked() {
	if e.closed && !e.drained && len(e.attempts) == 0 {
		e.drained = true
		close(e.drain)
	}
}

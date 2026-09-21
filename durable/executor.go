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
	controller AttemptController
	maxActive  int
	maxQueued  int

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
	result AdvanceResult
	err    error
	state  attemptState
}

type attemptState uint8

const (
	attemptQueued attemptState = iota + 1
	attemptActive
	attemptDone
)

func NewExecutor(controller AttemptController, limits Limits) (*Executor, error) {
	if controller == nil || limits.MaxActive <= 0 || limits.MaxQueued < 0 {
		return nil, ErrInvalidRunner
	}
	return &Executor{
		controller: controller,
		maxActive:  limits.MaxActive,
		maxQueued:  limits.MaxQueued,
		drain:      make(chan struct{}),
		attempts:   make(map[string]*Attempt),
	}, nil
}

// Admit schedules one fresh attempt. A parked result releases active capacity;
// callers may resolve its protocol and admit the same Run again later.
func (e *Executor) Admit(ctx context.Context, runID string) (*Attempt, error) {
	if e == nil || e.controller == nil || runID == "" {
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

// Submit is retained as an alias for callers using the original FIFO API.
func (e *Executor) Submit(ctx context.Context, runID string) (*Attempt, error) {
	return e.Admit(ctx, runID)
}

// Result returns the control-plane result of one attempt. Returned output
// bytes and Park metadata are copied so repeated calls are independent.
func (a *Attempt) Result(ctx context.Context) (AdvanceResult, error) {
	if a == nil {
		return AdvanceResult{}, ErrInvalidRunner
	}
	if ctx == nil {
		return AdvanceResult{}, errors.New("nil result context")
	}
	select {
	case <-a.done:
		result := a.result
		result.Output.Value = append([]byte(nil), result.Output.Value...)
		if result.Park != nil {
			park := *result.Park
			result.Park = &park
		}
		return result, a.err
	case <-ctx.Done():
		return AdvanceResult{}, ctx.Err()
	}
}

// Wait preserves the original API. New lifecycle code should use Result so a
// normal park transition is not mixed with execution failures.
func (a *Attempt) Wait(ctx context.Context) (pysolate.Output, error) {
	result, err := a.Result(ctx)
	if err != nil {
		return result.Output, err
	}
	if result.State == AttemptParked {
		return result.Output, parkError(result.Park)
	}
	return result.Output, nil
}

func (e *Executor) Cancel(ctx context.Context, runID string) error {
	if e == nil || e.controller == nil || runID == "" {
		return ErrInvalidRunner
	}
	if ctx == nil {
		return errors.New("nil cancel context")
	}
	if err := e.controller.Cancel(ctx, runID); err != nil {
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
	result, err := e.controller.Advance(a.ctx, a.runID)
	e.finish(a, result, err)
}

func (e *Executor) finish(a *Attempt, result AdvanceResult, err error) {
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
	a.complete(result, err)

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

func (a *Attempt) complete(result AdvanceResult, err error) {
	// All terminal transitions hold the Executor mutex.
	a.stop()
	a.result, a.err = result, err
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
	a.complete(AdvanceResult{}, err)
}

func (e *Executor) maybeDrainLocked() {
	if e.closed && !e.drained && len(e.attempts) == 0 {
		e.drained = true
		close(e.drain)
	}
}

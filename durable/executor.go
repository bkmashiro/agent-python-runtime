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
	// MaxActive is the compatibility alias for MaxRunning. New callers should
	// set MaxRunning and MaxResident explicitly.
	MaxActive int
	// MaxRunning bounds Guests currently allowed to execute Python.
	MaxRunning int
	// MaxResident bounds all live Guests, including those waiting in Host I/O.
	MaxResident int
	// MaxInflightTools bounds opted-in ExternalIO Host callbacks.
	MaxInflightTools int
	// MaxQueued bounds attempts that have not created a Guest yet.
	MaxQueued int
}

type Executor struct {
	controller  AttemptController
	maxRunning  int
	maxResident int
	maxQueued   int
	external    chan struct{}

	mu         sync.Mutex
	closed     bool
	drained    bool
	drain      chan struct{}
	queue      []*Attempt
	ready      []*Attempt
	readyBurst int
	running    int
	resident   int
	attempts   map[string]*Attempt
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
	permit chan struct{}
}

type attemptState uint8

// maxReadyBurst keeps live continuations responsive without permanently
// starving queued admissions when resident capacity is available.
const maxReadyBurst = 8

const (
	attemptQueued attemptState = iota + 1
	attemptRunning
	attemptWaitingLive
	attemptReady
	attemptDone
)

func NewExecutor(controller AttemptController, limits Limits) (*Executor, error) {
	maxRunning := limits.MaxRunning
	if maxRunning == 0 {
		maxRunning = limits.MaxActive
	} else if limits.MaxActive != 0 && limits.MaxActive != maxRunning {
		return nil, ErrInvalidRunner
	}
	maxResident := limits.MaxResident
	if maxResident == 0 {
		maxResident = maxRunning
	}
	maxInflightTools := limits.MaxInflightTools
	if maxInflightTools == 0 {
		maxInflightTools = maxResident
	}
	if controller == nil || maxRunning <= 0 || maxResident < maxRunning || maxInflightTools <= 0 || limits.MaxQueued < 0 {
		return nil, ErrInvalidRunner
	}
	return &Executor{
		controller:  controller,
		maxRunning:  maxRunning,
		maxResident: maxResident,
		maxQueued:   limits.MaxQueued,
		external:    make(chan struct{}, maxInflightTools),
		drain:       make(chan struct{}),
		attempts:    make(map[string]*Attempt),
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
	if e.running < e.maxRunning && e.resident < e.maxResident {
		a.state = attemptRunning
		e.running++
		e.resident++
		e.readyBurst = 0
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
	if a.state == attemptRunning || a.state == attemptWaitingLive || a.state == attemptReady {
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
	case attemptRunning, attemptWaitingLive, attemptReady:
		a.cancel()
	case attemptDone:
	}
}

func (e *Executor) run(a *Attempt) {
	ctx := context.WithValue(a.ctx, executionGateContextKey{}, &executionGate{executor: e, attempt: a})
	result, err := e.controller.Advance(ctx, a.runID)
	e.finish(a, result, err)
}

func (e *Executor) finish(a *Attempt, result AdvanceResult, err error) {
	e.mu.Lock()
	if a.state == attemptDone || a.state == attemptQueued {
		e.mu.Unlock()
		return
	}
	switch a.state {
	case attemptRunning:
		e.running--
		e.resident--
	case attemptWaitingLive:
		e.resident--
	case attemptReady:
		e.removeReadyLocked(a)
		e.resident--
	}
	a.state = attemptDone
	delete(e.attempts, a.runID)
	a.cancel()
	a.complete(result, err)
	e.scheduleLocked()
	e.maybeDrainLocked()
	e.mu.Unlock()
}

func (e *Executor) scheduleLocked() {
	for e.running < e.maxRunning {
		canAdmit := e.resident < e.maxResident && len(e.queue) > 0
		if len(e.ready) > 0 && (!canAdmit || e.readyBurst < maxReadyBurst) {
			next := e.ready[0]
			e.ready[0] = nil
			e.ready = e.ready[1:]
			if next.state != attemptReady {
				continue
			}
			next.state = attemptRunning
			e.running++
			e.readyBurst++
			permit := next.permit
			next.permit = nil
			close(permit)
			continue
		}
		if e.resident >= e.maxResident || len(e.queue) == 0 {
			return
		}
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
		next.state = attemptRunning
		e.running++
		e.resident++
		e.readyBurst = 0
		go e.run(next)
	}
}

type executionGateContextKey struct{}

type executionGate struct {
	executor *Executor
	attempt  *Attempt
	external bool
}

func yieldExecution(ctx context.Context) error {
	gate, _ := ctx.Value(executionGateContextKey{}).(*executionGate)
	if gate == nil {
		return nil
	}
	return gate.yield(ctx)
}

func reacquireExecution(ctx context.Context) error {
	gate, _ := ctx.Value(executionGateContextKey{}).(*executionGate)
	if gate == nil {
		return nil
	}
	return gate.reacquire(ctx)
}

func (gate *executionGate) yield(ctx context.Context) error {
	e := gate.executor
	e.mu.Lock()
	if gate.attempt.state != attemptRunning {
		e.mu.Unlock()
		return ErrInvalidRunner
	}
	gate.attempt.state = attemptWaitingLive
	e.running--
	e.scheduleLocked()
	e.mu.Unlock()
	select {
	case e.external <- struct{}{}:
		gate.external = true
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (gate *executionGate) reacquire(ctx context.Context) error {
	e := gate.executor
	if gate.external {
		<-e.external
		gate.external = false
	}
	e.mu.Lock()
	if gate.attempt.state != attemptWaitingLive {
		e.mu.Unlock()
		return ErrInvalidRunner
	}
	if err := ctx.Err(); err != nil {
		e.mu.Unlock()
		return err
	}
	if e.running < e.maxRunning {
		gate.attempt.state = attemptRunning
		e.running++
		e.mu.Unlock()
		return nil
	}
	permit := make(chan struct{})
	gate.attempt.state = attemptReady
	gate.attempt.permit = permit
	e.ready = append(e.ready, gate.attempt)
	e.mu.Unlock()

	select {
	case <-permit:
		return ctx.Err()
	case <-ctx.Done():
		e.mu.Lock()
		if gate.attempt.state == attemptReady {
			e.removeReadyLocked(gate.attempt)
			gate.attempt.state = attemptWaitingLive
			gate.attempt.permit = nil
		}
		e.mu.Unlock()
		return ctx.Err()
	}
}

// ExecutorStats is a point-in-time view of admission and live-wait capacity.
type ExecutorStats struct {
	Running       int
	Resident      int
	InflightTools int
	WaitingLive   int
	Ready         int
	Queued        int
}

// Stats returns a point-in-time snapshot. Counts may change immediately after
// the call returns.
func (e *Executor) Stats() ExecutorStats {
	if e == nil {
		return ExecutorStats{}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	stats := ExecutorStats{Running: e.running, Resident: e.resident, InflightTools: len(e.external), Queued: len(e.queue)}
	for _, attempt := range e.attempts {
		switch attempt.state {
		case attemptWaitingLive:
			stats.WaitingLive++
		case attemptReady:
			stats.Ready++
		}
	}
	return stats
}

func (a *Attempt) complete(result AdvanceResult, err error) {
	// All terminal transitions hold the Executor mutex.
	a.stop()
	a.result, a.err = result, err
	close(a.done)
}

func (e *Executor) removeReadyLocked(target *Attempt) {
	for i, attempt := range e.ready {
		if attempt == target {
			copy(e.ready[i:], e.ready[i+1:])
			e.ready[len(e.ready)-1] = nil
			e.ready = e.ready[:len(e.ready)-1]
			return
		}
	}
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

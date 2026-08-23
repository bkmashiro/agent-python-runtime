package wazero

import (
	"context"
	"errors"
	"sync"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
)

var (
	ErrPreparedFamilyConsumerLimit = errors.New("prepared family consumer limit reached")
	ErrPreparedFamilyActiveLimit   = errors.New("prepared family active limit reached")
	ErrPreparedFamilyRunsActive    = errors.New("prepared family has active runs")
	ErrPreparedFamilyClosed        = errors.New("prepared family is closed")
	ErrPreparedRunnerConsumed      = errors.New("prepared family runner is consumed")
	ErrPreparedTrustedPrepare      = errors.New("prepared family runner rejects caller trusted preparation")
	ErrPreparedInvocationMismatch  = errors.New("prepared family invocation identity mismatch")
)

type PreparedMemberDisposition string

const (
	PreparedMemberOK          PreparedMemberDisposition = "ok"
	PreparedMemberGuestError  PreparedMemberDisposition = "guest_error"
	PreparedMemberCancelled   PreparedMemberDisposition = "cancelled"
	PreparedMemberClosedUnrun PreparedMemberDisposition = "closed_unrun"
)

type preparedMemberPhase uint8

const (
	preparedMemberNew preparedMemberPhase = iota
	preparedMemberRunning
	preparedMemberTerminal
	preparedMemberClosed
)

type preparedFamilyMember struct {
	phase       preparedMemberPhase
	disposition PreparedMemberDisposition
}

type PreparedFamilyState struct {
	Created  uint32
	Active   uint32
	Terminal uint32
	Closed   bool
}

type preparedFamilyLifecycle struct {
	mu           sync.Mutex
	maxConsumers uint32
	maxActive    uint32
	total        uint32
	active       uint32
	nextID       uint64
	closed       bool
	members      map[uint64]preparedFamilyMember
}

func newPreparedFamilyLifecycle(maxConsumers, maxActive uint32) (*preparedFamilyLifecycle, error) {
	if maxConsumers == 0 || maxConsumers > 1024 || maxActive == 0 || maxActive > maxConsumers {
		return nil, ErrPreparedFamilyConsumerLimit
	}
	return &preparedFamilyLifecycle{
		maxConsumers: maxConsumers,
		maxActive:    maxActive,
		members:      make(map[uint64]preparedFamilyMember),
	}, nil
}

func (lifecycle *preparedFamilyLifecycle) reserve() (uint64, error) {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	if lifecycle.closed {
		return 0, ErrPreparedFamilyClosed
	}
	if lifecycle.total >= lifecycle.maxConsumers {
		return 0, ErrPreparedFamilyConsumerLimit
	}
	lifecycle.nextID++
	lifecycle.total++
	lifecycle.members[lifecycle.nextID] = preparedFamilyMember{phase: preparedMemberNew}
	return lifecycle.nextID, nil
}

func (lifecycle *preparedFamilyLifecycle) begin(id uint64) error {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	if lifecycle.closed {
		return ErrPreparedFamilyClosed
	}
	member, exists := lifecycle.members[id]
	if !exists || member.phase != preparedMemberNew {
		return ErrPreparedRunnerConsumed
	}
	if lifecycle.active >= lifecycle.maxActive {
		return ErrPreparedFamilyActiveLimit
	}
	member.phase = preparedMemberRunning
	lifecycle.members[id] = member
	lifecycle.active++
	return nil
}

func (lifecycle *preparedFamilyLifecycle) finish(id uint64, disposition PreparedMemberDisposition) error {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	member, exists := lifecycle.members[id]
	if !exists || member.phase != preparedMemberRunning || disposition == "" {
		return ErrPreparedRunnerConsumed
	}
	member.phase = preparedMemberTerminal
	member.disposition = disposition
	lifecycle.members[id] = member
	lifecycle.active--
	return nil
}

func (lifecycle *preparedFamilyLifecycle) retire(id uint64) error {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	member, exists := lifecycle.members[id]
	if !exists {
		return ErrPreparedRunnerConsumed
	}
	switch member.phase {
	case preparedMemberNew:
		member.disposition = PreparedMemberClosedUnrun
		member.phase = preparedMemberClosed
	case preparedMemberTerminal:
		member.phase = preparedMemberClosed
	case preparedMemberClosed:
		return nil
	default:
		return ErrPreparedFamilyRunsActive
	}
	lifecycle.members[id] = member
	return nil
}

func (lifecycle *preparedFamilyLifecycle) close() error {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	if lifecycle.closed {
		return nil
	}
	if lifecycle.active != 0 {
		return ErrPreparedFamilyRunsActive
	}
	lifecycle.closed = true
	for id, member := range lifecycle.members {
		if member.phase == preparedMemberNew {
			member.phase = preparedMemberClosed
			member.disposition = PreparedMemberClosedUnrun
			lifecycle.members[id] = member
		}
	}
	return nil
}

func (lifecycle *preparedFamilyLifecycle) state() PreparedFamilyState {
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	state := PreparedFamilyState{Created: lifecycle.total, Active: lifecycle.active, Closed: lifecycle.closed}
	for _, member := range lifecycle.members {
		if member.phase == preparedMemberTerminal || member.phase == preparedMemberClosed {
			state.Terminal++
		}
	}
	return state
}

type preparedFamilyRunner struct {
	delegate   enginecontract.Runner
	invocation runtimeconfig.InvocationRef
	lifecycle  *preparedFamilyLifecycle
	memberID   uint64

	mu      sync.Mutex
	closed  bool
	running bool
	done    chan struct{}
}

func newPreparedFamilyRunner(delegate enginecontract.Runner, invocation runtimeconfig.InvocationRef, lifecycle *preparedFamilyLifecycle, memberID uint64) *preparedFamilyRunner {
	return &preparedFamilyRunner{
		delegate: delegate, invocation: invocation, lifecycle: lifecycle,
		memberID: memberID, done: make(chan struct{}),
	}
}

func (runner *preparedFamilyRunner) Run(ctx context.Context, request []byte, trustedPrepare string) ([]byte, error) {
	if trustedPrepare != "" {
		return nil, ErrPreparedTrustedPrepare
	}
	decoded, err := runtimeconfig.DecodeRunRequest(request)
	if err != nil || decoded.RunID != runner.invocation.ExecutionID {
		return nil, ErrPreparedInvocationMismatch
	}
	runner.mu.Lock()
	if runner.closed || runner.running {
		runner.mu.Unlock()
		return nil, ErrPreparedRunnerConsumed
	}
	if err := runner.lifecycle.begin(runner.memberID); err != nil {
		runner.mu.Unlock()
		return nil, err
	}
	runner.running = true
	runner.mu.Unlock()

	runContext, err := enginecontract.WithInvocationRef(ctx, runner.invocation)
	if err != nil {
		_ = runner.lifecycle.finish(runner.memberID, PreparedMemberGuestError)
		runner.finishRun()
		return nil, err
	}
	response, runErr := runner.delegate.Run(runContext, request, "")
	disposition := PreparedMemberOK
	if runErr != nil {
		disposition = PreparedMemberGuestError
		if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) || ctx.Err() != nil {
			disposition = PreparedMemberCancelled
		}
	}
	_ = runner.lifecycle.finish(runner.memberID, disposition)
	runner.finishRun()
	return response, runErr
}

func (runner *preparedFamilyRunner) finishRun() {
	runner.mu.Lock()
	runner.running = false
	select {
	case <-runner.done:
	default:
		close(runner.done)
	}
	runner.mu.Unlock()
}

func (runner *preparedFamilyRunner) Close(ctx context.Context) error {
	runner.mu.Lock()
	if runner.closed {
		runner.mu.Unlock()
		return nil
	}
	running := runner.running
	done := runner.done
	runner.mu.Unlock()
	if running {
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	runner.mu.Lock()
	if runner.closed {
		runner.mu.Unlock()
		return nil
	}
	runner.closed = true
	runner.mu.Unlock()
	retireErr := runner.lifecycle.retire(runner.memberID)
	closeErr := runner.delegate.Close(ctx)
	return errors.Join(retireErr, closeErr)
}

func (runner *preparedFamilyRunner) Properties() enginecontract.Properties {
	return runner.delegate.Properties()
}

var _ enginecontract.Runner = (*preparedFamilyRunner)(nil)

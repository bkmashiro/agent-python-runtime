package scheduler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/engine"
)

type WorkerConfig struct {
	MaxActive       uint32
	MaxRequestBytes uint32
}

type ExecutionRequest struct {
	AttemptID      string
	Request        []byte
	TrustedPrepare string
}

type ExecutionResult struct {
	AttemptID  string
	Response   []byte
	Err        error
	StartedAt  time.Time
	FinishedAt time.Time
}

type Termination struct {
	AttemptID          string
	ExecutorTerminated bool
	Result             ExecutionResult
}

type WorkerSnapshot struct {
	Active uint32
	Closed bool
}

type executionRecord struct {
	mu sync.Mutex

	request        []byte
	trustedPrepare string
	cancel         context.CancelFunc
	done           chan struct{}
	result         ExecutionResult
	complete       bool
	handle         *ExecutionHandle
}

type ExecutionHandle struct {
	record *executionRecord
}

func (handle *ExecutionHandle) Done() <-chan struct{} {
	if handle == nil || handle.record == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}
	return handle.record.done
}

func (handle *ExecutionHandle) Result() (ExecutionResult, bool) {
	if handle == nil || handle.record == nil {
		return ExecutionResult{}, false
	}
	handle.record.mu.Lock()
	defer handle.record.mu.Unlock()
	if !handle.record.complete {
		return ExecutionResult{}, false
	}
	return copyExecutionResult(handle.record.result), true
}

type InProcessWorker struct {
	mu sync.Mutex

	runner engine.Runner
	config WorkerConfig
	active map[string]*executionRecord
	closed bool
}

func NewInProcessWorker(runner engine.Runner, config WorkerConfig) (*InProcessWorker, error) {
	if runner == nil || config.MaxActive == 0 || config.MaxActive > 1<<20 || config.MaxRequestBytes == 0 || config.MaxRequestBytes > 64<<20 {
		return nil, fmt.Errorf("%w: worker runner or bounds are invalid", ErrInvalidConfig)
	}
	if err := runner.Properties().Validate(); err != nil {
		return nil, fmt.Errorf("%w: runner properties: %v", ErrInvalidConfig, err)
	}
	return &InProcessWorker{runner: runner, config: config, active: make(map[string]*executionRecord)}, nil
}

func (worker *InProcessWorker) Start(ctx context.Context, request ExecutionRequest) (*ExecutionHandle, error) {
	if worker == nil || ctx == nil || !boundedIdentifier(request.AttemptID) || len(request.Request) == 0 || uint64(len(request.Request)) > uint64(worker.config.MaxRequestBytes) || uint64(len(request.TrustedPrepare)) > uint64(worker.config.MaxRequestBytes) {
		return nil, ErrInvalidTask
	}
	requestBytes := append([]byte(nil), request.Request...)
	worker.mu.Lock()
	if worker.closed {
		worker.mu.Unlock()
		return nil, ErrConflict
	}
	if prior, ok := worker.active[request.AttemptID]; ok {
		if !bytes.Equal(prior.request, requestBytes) || prior.trustedPrepare != request.TrustedPrepare {
			worker.mu.Unlock()
			return nil, ErrConflict
		}
		handle := prior.handle
		worker.mu.Unlock()
		return handle, nil
	}
	if uint32(len(worker.active)) >= worker.config.MaxActive {
		worker.mu.Unlock()
		return nil, ErrCapacity
	}
	executionContext, cancel := context.WithCancel(ctx)
	record := &executionRecord{
		request:        requestBytes,
		trustedPrepare: request.TrustedPrepare,
		cancel:         cancel,
		done:           make(chan struct{}),
	}
	record.handle = &ExecutionHandle{record: record}
	worker.active[request.AttemptID] = record
	worker.mu.Unlock()

	go worker.execute(executionContext, request.AttemptID, record)
	return record.handle, nil
}

func (worker *InProcessWorker) execute(ctx context.Context, attemptID string, record *executionRecord) {
	started := time.Now().UTC()
	response, err := worker.runner.Run(ctx, append([]byte(nil), record.request...), record.trustedPrepare)
	result := ExecutionResult{AttemptID: attemptID, Response: append([]byte(nil), response...), Err: err, StartedAt: started, FinishedAt: time.Now().UTC()}
	record.mu.Lock()
	record.result = result
	record.complete = true
	close(record.done)
	record.mu.Unlock()

	worker.mu.Lock()
	if worker.active[attemptID] == record {
		delete(worker.active, attemptID)
	}
	worker.mu.Unlock()
}

func (worker *InProcessWorker) Cancel(ctx context.Context, attemptID string) (Termination, error) {
	if worker == nil || ctx == nil || !boundedIdentifier(attemptID) {
		return Termination{}, ErrInvalidTask
	}
	worker.mu.Lock()
	record, ok := worker.active[attemptID]
	if !ok {
		worker.mu.Unlock()
		return Termination{}, ErrNotFound
	}
	record.cancel()
	worker.mu.Unlock()
	select {
	case <-record.done:
		result, ok := record.handle.Result()
		if !ok {
			return Termination{}, ErrConflict
		}
		return Termination{AttemptID: attemptID, ExecutorTerminated: true, Result: result}, nil
	case <-ctx.Done():
		return Termination{}, ctx.Err()
	}
}

func (worker *InProcessWorker) Snapshot() WorkerSnapshot {
	if worker == nil {
		return WorkerSnapshot{Closed: true}
	}
	worker.mu.Lock()
	defer worker.mu.Unlock()
	return WorkerSnapshot{Active: uint32(len(worker.active)), Closed: worker.closed}
}

func (worker *InProcessWorker) Close(ctx context.Context) error {
	if worker == nil || ctx == nil {
		return ErrInvalidConfig
	}
	worker.mu.Lock()
	if worker.closed {
		worker.mu.Unlock()
		return nil
	}
	worker.closed = true
	records := make([]*executionRecord, 0, len(worker.active))
	for _, record := range worker.active {
		record.cancel()
		records = append(records, record)
	}
	worker.mu.Unlock()
	for _, record := range records {
		select {
		case <-record.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err := worker.runner.Close(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

func copyExecutionResult(result ExecutionResult) ExecutionResult {
	result.Response = append([]byte(nil), result.Response...)
	return result
}

package scheduler

import (
	"context"
	"errors"
	"sync"
	"time"
)

type ProfiledExecution struct {
	Spec           ProfiledTaskSpec
	Request        []byte
	TrustedPrepare string
}

type ProfiledExecutionResult struct {
	TaskID     string
	AttemptID  string
	Response   []byte
	Err        error
	StartedAt  time.Time
	FinishedAt time.Time
}

type ProfiledBatchExecutorConfig struct {
	Scheduler       *Scheduler
	Profiles        *ProfileStore
	Worker          *InProcessWorker
	ControlLoop     *LiveMemoryControlLoop
	PollInterval    time.Duration
	MaxPayloadBytes uint32
}

type ProfiledBatchExecutor struct {
	mu     sync.Mutex
	config ProfiledBatchExecutorConfig
	used   bool
}

type batchActiveAttempt struct {
	execution ProfiledExecution
	handle    *ExecutionHandle
}

type batchCompletion struct {
	attemptID string
	handle    *ExecutionHandle
}

func NewProfiledBatchExecutor(config ProfiledBatchExecutorConfig) (*ProfiledBatchExecutor, error) {
	if config.Scheduler == nil || config.Profiles == nil || config.Worker == nil || config.Worker.Snapshot().Closed ||
		config.PollInterval <= 0 || config.PollInterval > time.Minute || config.MaxPayloadBytes == 0 ||
		config.MaxPayloadBytes > config.Worker.config.MaxRequestBytes ||
		config.ControlLoop != nil && (config.ControlLoop.config.Scheduler != config.Scheduler || config.ControlLoop.config.Profiles != config.Profiles) {
		return nil, ErrInvalidConfig
	}
	return &ProfiledBatchExecutor{config: config}, nil
}

func (executor *ProfiledBatchExecutor) Run(ctx context.Context, batch []ProfiledExecution) ([]ProfiledExecutionResult, error) {
	if executor == nil || ctx == nil || len(batch) == 0 || uint64(len(batch)) > uint64(executor.config.Scheduler.config.MaxTasks) {
		return nil, ErrInvalidTask
	}
	executor.mu.Lock()
	if executor.used {
		executor.mu.Unlock()
		return nil, ErrConflict
	}
	executor.used = true
	executor.mu.Unlock()

	executions := make(map[string]ProfiledExecution, len(batch))
	order := make(map[string]int, len(batch))
	for index, item := range batch {
		if !boundedIdentifier(item.Spec.TaskID) || len(item.Request) == 0 || uint64(len(item.Request)) > uint64(executor.config.MaxPayloadBytes) ||
			uint64(len(item.TrustedPrepare)) > uint64(executor.config.MaxPayloadBytes) {
			return nil, ErrInvalidTask
		}
		if _, duplicate := executions[item.Spec.TaskID]; duplicate {
			return nil, ErrConflict
		}
		item.Request = append([]byte(nil), item.Request...)
		executions[item.Spec.TaskID] = item
		order[item.Spec.TaskID] = index
	}
	for _, item := range batch {
		if _, _, err := executor.config.Scheduler.SubmitProfiled(executor.config.Profiles, item.Spec); err != nil {
			return nil, err
		}
	}

	runContext, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make([]ProfiledExecutionResult, len(batch))
	completed := make([]bool, len(batch))
	completedCount := 0
	active := make(map[string]batchActiveAttempt)
	completionCapacity := executor.config.Worker.config.MaxActive
	completions := make(chan batchCompletion, completionCapacity)
	var controlResults chan error
	controlStarted := false
	startControl := func() {
		if controlStarted || executor.config.ControlLoop == nil || len(active) == 0 {
			return
		}
		controlStarted = true
		controlResults = make(chan error, 1)
		go func() {
			_, controlErr := executor.config.ControlLoop.Run(runContext)
			controlResults <- controlErr
		}()
	}

	dispatch := func() error {
		for uint32(len(active)) < executor.config.Worker.config.MaxActive {
			attempt, err := executor.config.Scheduler.AdmitProfiled(executor.config.Profiles)
			if errors.Is(err, ErrNoAdmissibleTask) || errors.Is(err, ErrCapacity) {
				return nil
			}
			if err != nil {
				return err
			}
			item, ok := executions[attempt.TaskID]
			if !ok {
				_ = executor.config.Scheduler.ReleaseAdmission(attempt.AttemptID)
				executor.config.Profiles.releaseAttempt(attempt.AttemptID)
				return ErrConflict
			}
			handle, err := executor.config.Worker.Start(runContext, ExecutionRequest{
				AttemptID: attempt.AttemptID, Request: item.Request, TrustedPrepare: item.TrustedPrepare,
			})
			if err != nil {
				releaseErr := executor.config.Scheduler.ReleaseAdmission(attempt.AttemptID)
				executor.config.Profiles.releaseAttempt(attempt.AttemptID)
				return errors.Join(err, releaseErr)
			}
			if _, err := executor.config.Scheduler.Start(attempt.AttemptID); err != nil {
				return err
			}
			active[attempt.AttemptID] = batchActiveAttempt{execution: item, handle: handle}
			go func(attemptID string, executionHandle *ExecutionHandle) {
				select {
				case <-executionHandle.Done():
					completions <- batchCompletion{attemptID: attemptID, handle: executionHandle}
				case <-runContext.Done():
				}
			}(attempt.AttemptID, handle)
		}
		return nil
	}

	for completedCount < len(batch) {
		if err := dispatch(); err != nil {
			return nil, err
		}
		startControl()
		if completedCount == len(batch) {
			break
		}
		timer := time.NewTimer(executor.config.PollInterval)
		select {
		case <-ctx.Done():
			stopBatchTimer(timer)
			return nil, ctx.Err()
		case controlErr := <-controlResults:
			stopBatchTimer(timer)
			if controlErr != nil {
				return nil, controlErr
			}
			return nil, ErrConflict
		case completion := <-completions:
			stopBatchTimer(timer)
			entry, ok := active[completion.attemptID]
			if !ok {
				return nil, ErrConflict
			}
			delete(active, completion.attemptID)
			executionResult, ok := completion.handle.Result()
			if !ok {
				return nil, ErrConflict
			}
			attempt, ok := executor.config.Scheduler.attemptSnapshot(completion.attemptID)
			if !ok {
				return nil, ErrNotFound
			}
			if attempt.State == AttemptRunning || attempt.State == AttemptPinned {
				if _, err := executor.config.Scheduler.Complete(completion.attemptID); err != nil {
					attempt, ok = executor.config.Scheduler.attemptSnapshot(completion.attemptID)
					if !ok || attempt.State != AttemptEvictionRequested && attempt.State != AttemptReclaimed {
						return nil, err
					}
				}
			}
			if attempt.State == AttemptEvictionRequested || attempt.State == AttemptReclaimed {
				continue
			}
			index := order[entry.execution.Spec.TaskID]
			if completed[index] {
				return nil, ErrConflict
			}
			completed[index] = true
			completedCount++
			results[index] = ProfiledExecutionResult{
				TaskID: entry.execution.Spec.TaskID, AttemptID: completion.attemptID,
				Response: append([]byte(nil), executionResult.Response...), Err: executionResult.Err,
				StartedAt: executionResult.StartedAt, FinishedAt: executionResult.FinishedAt,
			}
		case <-timer.C:
		}
	}
	cancel()
	if controlStarted {
		if controlErr := <-controlResults; controlErr != nil && !errors.Is(controlErr, context.Canceled) {
			return nil, controlErr
		}
	}
	return results, nil
}

func stopBatchTimer(timer *time.Timer) {
	if timer == nil || timer.Stop() {
		return
	}
	select {
	case <-timer.C:
	default:
	}
}

func (scheduler *Scheduler) attemptSnapshot(attemptID string) (AttemptSnapshot, bool) {
	if scheduler == nil {
		return AttemptSnapshot{}, false
	}
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	record := scheduler.attempts[attemptID]
	if record == nil {
		return AttemptSnapshot{}, false
	}
	return record.snapshot, true
}

func (store *ProfileStore) releaseAttempt(attemptID string) {
	if store == nil {
		return
	}
	store.mu.Lock()
	delete(store.attempts, attemptID)
	store.mu.Unlock()
}

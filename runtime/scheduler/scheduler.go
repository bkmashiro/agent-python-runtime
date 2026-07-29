package scheduler

import (
	"fmt"
	"sort"
	"sync"
)

type taskRecord struct {
	spec     TaskSpec
	snapshot TaskSnapshot
}

type attemptRecord struct {
	snapshot AttemptSnapshot
}

type Scheduler struct {
	mu sync.Mutex

	config              Config
	tasks               map[string]*taskRecord
	attempts            map[string]*attemptRecord
	queue               []string
	reservedBytes       uint64
	observedBytes       uint64
	pressured           bool
	nextTaskSequence    uint64
	nextAttemptSequence uint64
}

func New(config Config) (*Scheduler, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	return &Scheduler{
		config:   config,
		tasks:    make(map[string]*taskRecord),
		attempts: make(map[string]*attemptRecord),
	}, nil
}

func (scheduler *Scheduler) Submit(spec TaskSpec) (TaskSnapshot, error) {
	if scheduler == nil {
		return TaskSnapshot{}, ErrInvalidConfig
	}
	if err := spec.validate(scheduler.config.HardBytes); err != nil {
		return TaskSnapshot{}, err
	}
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	if prior, ok := scheduler.tasks[spec.TaskID]; ok {
		if prior.spec != spec {
			return TaskSnapshot{}, ErrConflict
		}
		return prior.snapshot, nil
	}
	if uint32(len(scheduler.tasks)) >= scheduler.config.MaxTasks {
		return TaskSnapshot{}, ErrCapacity
	}
	scheduler.nextTaskSequence++
	now := scheduler.config.Clock().UTC()
	snapshot := TaskSnapshot{
		TaskID:           spec.TaskID,
		ProfileKey:       spec.ProfileKey,
		Lane:             spec.Lane,
		Priority:         spec.Priority,
		ReservationBytes: spec.ReservationBytes,
		ReservationFloor: spec.ReservationBytes,
		MaxEvictions:     spec.MaxEvictions,
		State:            TaskQueued,
		SubmittedAt:      now,
		Sequence:         scheduler.nextTaskSequence,
	}
	scheduler.tasks[spec.TaskID] = &taskRecord{spec: spec, snapshot: snapshot}
	scheduler.queue = append(scheduler.queue, spec.TaskID)
	return snapshot, nil
}

func (scheduler *Scheduler) Admit() (AttemptSnapshot, error) {
	if scheduler == nil {
		return AttemptSnapshot{}, ErrInvalidConfig
	}
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	return scheduler.admitLocked(nil)
}

type admissionReservationResolver func(TaskSnapshot) (uint64, error)

func (scheduler *Scheduler) admitLocked(resolve admissionReservationResolver) (AttemptSnapshot, error) {
	if scheduler.pressured {
		return AttemptSnapshot{}, ErrNoAdmissibleTask
	}
	if uint32(len(scheduler.attempts)) >= scheduler.config.MaxAttempts {
		return AttemptSnapshot{}, ErrCapacity
	}
	now := scheduler.config.Clock().UTC()
	selected := -1
	var selectedReservation uint64
	for index, taskID := range scheduler.queue {
		task := scheduler.tasks[taskID]
		if task == nil || !retryReady(task.snapshot.State, task.snapshot.NotBefore, now) {
			continue
		}
		reservation := task.snapshot.ReservationFloor
		if resolve != nil {
			var err error
			reservation, err = resolve(task.snapshot)
			if err != nil {
				return AttemptSnapshot{}, err
			}
		}
		if reservation == 0 || reservation > scheduler.config.HighBytes || scheduler.reservedBytes > scheduler.config.HighBytes-reservation {
			continue
		}
		if selected < 0 || queueLess(task.snapshot, scheduler.tasks[scheduler.queue[selected]].snapshot) {
			selected = index
			selectedReservation = reservation
		}
	}
	if selected < 0 {
		return AttemptSnapshot{}, ErrNoAdmissibleTask
	}
	taskID := scheduler.queue[selected]
	scheduler.queue = append(scheduler.queue[:selected], scheduler.queue[selected+1:]...)
	task := scheduler.tasks[taskID]
	ordinal := task.snapshot.Evictions + 1
	scheduler.nextAttemptSequence++
	attemptID := fmt.Sprintf("%s:attempt:%d", taskID, ordinal)
	attempt := AttemptSnapshot{
		AttemptID:     attemptID,
		TaskID:        taskID,
		Ordinal:       ordinal,
		Lane:          task.snapshot.Lane,
		Priority:      task.snapshot.Priority,
		ReservedBytes: selectedReservation,
		State:         AttemptAdmitted,
		AdmittedAt:    now,
		Sequence:      scheduler.nextAttemptSequence,
	}
	scheduler.attempts[attemptID] = &attemptRecord{snapshot: attempt}
	task.snapshot.State = TaskAdmitted
	task.snapshot.CurrentAttemptID = attemptID
	scheduler.reservedBytes += attempt.ReservedBytes
	return attempt, nil
}

func queueLess(left, right TaskSnapshot) bool {
	if left.Priority != right.Priority {
		return left.Priority > right.Priority
	}
	return left.Sequence < right.Sequence
}

func (scheduler *Scheduler) Start(attemptID string) (AttemptSnapshot, error) {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	attempt, ok := scheduler.attempts[attemptID]
	if !ok {
		return AttemptSnapshot{}, ErrNotFound
	}
	if attempt.snapshot.State == AttemptRunning {
		return attempt.snapshot, nil
	}
	if attempt.snapshot.State != AttemptAdmitted {
		return AttemptSnapshot{}, ErrConflict
	}
	now := scheduler.config.Clock().UTC()
	attempt.snapshot.State = AttemptRunning
	attempt.snapshot.StartedAt = now
	task := scheduler.tasks[attempt.snapshot.TaskID]
	task.snapshot.State = TaskRunning
	return attempt.snapshot, nil
}

func (scheduler *Scheduler) Complete(attemptID string) (AttemptSnapshot, error) {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	attempt, ok := scheduler.attempts[attemptID]
	if !ok {
		return AttemptSnapshot{}, ErrNotFound
	}
	if attempt.snapshot.State == AttemptCompleted {
		return attempt.snapshot, nil
	}
	if attempt.snapshot.State != AttemptRunning && attempt.snapshot.State != AttemptPinned {
		return AttemptSnapshot{}, ErrConflict
	}
	if scheduler.reservedBytes < attempt.snapshot.ReservedBytes {
		return AttemptSnapshot{}, ErrConflict
	}
	attempt.snapshot.State = AttemptCompleted
	attempt.snapshot.CompletedAt = scheduler.config.Clock().UTC()
	scheduler.reservedBytes -= attempt.snapshot.ReservedBytes
	task := scheduler.tasks[attempt.snapshot.TaskID]
	task.snapshot.State = TaskCompleted
	task.snapshot.CurrentAttemptID = attemptID
	return attempt.snapshot, nil
}

func (scheduler *Scheduler) Snapshot() Snapshot {
	if scheduler == nil {
		return Snapshot{}
	}
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()
	result := Snapshot{ReservedBytes: scheduler.reservedBytes, ObservedBytes: scheduler.observedBytes, Pressured: scheduler.pressured}
	for _, taskID := range scheduler.queue {
		if task := scheduler.tasks[taskID]; task != nil {
			result.Queued = append(result.Queued, task.snapshot)
		}
	}
	sort.Slice(result.Queued, func(left, right int) bool { return queueLess(result.Queued[left], result.Queued[right]) })
	for _, attempt := range scheduler.attempts {
		result.Attempts = append(result.Attempts, attempt.snapshot)
	}
	sort.Slice(result.Attempts, func(left, right int) bool { return result.Attempts[left].Sequence < result.Attempts[right].Sequence })
	return result
}

// Package scheduling provides a deterministic, trace-driven model for
// evaluating admission and live-I/O policies without changing runtime semantics.
package scheduling

import (
	"container/heap"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"
)

// PhaseKind identifies an explicit runtime phase.
type PhaseKind string

const (
	CPU         PhaseKind = "cpu"
	ExternalIO  PhaseKind = "external_io"
	DurableWait PhaseKind = "durable_wait"
)

// Phase is one measured or controlled phase in a task trace.
type Phase struct {
	Kind     PhaseKind     `json:"kind"`
	Duration time.Duration `json:"duration_ns"`
}

// Task is a deterministic trace. ResidentBytes is an estimate used only for
// byte-time accounting; admission is bounded by MaxResident task count.
type Task struct {
	ID            string        `json:"id"`
	Arrival       time.Duration `json:"arrival_ns"`
	Priority      int           `json:"priority,omitempty"`
	ResidentBytes int64         `json:"resident_bytes,omitempty"`
	Phases        []Phase       `json:"phases"`
}

// Policy chooses among runnable CPU/inline-I/O phases. It does not preempt a
// running phase.
type Policy string

const (
	FIFO       Policy = "fifo"
	ReadyFirst Policy = "ready_first"
	FinishSoon Policy = "finish_soon"
)

// Config defines bounded resources. MaxQueued=0 means unlimited in this
// research model; production admission can still use a strict zero-length queue.
type Config struct {
	MaxRunning       int    `json:"max_running"`
	MaxResident      int    `json:"max_resident"`
	MaxInflightTools int    `json:"max_inflight_tools"`
	MaxQueued        int    `json:"max_queued,omitempty"`
	LiveIO           bool   `json:"live_io"`
	Policy           Policy `json:"policy"`
}

// TaskResult reports one task's simulated terminal state.
type TaskResult struct {
	CompletedAt time.Duration `json:"completed_at_ns,omitempty"`
	Latency     time.Duration `json:"latency_ns,omitempty"`
	Rejected    bool          `json:"rejected,omitempty"`
}

// Result reports deterministic population outcomes and resource-time areas.
type Result struct {
	Config             Config                `json:"config"`
	Completed          int                   `json:"completed"`
	Rejected           int                   `json:"rejected"`
	Makespan           time.Duration         `json:"makespan_ns"`
	MeanLatency        time.Duration         `json:"mean_latency_ns"`
	P95Latency         time.Duration         `json:"p95_latency_ns"`
	PeakRunning        int                   `json:"peak_running"`
	PeakResident       int                   `json:"peak_resident"`
	PeakInflightTools  int                   `json:"peak_inflight_tools"`
	RunningSlotTime    time.Duration         `json:"running_slot_time_ns"`
	ToolSlotTime       time.Duration         `json:"tool_slot_time_ns"`
	ResidentMiBSeconds float64               `json:"resident_mib_seconds"`
	Tasks              map[string]TaskResult `json:"tasks"`
}

type eventKind uint8

const (
	eventArrival eventKind = iota + 1
	eventCPUDone
	eventIODone
	eventWaitDone
)

type event struct {
	at    time.Duration
	seq   uint64
	kind  eventKind
	task  *taskState
	phase int
}

type eventHeap []event

func (events eventHeap) Len() int { return len(events) }
func (events eventHeap) Less(left, right int) bool {
	if events[left].at != events[right].at {
		return events[left].at < events[right].at
	}
	return events[left].seq < events[right].seq
}
func (events eventHeap) Swap(left, right int) {
	events[left], events[right] = events[right], events[left]
}
func (events *eventHeap) Push(value any) { *events = append(*events, value.(event)) }
func (events *eventHeap) Pop() any {
	old := *events
	last := old[len(old)-1]
	*events = old[:len(old)-1]
	return last
}

type taskState struct {
	task         Task
	phase        int
	admitted     bool
	continuation bool
	readySeq     uint64
	holdsRunning bool
	inToolQueue  bool
	completed    bool
	rejected     bool
}

type simulator struct {
	config Config
	now    time.Duration
	seq    uint64
	events eventHeap

	states        []*taskState
	ready         []*taskState
	admission     []*taskState
	toolQueue     []*taskState
	running       int
	resident      int
	tools         int
	residentBytes int64

	result Result
}

// Simulate evaluates one fixed trace. Equal-time events are processed as a
// batch before policy decisions, making results independent of heap tie order.
func Simulate(tasks []Task, config Config) (Result, error) {
	if err := validateSimulation(tasks, config); err != nil {
		return Result{}, err
	}
	sim := &simulator{
		config: config,
		result: Result{Config: config, Tasks: make(map[string]TaskResult, len(tasks))},
	}
	heap.Init(&sim.events)
	for _, task := range tasks {
		state := &taskState{task: task}
		sim.states = append(sim.states, state)
		sim.pushEvent(task.Arrival, eventArrival, state)
	}
	for sim.events.Len() > 0 {
		nextAt := sim.events[0].at
		sim.integrate(nextAt - sim.now)
		sim.now = nextAt
		var batch []event
		for sim.events.Len() > 0 && sim.events[0].at == nextAt {
			batch = append(batch, heap.Pop(&sim.events).(event))
		}
		for _, next := range batch {
			if err := sim.handle(next); err != nil {
				return Result{}, err
			}
		}
		sim.drain()
		sim.enforceQueueLimit()
	}
	if sim.result.Completed+sim.result.Rejected != len(tasks) {
		return Result{}, errors.New("simulation stopped with unfinished tasks")
	}
	sim.finishResult()
	return sim.result, nil
}

func validateSimulation(tasks []Task, config Config) error {
	if len(tasks) == 0 || config.MaxRunning < 1 || config.MaxResident < config.MaxRunning || config.MaxInflightTools < 1 || config.MaxQueued < 0 {
		return errors.New("invalid scheduling simulation bounds")
	}
	switch config.Policy {
	case FIFO, ReadyFirst, FinishSoon:
	default:
		return fmt.Errorf("unknown scheduling policy %q", config.Policy)
	}
	seen := make(map[string]struct{}, len(tasks))
	for _, task := range tasks {
		if task.ID == "" || task.Arrival < 0 || task.ResidentBytes < 0 || len(task.Phases) == 0 || task.Phases[0].Kind != CPU {
			return fmt.Errorf("invalid task %q", task.ID)
		}
		if _, exists := seen[task.ID]; exists {
			return fmt.Errorf("duplicate task %q", task.ID)
		}
		seen[task.ID] = struct{}{}
		for index, phase := range task.Phases {
			if phase.Duration <= 0 {
				return fmt.Errorf("task %q phase %d has non-positive duration", task.ID, index)
			}
			switch phase.Kind {
			case CPU, ExternalIO, DurableWait:
			default:
				return fmt.Errorf("task %q phase %d has unknown kind %q", task.ID, index, phase.Kind)
			}
			if phase.Kind == DurableWait && index+1 < len(task.Phases) && task.Phases[index+1].Kind != CPU {
				return fmt.Errorf("task %q must re-enter through CPU after durable wait", task.ID)
			}
		}
	}
	return nil
}

func (sim *simulator) handle(next event) error {
	state := next.task
	if state.completed || state.rejected {
		return nil
	}
	switch next.kind {
	case eventArrival:
		sim.enqueueAdmission(state, false)
	case eventCPUDone:
		if next.phase != state.phase || !state.holdsRunning {
			return errors.New("stale CPU completion")
		}
		state.phase++
		if state.phase < len(state.task.Phases) && state.task.Phases[state.phase].Kind == ExternalIO && !sim.config.LiveIO {
			sim.enqueueTool(state)
			break
		}
		sim.running--
		state.holdsRunning = false
		sim.advance(state)
	case eventIODone:
		if next.phase != state.phase || !state.inToolQueue {
			return errors.New("stale external I/O completion")
		}
		sim.tools--
		state.inToolQueue = false
		state.phase++
		if state.holdsRunning {
			if state.phase < len(state.task.Phases) && state.task.Phases[state.phase].Kind == CPU {
				sim.pushEvent(sim.now+state.task.Phases[state.phase].Duration, eventCPUDone, state)
				break
			}
			sim.running--
			state.holdsRunning = false
		}
		sim.advance(state)
	case eventWaitDone:
		if next.phase != state.phase || state.admitted {
			return errors.New("stale durable-wait completion")
		}
		state.phase++
		sim.advance(state)
	default:
		return errors.New("unknown simulation event")
	}
	return nil
}

func (sim *simulator) advance(state *taskState) {
	if state.phase >= len(state.task.Phases) {
		sim.complete(state)
		return
	}
	phase := state.task.Phases[state.phase]
	if !state.admitted {
		sim.enqueueAdmission(state, state.phase > 0)
		return
	}
	switch phase.Kind {
	case CPU:
		sim.enqueueReady(state, state.phase > 0)
	case ExternalIO:
		state.continuation = true
		if sim.config.LiveIO {
			sim.enqueueTool(state)
		} else {
			sim.enqueueReady(state, true)
		}
	case DurableWait:
		sim.releaseResident(state)
		sim.pushEvent(sim.now+phase.Duration, eventWaitDone, state)
	}
}

func (sim *simulator) enqueueAdmission(state *taskState, continuation bool) {
	state.continuation = continuation
	state.readySeq = sim.nextSeq()
	sim.admission = append(sim.admission, state)
}

func (sim *simulator) enqueueReady(state *taskState, continuation bool) {
	state.continuation = continuation
	state.readySeq = sim.nextSeq()
	sim.ready = append(sim.ready, state)
}

func (sim *simulator) enqueueTool(state *taskState) {
	state.inToolQueue = true
	sim.toolQueue = append(sim.toolQueue, state)
}

func (sim *simulator) drain() {
	for {
		progress := false
		for sim.tools < sim.config.MaxInflightTools && len(sim.toolQueue) > 0 {
			state := sim.toolQueue[0]
			sim.toolQueue = sim.toolQueue[1:]
			sim.tools++
			sim.updatePeaks()
			sim.pushEvent(sim.now+state.task.Phases[state.phase].Duration, eventIODone, state)
			progress = true
		}
		if sim.running < sim.config.MaxRunning {
			state, fromAdmission := sim.chooseRunnable()
			if state != nil {
				if fromAdmission {
					sim.admit(state)
				}
				phase := state.task.Phases[state.phase]
				state.holdsRunning = true
				sim.running++
				sim.updatePeaks()
				if phase.Kind == CPU {
					sim.pushEvent(sim.now+phase.Duration, eventCPUDone, state)
				} else {
					sim.enqueueTool(state)
				}
				progress = true
			}
		}
		if !progress {
			return
		}
	}
}

func (sim *simulator) chooseRunnable() (*taskState, bool) {
	type candidate struct {
		state         *taskState
		fromAdmission bool
	}
	candidates := make([]candidate, 0, len(sim.ready)+len(sim.admission))
	for _, state := range sim.ready {
		candidates = append(candidates, candidate{state: state})
	}
	if sim.resident < sim.config.MaxResident {
		for _, state := range sim.admission {
			candidates = append(candidates, candidate{state: state, fromAdmission: true})
		}
	}
	if len(candidates) == 0 {
		return nil, false
	}
	best := 0
	for index := 1; index < len(candidates); index++ {
		if sim.prefer(candidates[index].state, candidates[best].state) {
			best = index
		}
	}
	chosen := candidates[best]
	if chosen.fromAdmission {
		sim.admission = removeState(sim.admission, chosen.state)
	} else {
		sim.ready = removeState(sim.ready, chosen.state)
	}
	return chosen.state, chosen.fromAdmission
}

func (sim *simulator) prefer(left, right *taskState) bool {
	switch sim.config.Policy {
	case ReadyFirst:
		if left.continuation != right.continuation {
			return left.continuation
		}
	case FinishSoon:
		leftRemaining, rightRemaining := remainingDuration(left), remainingDuration(right)
		if leftRemaining != rightRemaining {
			return leftRemaining < rightRemaining
		}
		if left.task.Priority != right.task.Priority {
			return left.task.Priority > right.task.Priority
		}
	}
	return left.readySeq < right.readySeq
}

func remainingDuration(state *taskState) time.Duration {
	var total time.Duration
	for _, phase := range state.task.Phases[state.phase:] {
		total += phase.Duration
	}
	return total
}

func (sim *simulator) admit(state *taskState) {
	state.admitted = true
	sim.resident++
	sim.residentBytes += state.task.ResidentBytes
	sim.updatePeaks()
}

func (sim *simulator) releaseResident(state *taskState) {
	if !state.admitted {
		return
	}
	state.admitted = false
	sim.resident--
	sim.residentBytes -= state.task.ResidentBytes
}

func (sim *simulator) complete(state *taskState) {
	state.completed = true
	sim.releaseResident(state)
	sim.result.Completed++
	sim.result.Tasks[state.task.ID] = TaskResult{CompletedAt: sim.now, Latency: sim.now - state.task.Arrival}
	if sim.now > sim.result.Makespan {
		sim.result.Makespan = sim.now
	}
}

func (sim *simulator) enforceQueueLimit() {
	if sim.config.MaxQueued == 0 {
		return
	}
	for len(sim.admission) > sim.config.MaxQueued {
		newest := 0
		for index := 1; index < len(sim.admission); index++ {
			if sim.admission[index].readySeq > sim.admission[newest].readySeq {
				newest = index
			}
		}
		state := sim.admission[newest]
		sim.admission = append(sim.admission[:newest], sim.admission[newest+1:]...)
		state.rejected = true
		sim.result.Rejected++
		sim.result.Tasks[state.task.ID] = TaskResult{Rejected: true}
	}
}

func (sim *simulator) integrate(delta time.Duration) {
	if delta <= 0 {
		return
	}
	sim.result.RunningSlotTime += time.Duration(sim.running) * delta
	sim.result.ToolSlotTime += time.Duration(sim.tools) * delta
	sim.result.ResidentMiBSeconds += float64(sim.residentBytes) / float64(1<<20) * delta.Seconds()
}

func (sim *simulator) updatePeaks() {
	if sim.running > sim.result.PeakRunning {
		sim.result.PeakRunning = sim.running
	}
	if sim.resident > sim.result.PeakResident {
		sim.result.PeakResident = sim.resident
	}
	if sim.tools > sim.result.PeakInflightTools {
		sim.result.PeakInflightTools = sim.tools
	}
}

func (sim *simulator) finishResult() {
	latencies := make([]time.Duration, 0, sim.result.Completed)
	var total time.Duration
	for _, task := range sim.result.Tasks {
		if task.Rejected {
			continue
		}
		latencies = append(latencies, task.Latency)
		total += task.Latency
	}
	if len(latencies) == 0 {
		return
	}
	sort.Slice(latencies, func(left, right int) bool { return latencies[left] < latencies[right] })
	sim.result.MeanLatency = total / time.Duration(len(latencies))
	index := int(math.Ceil(0.95*float64(len(latencies)))) - 1
	sim.result.P95Latency = latencies[index]
}

func (sim *simulator) pushEvent(at time.Duration, kind eventKind, state *taskState) {
	heap.Push(&sim.events, event{at: at, seq: sim.nextSeq(), kind: kind, task: state, phase: state.phase})
}

func (sim *simulator) nextSeq() uint64 {
	sim.seq++
	return sim.seq
}

func removeState(states []*taskState, target *taskState) []*taskState {
	for index, state := range states {
		if state == target {
			return append(states[:index], states[index+1:]...)
		}
	}
	return states
}

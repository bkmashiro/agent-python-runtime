package durable

import (
	"errors"
	"sort"
	"sync"
	"time"
)

// ToolOperation distinguishes an ordinary Host dispatch from recovery lookup.
type ToolOperation string

const (
	ToolCall   ToolOperation = "call"
	ToolLookup ToolOperation = "lookup"
)

// ToolOutcome is the scheduler-facing result class of one Host operation.
type ToolOutcome string

const (
	ToolSucceeded ToolOutcome = "succeeded"
	ToolFailed    ToolOutcome = "failed"
	ToolCancelled ToolOutcome = "cancelled"
	ToolDeadline  ToolOutcome = "deadline"
)

// ToolObservation records time spent at explicit Host-tool boundaries. Queue is
// the wait for external-tool capacity; service is the Host callback; resume is
// the wait to reacquire a running slot before Python continues.
type ToolObservation struct {
	Name            string
	Version         string
	Operation       ToolOperation
	Scheduling      SchedulingClass
	OperationKey    string
	ArgumentBytes   int
	QueueDuration   time.Duration
	ServiceDuration time.Duration
	ResumeDuration  time.Duration
	Outcome         ToolOutcome
}

// ToolObserver receives synchronous observations. Implementations should be
// bounded and non-blocking because notification runs on the attempt goroutine.
type ToolObserver interface {
	ObserveTool(ToolObservation)
}

// ToolObserverFunc adapts a function to ToolObserver.
type ToolObserverFunc func(ToolObservation)

func (function ToolObserverFunc) ObserveTool(observation ToolObservation) {
	if function != nil {
		function(observation)
	}
}

// ToolEstimate is one stable, payload-bucketed aggregate.
type ToolEstimate struct {
	Name           string          `json:"name"`
	Version        string          `json:"version"`
	Operation      ToolOperation   `json:"operation"`
	Scheduling     SchedulingClass `json:"scheduling"`
	ArgumentBucket string          `json:"argument_bucket"`
	Count          uint64          `json:"count"`
	Failures       uint64          `json:"failures"`
	Cancellations  uint64          `json:"cancellations"`
	Deadlines      uint64          `json:"deadlines"`
	MeanQueue      time.Duration   `json:"mean_queue_ns"`
	MeanService    time.Duration   `json:"mean_service_ns"`
	MeanResume     time.Duration   `json:"mean_resume_ns"`
	EWMAQueue      time.Duration   `json:"ewma_queue_ns"`
	EWMAService    time.Duration   `json:"ewma_service_ns"`
	EWMAResume     time.Duration   `json:"ewma_resume_ns"`
}

type toolEstimateKey struct {
	name, version, bucket string
	operation             ToolOperation
	scheduling            SchedulingClass
}

type toolEstimateState struct {
	count, failures, cancellations, deadlines uint64
	queueTotal, serviceTotal, resumeTotal     int64
	queueEWMA, serviceEWMA, resumeEWMA        float64
}

// ToolLatencyStats maintains bounded-cost means and EWMAs. It creates one
// entry per tool/version/operation/scheduling/payload-bucket combination.
type ToolLatencyStats struct {
	mu      sync.Mutex
	alpha   float64
	entries map[toolEstimateKey]*toolEstimateState
}

// NewToolLatencyStats constructs an aggregate with 0 < alpha <= 1.
func NewToolLatencyStats(alpha float64) (*ToolLatencyStats, error) {
	if alpha <= 0 || alpha > 1 {
		return nil, errors.New("tool latency EWMA alpha must be in (0, 1]")
	}
	return &ToolLatencyStats{alpha: alpha, entries: make(map[toolEstimateKey]*toolEstimateState)}, nil
}

// ObserveTool updates one aggregate in constant time.
func (stats *ToolLatencyStats) ObserveTool(observation ToolObservation) {
	if stats == nil {
		return
	}
	key := toolEstimateKey{
		name: observation.Name, version: observation.Version,
		operation: observation.Operation, scheduling: observation.Scheduling,
		bucket: toolArgumentBucket(observation.ArgumentBytes),
	}
	stats.mu.Lock()
	defer stats.mu.Unlock()
	state := stats.entries[key]
	if state == nil {
		state = &toolEstimateState{}
		stats.entries[key] = state
	}
	state.count++
	state.queueTotal += observation.QueueDuration.Nanoseconds()
	state.serviceTotal += observation.ServiceDuration.Nanoseconds()
	state.resumeTotal += observation.ResumeDuration.Nanoseconds()
	if state.count == 1 {
		state.queueEWMA = float64(observation.QueueDuration)
		state.serviceEWMA = float64(observation.ServiceDuration)
		state.resumeEWMA = float64(observation.ResumeDuration)
	} else {
		state.queueEWMA = ewma(state.queueEWMA, float64(observation.QueueDuration), stats.alpha)
		state.serviceEWMA = ewma(state.serviceEWMA, float64(observation.ServiceDuration), stats.alpha)
		state.resumeEWMA = ewma(state.resumeEWMA, float64(observation.ResumeDuration), stats.alpha)
	}
	switch observation.Outcome {
	case ToolFailed:
		state.failures++
	case ToolCancelled:
		state.cancellations++
	case ToolDeadline:
		state.deadlines++
	}
}

// Snapshot returns deterministic copies suitable for metrics export or policy
// estimation. Duration JSON fields are nanoseconds.
func (stats *ToolLatencyStats) Snapshot() []ToolEstimate {
	if stats == nil {
		return nil
	}
	stats.mu.Lock()
	defer stats.mu.Unlock()
	result := make([]ToolEstimate, 0, len(stats.entries))
	for key, state := range stats.entries {
		count := int64(state.count)
		result = append(result, ToolEstimate{
			Name: key.name, Version: key.version, Operation: key.operation,
			Scheduling: key.scheduling, ArgumentBucket: key.bucket,
			Count: state.count, Failures: state.failures,
			Cancellations: state.cancellations, Deadlines: state.deadlines,
			MeanQueue:   time.Duration(state.queueTotal / count),
			MeanService: time.Duration(state.serviceTotal / count),
			MeanResume:  time.Duration(state.resumeTotal / count),
			EWMAQueue:   time.Duration(state.queueEWMA),
			EWMAService: time.Duration(state.serviceEWMA),
			EWMAResume:  time.Duration(state.resumeEWMA),
		})
	}
	sort.Slice(result, func(left, right int) bool {
		a, b := result[left], result[right]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if a.Version != b.Version {
			return a.Version < b.Version
		}
		if a.Operation != b.Operation {
			return a.Operation < b.Operation
		}
		if a.Scheduling != b.Scheduling {
			return a.Scheduling < b.Scheduling
		}
		return a.ArgumentBucket < b.ArgumentBucket
	})
	return result
}

func ewma(previous, current, alpha float64) float64 {
	return alpha*current + (1-alpha)*previous
}

func toolArgumentBucket(bytes int) string {
	switch {
	case bytes <= 1<<10:
		return "le_1k"
	case bytes <= 16<<10:
		return "le_16k"
	case bytes <= 256<<10:
		return "le_256k"
	default:
		return "gt_256k"
	}
}

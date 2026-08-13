package agentfunction

import (
	"context"
	"errors"
	"sync"
)

var ErrFlightPanic = errors.New("agent function single-flight leader panicked")

type FlightStats struct {
	Leaders  uint64
	Waiters  uint64
	InFlight uint64
}

type flight struct {
	done   chan struct{}
	result Result
	err    error
}

// FlightGroup collapses concurrent identical invocations only. It retains no
// completed value and is independent of Store.
type FlightGroup struct {
	mu      sync.Mutex
	flights map[string]*flight
	stats   FlightStats
}

func NewFlightGroup() *FlightGroup {
	return &FlightGroup{flights: make(map[string]*flight)}
}

func (group *FlightGroup) Do(ctx context.Context, key string, function func() (Result, error)) (Result, error) {
	if group == nil || function == nil {
		return function()
	}
	group.mu.Lock()
	if existing, ok := group.flights[key]; ok {
		group.stats.Waiters++
		group.mu.Unlock()
		select {
		case <-existing.done:
			result := existing.result
			result.Value = append([]byte(nil), result.Value...)
			result.Shared = true
			result.Disposition = Waiter
			return result, existing.err
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
	}
	current := &flight{done: make(chan struct{})}
	group.flights[key] = current
	group.stats.Leaders++
	group.stats.InFlight++
	group.mu.Unlock()

	current.result, current.err = runFlight(function)
	current.result.Value = append([]byte(nil), current.result.Value...)
	if current.err == nil && current.result.Disposition == "" {
		current.result.Disposition = Leader
	}

	group.mu.Lock()
	delete(group.flights, key)
	group.stats.InFlight--
	close(current.done)
	group.mu.Unlock()
	return current.result, current.err
}

func runFlight(function func() (Result, error)) (result Result, err error) {
	defer func() {
		if recover() != nil {
			result = Result{}
			err = ErrFlightPanic
		}
	}()
	return function()
}

func (group *FlightGroup) Stats() FlightStats {
	if group == nil {
		return FlightStats{}
	}
	group.mu.Lock()
	defer group.mu.Unlock()
	return group.stats
}

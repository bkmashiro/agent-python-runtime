package wazero

import "testing"

func TestPreparedDeficitSchedulingDeduplicatesOutstandingWork(t *testing.T) {
	pool := &preparedPool{
		ready:    make(chan *preparedInstance, 8),
		requests: make(chan preparedRefillRequest, 8),
		maximum:  8,
	}
	pool.capacity.Store(4)
	pool.ready <- &preparedInstance{}
	engine := &Engine{pool: pool}

	if scheduled := engine.schedulePreparedDeficit(nil); scheduled != 3 {
		t.Fatalf("scheduled=%d, want 3", scheduled)
	}
	if scheduled := engine.schedulePreparedDeficit(nil); scheduled != 0 {
		t.Fatalf("duplicate scheduled=%d, want 0", scheduled)
	}
	if got := len(pool.requests); got != 3 {
		t.Fatalf("queued requests=%d, want 3", got)
	}
	if got := pool.queued.Load(); got != 3 {
		t.Fatalf("queued counter=%d, want 3", got)
	}
	state := engine.PreparedPoolState()
	if state.TargetCapacity != 4 || state.Ready != 1 || state.Queued != 3 || state.SupplyAccounted != 4 ||
		state.Floor > state.Critical || state.Critical > state.Low || state.Low > state.High {
		t.Fatalf("invalid prepared state: %+v", state)
	}
}

func TestPreparedDeficitCountsRefillingWork(t *testing.T) {
	pool := &preparedPool{
		ready:    make(chan *preparedInstance, 8),
		requests: make(chan preparedRefillRequest, 8),
		maximum:  8,
	}
	pool.capacity.Store(4)
	pool.ready <- &preparedInstance{}
	pool.refilling.Store(2)
	pool.queued.Store(1)
	engine := &Engine{pool: pool}
	if scheduled := engine.schedulePreparedDeficit(nil); scheduled != 0 {
		t.Fatalf("scheduled=%d with outstanding work", scheduled)
	}
}

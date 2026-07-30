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

func TestPreparedDeficitBoundsAutomaticOutstandingWorkByPressure(t *testing.T) {
	tests := []struct {
		name    string
		ready   uint32
		waiting uint32
		want    uint32
	}{
		{name: "normal", ready: 40, want: 4},
		{name: "low", ready: 24, want: 8},
		{name: "critical", ready: 12, want: 12},
		{name: "waiting", ready: 40, waiting: 1, want: 12},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pool := &preparedPool{
				ready:           make(chan *preparedInstance, 64),
				requests:        make(chan preparedRefillRequest, 64),
				maximum:         64,
				refillWorkers:   12,
				automaticRefill: true,
			}
			pool.capacity.Store(64)
			pool.waiting.Store(test.waiting)
			for index := uint32(0); index < test.ready; index++ {
				pool.ready <- &preparedInstance{}
			}
			engine := &Engine{pool: pool}
			if got := engine.schedulePreparedDeficit(nil); got != test.want {
				t.Fatalf("scheduled=%d, want %d", got, test.want)
			}
			if got := pool.queued.Load(); got != test.want {
				t.Fatalf("queued=%d, want %d", got, test.want)
			}
		})
	}
}

func TestPreparedDeficitPreservesExplicitWorkerBound(t *testing.T) {
	pool := &preparedPool{
		ready:         make(chan *preparedInstance, 64),
		requests:      make(chan preparedRefillRequest, 64),
		maximum:       64,
		refillWorkers: 6,
	}
	pool.capacity.Store(64)
	pool.waiting.Store(1)
	engine := &Engine{pool: pool}
	if got := engine.schedulePreparedDeficit(nil); got != 6 {
		t.Fatalf("scheduled=%d, want explicit worker bound 6", got)
	}
}

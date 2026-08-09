package wazero

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	"github.com/tetratelabs/wazero/api"
)

const (
	maxPreparedCapacity            uint32 = 4
	maxCOWPreparedCapacity         uint32 = 65536
	defaultPreparedRefillWorkers   uint32 = 4
	lowPreparedRefillWorkers       uint32 = 8
	criticalPreparedRefillWorkers  uint32 = 12
	maxPreparedRefillWorkers       uint32 = 16
	adaptivePreparedRefillSentinel uint32 = 17
)

type preparedInstance struct {
	module          api.Module
	stderr          *bytes.Buffer
	memoryBytes     uint64
	fromPool        bool
	footprintSource preparedFootprintSource
	workspaceGate   *workspaceGate
}

type preparedRefillRequest struct {
	result chan<- error
}

type preparedPool struct {
	ready           chan *preparedInstance
	requests        chan preparedRefillRequest
	context         context.Context
	cancel          context.CancelFunc
	maximum         uint32
	refillWorkers   uint32
	automaticRefill bool

	mutex     sync.Mutex
	closed    bool
	workers   sync.WaitGroup
	capacity  atomic.Uint32
	retained  atomic.Uint64
	queued    atomic.Uint32
	refilling atomic.Uint32
	leased    atomic.Uint32
	executing atomic.Uint32
	waiting   atomic.Uint32
	retiring  atomic.Uint32
	policy    preparedRefillPolicy
}

func (engine *Engine) initializePreparedPool(capacity, maximum, configuredWorkers uint32) error {
	if capacity == 0 {
		return nil
	}
	if maximum < capacity || maximum == 0 {
		return errors.New("prepared pool maximum is below its initial capacity")
	}
	poolContext, cancel := context.WithCancel(context.Background())
	pool := &preparedPool{
		ready:    make(chan *preparedInstance, maximum),
		requests: make(chan preparedRefillRequest, maximum),
		context:  poolContext,
		cancel:   cancel,
		maximum:  maximum,
	}
	engine.pool = pool
	if engine.strategy == enginecontract.StrategyCOWReadySingleUse {
		cowRuntime, err := newCOWPreparedRuntime(poolContext, engine)
		if err != nil {
			return fmt.Errorf("initialize COW prepared runtime: %w", err)
		}
		engine.cowRuntime = cowRuntime
	}
	workerCount := maximum
	if configuredWorkers == adaptivePreparedRefillSentinel {
		pool.automaticRefill = true
		if workerCount > criticalPreparedRefillWorkers {
			workerCount = criticalPreparedRefillWorkers
		}
	} else if configuredWorkers > 0 {
		workerCount = configuredWorkers
	} else if workerCount > defaultPreparedRefillWorkers {
		workerCount = defaultPreparedRefillWorkers
	}
	pool.refillWorkers = workerCount
	for worker := uint32(0); worker < workerCount; worker++ {
		pool.workers.Add(1)
		go engine.runPreparedRefillWorker(pool)
	}
	if err := engine.growPreparedPool(poolContext, capacity); err != nil {
		if engine.cowRuntime != nil {
			return err
		}
	}
	return nil
}

func (engine *Engine) runPreparedRefillWorker(pool *preparedPool) {
	defer pool.workers.Done()
	for {
		select {
		case <-pool.context.Done():
			return
		case request := <-pool.requests:
			pool.mutex.Lock()
			pool.queued.Add(^uint32(0))
			pool.refilling.Add(1)
			pool.mutex.Unlock()
			instance, err := engine.prepareSingleUseInstance(pool.context)
			if err == nil {
				err = publishPreparedInstance(pool, instance)
			}
			pool.refilling.Add(^uint32(0))
			var retryDelay time.Duration
			if err == nil {
				pool.policy.noteSuccess()
			} else {
				retryDelay = pool.policy.noteFailure(time.Now())
			}
			if request.result != nil {
				request.result <- err
				continue
			}
			if err == nil {
				engine.schedulePreparedDeficit(nil)
				continue
			}
			timer := time.NewTimer(retryDelay)
			select {
			case <-pool.context.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return
			case <-timer.C:
				engine.schedulePreparedDeficit(nil)
			}
		}
	}
}

func publishPreparedInstance(pool *preparedPool, instance *preparedInstance) error {
	if instance == nil {
		return errors.New("prepared refill produced a nil instance")
	}
	pool.mutex.Lock()
	closed := pool.closed
	pool.mutex.Unlock()
	if closed {
		_ = instance.module.Close(context.Background())
		return errors.New("prepared pool is closed")
	}
	instance.fromPool = true
	pool.retained.Add(instance.memoryBytes)
	select {
	case pool.ready <- instance:
		return nil
	case <-pool.context.Done():
		subtractRetained(pool, instance.memoryBytes)
		_ = instance.module.Close(context.Background())
		return pool.context.Err()
	}
}

func (engine *Engine) closeServedInstance(instance *preparedInstance) error {
	if instance == nil || instance.module == nil {
		return nil
	}
	pool := engine.pool
	if instance.fromPool && pool != nil {
		pool.retiring.Add(1)
		err := instance.module.Close(context.Background())
		pool.leased.Add(^uint32(0))
		pool.retiring.Add(^uint32(0))
		return err
	}
	return instance.module.Close(context.Background())
}

// GrowPreparedCapacity admits additional never-served slots into the existing
// pool. It never constructs another Runtime, CompiledModule, or COW baseline.
func (engine *Engine) GrowPreparedCapacity(ctx context.Context, additional uint32) error {
	if engine == nil || engine.pool == nil {
		return errors.New("prepared pool is unavailable")
	}
	if engine.cowRuntime == nil {
		return errors.New("dynamic prepared growth is restricted to the COW strategy")
	}
	return engine.growPreparedPool(ctx, additional)
}

func (engine *Engine) growPreparedPool(ctx context.Context, additional uint32) error {
	if additional == 0 {
		return nil
	}
	pool := engine.pool
	pool.mutex.Lock()
	if pool.closed {
		pool.mutex.Unlock()
		return errors.New("prepared pool is closed")
	}
	current := pool.capacity.Load()
	if additional > pool.maximum-current {
		pool.mutex.Unlock()
		return fmt.Errorf("prepared capacity %d exceeds configured maximum %d", current+additional, pool.maximum)
	}
	pool.capacity.Store(current + additional)
	pool.mutex.Unlock()

	results := make(chan error, pool.maximum)
	pool.mutex.Lock()
	scheduled := engine.schedulePreparedDeficitLocked(results)
	pool.mutex.Unlock()
	var failures uint32
	var joined error
	for index := uint32(0); index < scheduled; index++ {
		select {
		case err := <-results:
			if err != nil {
				failures++
				joined = errors.Join(joined, err)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if failures > 0 {
		pool.mutex.Lock()
		pool.capacity.Store(pool.capacity.Load() - failures)
		pool.mutex.Unlock()
		return fmt.Errorf("prepare %d slots: %w", failures, joined)
	}
	return nil
}

func (engine *Engine) checkoutModule(ctx context.Context) (*preparedInstance, error) {
	if engine.pool != nil {
		started := time.Now()
		if engine.cowRuntime != nil {
			engine.pool.waiting.Add(1)
			select {
			case instance := <-engine.pool.ready:
				engine.pool.waiting.Add(^uint32(0))
				subtractRetained(engine.pool, instance.memoryBytes)
				observe(engine.observer, "pool_wait", started, nil)
				engine.pool.leased.Add(1)
				engine.schedulePreparedDeficit(nil)
				return instance, nil
			case <-ctx.Done():
				engine.pool.waiting.Add(^uint32(0))
				err := fmt.Errorf("wait for COW-ready slot: %w", ctx.Err())
				observe(engine.observer, "pool_wait", started, err)
				return nil, err
			}
		}
		select {
		case instance := <-engine.pool.ready:
			subtractRetained(engine.pool, instance.memoryBytes)
			observe(engine.observer, "pool_hit", started, nil)
			engine.pool.leased.Add(1)
			engine.schedulePreparedDeficit(nil)
			return instance, nil
		default:
			observe(engine.observer, "pool_miss", started, nil)
		}
	}
	return engine.newInitializedModule(ctx, "")
}

func (engine *Engine) prepareSingleUseInstance(parent context.Context) (*preparedInstance, error) {
	prepareContext, cancel := context.WithTimeout(parent, engine.config.Timeout)
	defer cancel()
	if engine.cowRuntime != nil {
		return engine.cowRuntime.prepare(prepareContext, engine, "pool_prepare_")
	}
	instance, err := engine.newInitializedModule(prepareContext, "pool_prepare_")
	if err != nil {
		return nil, err
	}
	if err := engine.warmPreparedInstance(prepareContext, instance, "pool_prepare_"); err != nil {
		_ = instance.module.Close(context.Background())
		return nil, err
	}
	return instance, nil
}

func (engine *Engine) warmPreparedInstance(ctx context.Context, instance *preparedInstance, prefix string) error {
	if engine.preparedWarmupProfile == "" {
		return nil
	}
	if instance == nil || instance.module == nil || instance.stderr == nil {
		return errors.New("prepared warmup requires an initialized instance")
	}
	warmupContext, hostCallGuard := guardInitializationHostCalls(ctx)
	started := time.Now()
	err := callStatusWithBytes(warmupContext, instance.module, "runtime_warmup", []byte(engine.preparedWarmupProfile))
	observe(engine.observer, prefix+"warmup", started, err)
	if err != nil {
		return withGuestDiagnostic(err, instance.stderr.String())
	}
	if err := verifyNoInitializationHostCalls(hostCallGuard); err != nil {
		return err
	}
	instance.stderr.Reset()
	return nil
}

func (engine *Engine) newInitializedModule(ctx context.Context, prefix string) (*preparedInstance, error) {
	ctx, hostCallGuard := guardInitializationHostCalls(ctx)
	guestStderr := &bytes.Buffer{}
	moduleConfig, workspaceGate, err := engine.moduleConfig(guestStderr)
	if err != nil {
		return nil, err
	}
	instantiateStarted := time.Now()
	module, err := engine.runtime.InstantiateModule(
		ctx,
		engine.compiled,
		moduleConfig,
	)
	observe(engine.observer, prefix+"instantiate_guest", instantiateStarted, err)
	if err != nil {
		return nil, fmt.Errorf("instantiate guest: %w", err)
	}
	failed := true
	defer func() {
		if failed {
			_ = module.Close(context.Background())
		}
	}()

	initializeStarted := time.Now()
	err = callNoArgs(ctx, module, "_initialize")
	observe(engine.observer, prefix+"_initialize", initializeStarted, err)
	if err != nil {
		return nil, withGuestDiagnostic(err, guestStderr.String())
	}
	runtimeInitStarted := time.Now()
	err = callStatusWithBytes(ctx, module, "runtime_init", []byte("{}"))
	observe(engine.observer, prefix+"runtime_init", runtimeInitStarted, err)
	if err != nil {
		return nil, withGuestDiagnostic(err, guestStderr.String())
	}
	attachStarted := time.Now()
	err = verifyNoInitializationHostCalls(hostCallGuard)
	observe(engine.observer, prefix+"attach_host_calls", attachStarted, err)
	if err != nil {
		return nil, err
	}
	guestStderr.Reset()
	failed = false
	return &preparedInstance{module: module, stderr: guestStderr, memoryBytes: uint64(module.Memory().Size()), workspaceGate: workspaceGate}, nil
}

func (engine *Engine) schedulePreparedDeficit(result chan<- error) uint32 {
	pool := engine.pool
	if pool == nil {
		return 0
	}
	pool.mutex.Lock()
	defer pool.mutex.Unlock()
	return engine.schedulePreparedDeficitLocked(result)
}

func (engine *Engine) schedulePreparedDeficitLocked(result chan<- error) uint32 {
	pool := engine.pool
	if pool == nil || pool.closed {
		return 0
	}
	target := pool.capacity.Load()
	accounted := uint32(len(pool.ready)) + pool.queued.Load() + pool.refilling.Load()
	if accounted >= target {
		return 0
	}
	deficit := target - accounted
	deficit = pool.policy.schedulingLimit(time.Now(), deficit)
	if result == nil && pool.refillWorkers > 0 {
		limit := pool.refillWorkers
		if pool.automaticRefill {
			limit = automaticPreparedRefillLimit(target, uint32(len(pool.ready)), pool.waiting.Load(), limit)
		}
		outstanding := pool.queued.Load() + pool.refilling.Load()
		if outstanding >= limit {
			return 0
		}
		if available := limit - outstanding; deficit > available {
			deficit = available
		}
	}
	for index := uint32(0); index < deficit; index++ {
		pool.queued.Add(1)
		pool.requests <- preparedRefillRequest{result: result}
	}
	return deficit
}

func automaticPreparedRefillLimit(target, ready, waiting, workerBound uint32) uint32 {
	limit := defaultPreparedRefillWorkers
	_, critical, low, _ := preparedWatermarks(target)
	switch {
	case waiting > 0 || ready <= critical:
		limit = criticalPreparedRefillWorkers
	case ready <= low:
		limit = lowPreparedRefillWorkers
	}
	if limit > workerBound {
		limit = workerBound
	}
	if limit > target {
		limit = target
	}
	return limit
}

// PreparedPoolState is a point-in-time lifecycle snapshot. SupplyAccounted is
// ready + queued + refilling; leased/executing/retiring describe served modules
// and are not counted toward the never-served target.
type PreparedPoolState struct {
	TargetCapacity      uint32 `json:"target_capacity"`
	MaximumCapacity     uint32 `json:"maximum_capacity"`
	Floor               uint32 `json:"floor"`
	Critical            uint32 `json:"critical"`
	Low                 uint32 `json:"low"`
	High                uint32 `json:"high"`
	Ready               uint32 `json:"ready"`
	Leased              uint32 `json:"leased"`
	Executing           uint32 `json:"executing"`
	Waiting             uint32 `json:"waiting"`
	Queued              uint32 `json:"queued"`
	Refilling           uint32 `json:"refilling"`
	Retiring            uint32 `json:"retiring"`
	SupplyAccounted     uint32 `json:"supply_accounted"`
	ConsecutiveFailures uint32 `json:"consecutive_failures"`
	TotalFailures       uint64 `json:"total_failures"`
	BreakerOpen         bool   `json:"breaker_open"`
}

func (engine *Engine) PreparedPoolState() PreparedPoolState {
	if engine == nil || engine.pool == nil {
		return PreparedPoolState{}
	}
	pool := engine.pool
	target := pool.capacity.Load()
	floor, critical, low, high := preparedWatermarks(target)
	ready := uint32(len(pool.ready))
	queued := pool.queued.Load()
	refilling := pool.refilling.Load()
	return PreparedPoolState{
		TargetCapacity: target, MaximumCapacity: pool.maximum,
		Floor: floor, Critical: critical, Low: low, High: high,
		Ready: ready, Leased: pool.leased.Load(), Executing: pool.executing.Load(), Waiting: pool.waiting.Load(),
		Queued: queued, Refilling: refilling, Retiring: pool.retiring.Load(),
		SupplyAccounted:     ready + queued + refilling,
		ConsecutiveFailures: pool.policy.consecutiveFailures.Load(), TotalFailures: pool.policy.totalFailures.Load(),
		BreakerOpen: pool.policy.breakerOpen(time.Now()),
	}
}

// PreparedReady reports the number of never-served initialized instances ready
// for exclusive checkout. It is backend-specific diagnostic information.
func (engine *Engine) PreparedReady() int {
	if engine == nil || engine.pool == nil {
		return 0
	}
	return len(engine.pool.ready)
}

// PreparedCapacity reports the admitted target within the configured pool
// envelope. A refill can make PreparedReady temporarily smaller than this value.
func (engine *Engine) PreparedCapacity() uint32 {
	if engine == nil || engine.pool == nil {
		return 0
	}
	return engine.pool.capacity.Load()
}

// PreparedRefillWorkers reports the replenishment worker bound. In automatic
// mode, only a pressure-dependent subset may have outstanding work.
func (engine *Engine) PreparedRefillWorkers() uint32 {
	if engine == nil || engine.pool == nil {
		return 0
	}
	return engine.pool.refillWorkers
}

// PreparedRefillConcurrencyLimit reports the current outstanding-refill bound.
func (engine *Engine) PreparedRefillConcurrencyLimit() uint32 {
	if engine == nil || engine.pool == nil {
		return 0
	}
	pool := engine.pool
	if !pool.automaticRefill {
		return pool.refillWorkers
	}
	return automaticPreparedRefillLimit(pool.capacity.Load(), uint32(len(pool.ready)), pool.waiting.Load(), pool.refillWorkers)
}

// PreparedRetainedGuestMemoryBytes reports queued candidates' current linear
// memory size. It excludes in-flight Runs and Host/runtime overhead.
func (engine *Engine) PreparedRetainedGuestMemoryBytes() uint64 {
	if engine == nil || engine.pool == nil {
		return 0
	}
	return engine.pool.retained.Load()
}

func subtractRetained(pool *preparedPool, bytes uint64) {
	if bytes > 0 {
		pool.retained.Add(^uint64(bytes - 1))
	}
}

func (engine *Engine) closePreparedPool() {
	pool := engine.pool
	if pool == nil {
		return
	}
	pool.mutex.Lock()
	if pool.closed {
		pool.mutex.Unlock()
		return
	}
	pool.closed = true
	pool.cancel()
	pool.mutex.Unlock()
	pool.workers.Wait()
	for {
		select {
		case instance := <-pool.ready:
			subtractRetained(pool, instance.memoryBytes)
			_ = instance.module.Close(context.Background())
		default:
			pool.capacity.Store(0)
			return
		}
	}
}

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
	maxPreparedCapacity      uint32 = 4
	maxCOWPreparedCapacity   uint32 = 4096
	maxPreparedRefillWorkers uint32 = 4
)

type preparedInstance struct {
	module      api.Module
	stderr      *bytes.Buffer
	memoryBytes uint64
}

type preparedRefillRequest struct {
	result chan<- error
}

type preparedPool struct {
	ready         chan *preparedInstance
	requests      chan preparedRefillRequest
	context       context.Context
	cancel        context.CancelFunc
	maximum       uint32
	refillWorkers uint32

	mutex    sync.Mutex
	closed   bool
	workers  sync.WaitGroup
	capacity atomic.Uint32
	retained atomic.Uint64
}

func (engine *Engine) initializePreparedPool(capacity, maximum uint32) error {
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
	if workerCount > maxPreparedRefillWorkers {
		workerCount = maxPreparedRefillWorkers
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
			instance, err := engine.prepareSingleUseInstance(pool.context)
			if err == nil {
				err = publishPreparedInstance(pool, instance)
			}
			if request.result != nil {
				request.result <- err
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

	results := make(chan error, additional)
	for index := uint32(0); index < additional; index++ {
		select {
		case pool.requests <- preparedRefillRequest{result: results}:
		case <-pool.context.Done():
			return pool.context.Err()
		}
	}
	var failures uint32
	var joined error
	for index := uint32(0); index < additional; index++ {
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
			select {
			case instance := <-engine.pool.ready:
				subtractRetained(engine.pool, instance.memoryBytes)
				observe(engine.observer, "pool_wait", started, nil)
				engine.schedulePreparedRefill()
				return instance, nil
			case <-ctx.Done():
				err := fmt.Errorf("wait for COW-ready slot: %w", ctx.Err())
				observe(engine.observer, "pool_wait", started, err)
				return nil, err
			}
		}
		select {
		case instance := <-engine.pool.ready:
			subtractRetained(engine.pool, instance.memoryBytes)
			observe(engine.observer, "pool_hit", started, nil)
			engine.schedulePreparedRefill()
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
	return engine.newInitializedModule(prepareContext, "pool_prepare_")
}

func (engine *Engine) newInitializedModule(ctx context.Context, prefix string) (*preparedInstance, error) {
	ctx, hostCallGuard := guardInitializationHostCalls(ctx)
	guestStderr := &bytes.Buffer{}
	instantiateStarted := time.Now()
	module, err := engine.runtime.InstantiateModule(
		ctx,
		engine.compiled,
		newModuleConfig(guestStderr),
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
	return &preparedInstance{module: module, stderr: guestStderr, memoryBytes: uint64(module.Memory().Size())}, nil
}

func (engine *Engine) schedulePreparedRefill() {
	pool := engine.pool
	if pool == nil {
		return
	}
	pool.mutex.Lock()
	closed := pool.closed
	pool.mutex.Unlock()
	if closed {
		return
	}
	select {
	case pool.requests <- preparedRefillRequest{}:
	case <-pool.context.Done():
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

// PreparedRefillWorkers reports the fixed replenishment worker bound.
func (engine *Engine) PreparedRefillWorkers() uint32 {
	if engine == nil || engine.pool == nil {
		return 0
	}
	return engine.pool.refillWorkers
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

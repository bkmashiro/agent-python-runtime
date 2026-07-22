package wazero

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"time"

	wazerort "github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

const maxPreparedCapacity uint32 = 4

type preparedInstance struct {
	module api.Module
	stderr *bytes.Buffer
}

type preparedPool struct {
	ready   chan *preparedInstance
	context context.Context
	cancel  context.CancelFunc

	mutex  sync.Mutex
	closed bool
	refill sync.WaitGroup
}

func (engine *Engine) initializePreparedPool(capacity uint32) {
	if capacity == 0 {
		return
	}
	poolContext, cancel := context.WithCancel(context.Background())
	pool := &preparedPool{
		ready:   make(chan *preparedInstance, capacity),
		context: poolContext,
		cancel:  cancel,
	}
	engine.pool = pool
	for index := uint32(0); index < capacity; index++ {
		instance, err := engine.prepareSingleUseInstance(poolContext)
		if err != nil {
			continue
		}
		pool.ready <- instance
	}
}

func (engine *Engine) checkoutModule(ctx context.Context) (*preparedInstance, error) {
	if engine.pool != nil {
		started := time.Now()
		select {
		case instance := <-engine.pool.ready:
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
	return engine.newInitializedModule(prepareContext, "pool_prepare_")
}

func (engine *Engine) newInitializedModule(ctx context.Context, prefix string) (*preparedInstance, error) {
	guestStderr := &bytes.Buffer{}
	instantiateStarted := time.Now()
	module, err := engine.runtime.InstantiateModule(
		ctx,
		engine.compiled,
		wazerort.NewModuleConfig().WithName("").WithStderr(guestStderr),
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
	guestStderr.Reset()
	failed = false
	return &preparedInstance{module: module, stderr: guestStderr}, nil
}

func (engine *Engine) schedulePreparedRefill() {
	pool := engine.pool
	if pool == nil {
		return
	}
	pool.mutex.Lock()
	if pool.closed {
		pool.mutex.Unlock()
		return
	}
	pool.refill.Add(1)
	pool.mutex.Unlock()
	go func() {
		defer pool.refill.Done()
		instance, err := engine.prepareSingleUseInstance(pool.context)
		if err != nil {
			return
		}
		pool.mutex.Lock()
		defer pool.mutex.Unlock()
		if pool.closed {
			_ = instance.module.Close(context.Background())
			return
		}
		select {
		case pool.ready <- instance:
		default:
			_ = instance.module.Close(context.Background())
		}
	}()
}

// PreparedReady reports the number of never-served initialized instances ready
// for exclusive checkout. It is backend-specific diagnostic information.
func (engine *Engine) PreparedReady() int {
	if engine == nil || engine.pool == nil {
		return 0
	}
	return len(engine.pool.ready)
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
	pool.refill.Wait()
	for {
		select {
		case instance := <-pool.ready:
			_ = instance.module.Close(context.Background())
		default:
			return
		}
	}
}

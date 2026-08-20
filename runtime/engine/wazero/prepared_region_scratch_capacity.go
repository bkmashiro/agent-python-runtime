package wazero

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/preparedregion"
	"github.com/tetratelabs/wazero/api"
)

var ErrPreparedRegionScratchCapacityConsumed = errors.New("prepared region scratch capacity already consumed or discarded")
var ErrPreparedRegionScratchCapacityUnavailable = errors.New("prepared region scratch capacity unavailable")

type PreparedRegionScratchProvisionEvidence struct {
	ModuleInstantiations uint32 `json:"module_instantiations"`
	InitializeCalls      uint32 `json:"initialize_calls"`
	RuntimeInitCalls     uint32 `json:"runtime_init_calls"`
	ProvisionNanos       uint64 `json:"provision_nanos"`
	PreparedHit          bool   `json:"prepared_hit"`
	COWHit               bool   `json:"cow_hit"`
	NeverServed          bool   `json:"never_served"`
	BrokerAvailable      bool   `json:"broker_available"`
	WorkspaceMounted     bool   `json:"workspace_mounted"`
}

// PreparedRegionScratchCapacity owns one initialized, never-served, private
// Guest. Execute consumes it exactly once; Close discards it without serving.
type PreparedRegionScratchCapacity struct {
	mu sync.Mutex

	engine     *Engine
	module     api.Module
	stderr     *boundedDiagnostic
	stdout     *forbiddenStdout
	releaseCOW func()
	claims     uint32
	rejected   uint32
	consumed   bool
	discarded  bool
	closed     bool
}

type PreparedRegionScratchCapacityEvidence struct {
	Ready          uint32 `json:"ready"`
	Claims         uint32 `json:"claims"`
	Consumed       uint32 `json:"consumed"`
	RejectedClaims uint32 `json:"rejected_claims"`
	Discarded      uint32 `json:"discarded"`
}

func (engine *Engine) PreparePreparedRegionScratch(ctx context.Context) (_ *PreparedRegionScratchCapacity, evidence PreparedRegionScratchProvisionEvidence, provisionErr error) {
	if engine == nil || ctx == nil || !engine.config.Mechanisms.SemanticAnalysis || !engine.config.Mechanisms.PreparedRuntime {
		return nil, evidence, runtimeconfig.ErrMechanismDisabled
	}
	properties := engine.Properties()
	evidence.BrokerAvailable = properties.CapabilityBrokerAvailable
	evidence.WorkspaceMounted = properties.WorkspaceMounted
	if properties.WorkspaceMounted || properties.CapabilityBrokerAvailable {
		return nil, evidence, ErrPreparedRegionScratchAuthority
	}
	if err := engine.acquireSemanticSession(); err != nil {
		return nil, evidence, err
	}
	leased := true
	defer func() {
		if leased {
			engine.releaseSemanticSession()
		}
	}()

	started := time.Now()
	provisioned, err := engine.ensurePreparedWithResult(ctx)
	if err != nil {
		evidence.ProvisionNanos = uint64(time.Since(started))
		return nil, evidence, err
	}
	if !provisioned {
		evidence.ProvisionNanos = uint64(time.Since(started))
		return nil, evidence, ErrPreparedRegionScratchCapacityUnavailable
	}
	evidence.ModuleInstantiations = 1
	evidence.InitializeCalls = 1
	evidence.RuntimeInitCalls = 1

	cowRuntime, releaseCOW, cowSelected, err := engine.acquireCOWRuntime()
	if err != nil {
		evidence.ProvisionNanos = uint64(time.Since(started))
		return nil, evidence, err
	}
	var prepared *preparedInstance
	if cowSelected {
		cowPrepared, lifecycle, prepareErr := cowRuntime.prepare(ctx, engine)
		if prepareErr != nil {
			releaseCOW()
			evidence.ProvisionNanos = uint64(time.Since(started))
			return nil, evidence, fmt.Errorf("prepare private scratch COW slot: %w", prepareErr)
		}
		prepared = cowPrepared
		evidence.COWHit = true
		if lifecycle.ModuleInstantiations > 0 {
			evidence.ModuleInstantiations += lifecycle.ModuleInstantiations
		}
	} else {
		prepared = engine.takePrepared()
		if prepared == nil {
			evidence.ProvisionNanos = uint64(time.Since(started))
			return nil, evidence, ErrPreparedRegionScratchCapacityUnavailable
		}
		evidence.PreparedHit = true
		releaseCOW = nil
	}
	evidence.ProvisionNanos = uint64(time.Since(started))
	evidence.NeverServed = true
	leased = false
	return &PreparedRegionScratchCapacity{
		engine: engine, module: prepared.module, stderr: prepared.stderr, stdout: prepared.stdout, releaseCOW: releaseCOW,
	}, evidence, nil
}

func (capacity *PreparedRegionScratchCapacity) Execute(ctx context.Context, request []byte, decision preparedregion.PreparedRegionDecision) (result preparedregion.PreparedRegionScratchResult, evidence PreparedRegionScratchExecutionEvidence, executionErr error) {
	if capacity == nil || ctx == nil {
		return result, evidence, ErrPreparedRegionScratchCapacityConsumed
	}
	capacity.mu.Lock()
	defer capacity.mu.Unlock()
	if capacity.closed || capacity.consumed || capacity.module == nil {
		capacity.rejected++
		return result, evidence, ErrPreparedRegionScratchCapacityConsumed
	}
	capacity.claims++
	capacity.consumed = true
	evidence.FreshModule = true
	evidence.PreparedCapacity = true
	evidence.BrokerAvailable = false
	evidence.WorkspaceMounted = false
	defer func() {
		started := time.Now()
		executionErr = errors.Join(executionErr, capacity.closeLocked())
		evidence.CloseNanos = uint64(time.Since(started))
	}()
	if len(request) == 0 || uint64(len(request)) > uint64(capacity.engine.config.MaxRequestBytes) {
		return result, evidence, ErrPreparedRegionScratchRequest
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		result, _ = preparedregion.NewPreparedRegionCancelledResult(decision.IdentitySHA256, preparedRegionCancellationType(ctxErr))
		evidence.TerminalStatus = result.Status
		return result, evidence, ctxErr
	}
	executionContext, cancel := context.WithTimeout(ctx, capacity.engine.config.Timeout)
	defer cancel()
	started := time.Now()
	payload, err := callGuestResponse(executionContext, capacity.module, "runtime_execute_prepared_region_scratch", request, capacity.engine.config.MaxResponseBytes)
	evidence.ExecuteNanos = uint64(time.Since(started))
	if err != nil {
		if executionContext.Err() != nil {
			result, _ = preparedregion.NewPreparedRegionCancelledResult(decision.IdentitySHA256, preparedRegionCancellationType(executionContext.Err()))
			evidence.TerminalStatus = result.Status
		}
		return result, evidence, withGuestDiagnostic(err, capacity.stderr.String())
	}
	if capacity.stdout.Used() {
		return result, evidence, ErrGuestStdoutBypass
	}
	result, err = preparedregion.DecodePreparedRegionScratchResult(payload)
	if err != nil || result.DecisionSHA256 != decision.IdentitySHA256 {
		return preparedregion.PreparedRegionScratchResult{}, evidence, preparedregion.ErrInvalidPreparedRegion
	}
	evidence.TerminalStatus = result.Status
	return result, evidence, nil
}

func (capacity *PreparedRegionScratchCapacity) Close(context.Context) error {
	if capacity == nil {
		return nil
	}
	capacity.mu.Lock()
	defer capacity.mu.Unlock()
	return capacity.closeLocked()
}

func (capacity *PreparedRegionScratchCapacity) Evidence() PreparedRegionScratchCapacityEvidence {
	if capacity == nil {
		return PreparedRegionScratchCapacityEvidence{}
	}
	capacity.mu.Lock()
	defer capacity.mu.Unlock()
	evidence := PreparedRegionScratchCapacityEvidence{Claims: capacity.claims, RejectedClaims: capacity.rejected}
	if !capacity.closed && !capacity.consumed && capacity.module != nil {
		evidence.Ready = 1
	}
	if capacity.consumed {
		evidence.Consumed = 1
	}
	if capacity.discarded {
		evidence.Discarded = 1
	}
	return evidence
}

func (capacity *PreparedRegionScratchCapacity) closeLocked() error {
	if capacity.closed {
		return nil
	}
	if !capacity.consumed && capacity.module != nil {
		capacity.discarded = true
	}
	capacity.closed = true
	var closeErr error
	if capacity.module != nil {
		closeErr = capacity.module.Close(context.Background())
		capacity.module = nil
	}
	if capacity.releaseCOW != nil {
		capacity.releaseCOW()
		capacity.releaseCOW = nil
	}
	if capacity.engine != nil {
		capacity.engine.releaseSemanticSession()
		capacity.engine = nil
	}
	return closeErr
}

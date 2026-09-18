package wazero

import (
	"context"
	"errors"
	"fmt"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/preparedregion"
)

var (
	ErrPreparedRegionScratchAuthority = errors.New("prepared region scratch Guest cannot carry workspace or Broker authority")
	ErrPreparedRegionScratchRequest   = errors.New("prepared region scratch request is empty or exceeds the bound")
)

type PreparedRegionScratchExecutionEvidence struct {
	ModuleInstantiations uint32                                     `json:"module_instantiations"`
	InitializeCalls      uint32                                     `json:"initialize_calls"`
	RuntimeInitCalls     uint32                                     `json:"runtime_init_calls"`
	InstantiateNanos     uint64                                     `json:"instantiate_nanos"`
	InitializeNanos      uint64                                     `json:"initialize_nanos"`
	RuntimeInitNanos     uint64                                     `json:"runtime_init_nanos"`
	ExecuteNanos         uint64                                     `json:"execute_nanos"`
	CloseNanos           uint64                                     `json:"close_nanos"`
	FreshModule          bool                                       `json:"fresh_module"`
	PreparedCapacity     bool                                       `json:"prepared_capacity"`
	BrokerAvailable      bool                                       `json:"broker_available"`
	WorkspaceMounted     bool                                       `json:"workspace_mounted"`
	TerminalStatus       preparedregion.PreparedRegionScratchStatus `json:"terminal_status"`
}

func (engine *Engine) ExecutePreparedRegionScratch(ctx context.Context, request []byte, decision preparedregion.PreparedRegionDecision) (result preparedregion.PreparedRegionScratchResult, evidence PreparedRegionScratchExecutionEvidence, executionErr error) {
	if engine == nil || ctx == nil || !engine.config.Mechanisms.SemanticAnalysis {
		return result, evidence, runtimeconfig.ErrMechanismDisabled
	}
	properties := engine.Properties()
	evidence.BrokerAvailable = properties.CapabilityBrokerAvailable
	evidence.WorkspaceMounted = properties.WorkspaceMounted
	if properties.WorkspaceMounted || properties.CapabilityBrokerAvailable {
		return result, evidence, ErrPreparedRegionScratchAuthority
	}
	if len(request) == 0 || uint64(len(request)) > uint64(engine.config.MaxRequestBytes) {
		return result, evidence, ErrPreparedRegionScratchRequest
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		result, _ = preparedregion.NewPreparedRegionCancelledResult(decision.IdentitySHA256, preparedRegionCancellationType(ctxErr))
		evidence.TerminalStatus = result.Status
		return result, evidence, ctxErr
	}
	if err := engine.acquireSemanticSession(); err != nil {
		return result, evidence, err
	}
	defer engine.releaseSemanticSession()

	scratchContext, cancel := context.WithTimeout(ctx, engine.config.Timeout)
	defer cancel()
	stderr := &boundedDiagnostic{}
	stdout := &forbiddenStdout{}
	evidence.FreshModule = true
	prepared, setup, err := engine.newFreshGuest(scratchContext, stderr, stdout, false)
	evidence.ModuleInstantiations = setup.ModuleInstantiations
	evidence.InstantiateNanos = setup.InstantiateNanos
	evidence.InitializeCalls = setup.InitializeCalls
	evidence.InitializeNanos = setup.InitializeNanos
	evidence.RuntimeInitCalls = setup.RuntimeInitCalls
	evidence.RuntimeInitNanos = setup.RuntimeInitNanos
	if prepared == nil {
		return result, evidence, fmt.Errorf("instantiate prepared region scratch Guest: %w", err)
	}
	defer func() {
		started := time.Now()
		executionErr = errors.Join(executionErr, closePreparedInstance(prepared))
		evidence.CloseNanos = uint64(time.Since(started))
	}()
	if err != nil {
		return result, evidence, withGuestDiagnostic(err, stderr.String())
	}
	started := time.Now()
	payload, err := callGuestResponse(scratchContext, prepared.module, "runtime_execute_prepared_region_scratch", request, engine.config.MaxResponseBytes)
	evidence.ExecuteNanos = uint64(time.Since(started))
	if err != nil {
		if scratchContext.Err() != nil {
			result, _ = preparedregion.NewPreparedRegionCancelledResult(decision.IdentitySHA256, preparedRegionCancellationType(scratchContext.Err()))
			evidence.TerminalStatus = result.Status
		}
		return result, evidence, withGuestDiagnostic(err, stderr.String())
	}
	if stdout.Used() {
		return result, evidence, ErrGuestStdoutBypass
	}
	result, err = preparedregion.DecodePreparedRegionScratchResult(payload)
	if err != nil || result.DecisionSHA256 != decision.IdentitySHA256 {
		return preparedregion.PreparedRegionScratchResult{}, evidence, preparedregion.ErrInvalidPreparedRegion
	}
	evidence.TerminalStatus = result.Status
	return result, evidence, nil
}

func preparedRegionCancellationType(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	return "context_canceled"
}

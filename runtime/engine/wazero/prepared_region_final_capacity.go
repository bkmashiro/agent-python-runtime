package wazero

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	"github.com/bkmashiro/agent-python-runtime/runtime/preparedregion"
	"github.com/tetratelabs/wazero/api"
)

var ErrPreparedRegionDerivedCapacityConsumed = errors.New("prepared region derived capacity already consumed or discarded")
var ErrPreparedRegionDerivedCapacityUnavailable = errors.New("prepared region derived capacity unavailable")

type PreparedRegionFinalProvisionEvidence struct {
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

type PreparedRegionDerivedCompileEvidence struct {
	PreparedCapacity     bool   `json:"prepared_capacity"`
	ModuleInstantiations uint32 `json:"module_instantiations"`
	RuntimeInitCalls     uint32 `json:"runtime_init_calls"`
	SourceValidations    uint32 `json:"source_validations"`
	SelectionCompiles    uint32 `json:"selection_compiles"`
	BrokerAvailable      bool   `json:"broker_available"`
	WorkspaceMounted     bool   `json:"workspace_mounted"`
}

type PreparedRegionDerivedExecutionEvidence struct {
	PreparedCapacity      bool   `json:"prepared_capacity"`
	ModuleInstantiations  uint32 `json:"module_instantiations"`
	RuntimeInitCalls      uint32 `json:"runtime_init_calls"`
	FormalGuestExecutions uint32 `json:"formal_guest_executions"`
	BrokerAvailable       bool   `json:"broker_available"`
	WorkspaceMounted      bool   `json:"workspace_mounted"`
}

// PreparedRegionFinalCapacity owns one initialized, never-served final Guest.
// Compile validates unchanged source and installs one sealed target-Guest-owned
// derived program. Execute may then run that exact request once, without fallback.
type PreparedRegionFinalCapacity struct {
	mu sync.Mutex

	engine     *Engine
	module     api.Module
	stderr     *boundedDiagnostic
	stdout     *forbiddenStdout
	releaseCOW func()

	runContext context.Context
	cancel     context.CancelFunc
	request    []byte
	compiled   bool
	consumed   bool
	closed     bool
}

func (engine *Engine) PreparePreparedRegionFinal(ctx context.Context) (_ *PreparedRegionFinalCapacity, evidence PreparedRegionFinalProvisionEvidence, provisionErr error) {
	if engine == nil || ctx == nil || !engine.config.Mechanisms.SemanticAnalysis || !engine.config.Mechanisms.PreparedRuntime {
		return nil, evidence, runtimeconfig.ErrMechanismDisabled
	}
	properties := engine.Properties()
	evidence.BrokerAvailable = properties.CapabilityBrokerAvailable
	evidence.WorkspaceMounted = properties.WorkspaceMounted
	if properties.CapabilityBrokerAvailable || properties.WorkspaceMounted || engine.preparedRegions == nil || engine.config.DeterministicVerification != nil {
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
		return nil, evidence, ErrPreparedRegionDerivedCapacityUnavailable
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
			return nil, evidence, fmt.Errorf("prepare private final COW slot: %w", prepareErr)
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
			return nil, evidence, ErrPreparedRegionDerivedCapacityUnavailable
		}
		evidence.PreparedHit = true
		releaseCOW = nil
	}
	if prepared.temporary != nil {
		_ = prepared.module.Close(context.Background())
		if releaseCOW != nil {
			releaseCOW()
		}
		return nil, evidence, ErrPreparedRegionScratchAuthority
	}
	evidence.ProvisionNanos = uint64(time.Since(started))
	evidence.NeverServed = true
	leased = false
	return &PreparedRegionFinalCapacity{
		engine: engine, module: prepared.module, stderr: prepared.stderr, stdout: prepared.stdout, releaseCOW: releaseCOW,
	}, evidence, nil
}

func (capacity *PreparedRegionFinalCapacity) Compile(ctx context.Context, request []byte, selection preparedregion.PreparedRegionExecutionSelection, decision preparedregion.PreparedRegionDecision, capsule preparedregion.PreparedRegionCapsule, patch preparedregion.PreparedRegionPatch) (evidence PreparedRegionDerivedCompileEvidence, compileErr error) {
	if capacity == nil || ctx == nil {
		return evidence, ErrPreparedRegionDerivedCapacityConsumed
	}
	capacity.mu.Lock()
	defer capacity.mu.Unlock()
	if capacity.closed || capacity.compiled || capacity.consumed || capacity.module == nil {
		return evidence, ErrPreparedRegionDerivedCapacityConsumed
	}
	capacity.compiled = true
	defer func() {
		if compileErr != nil {
			compileErr = errors.Join(compileErr, capacity.closeLocked())
		}
	}()
	evidence.PreparedCapacity = true
	if _, ok := enginecontract.InvocationRefFromContext(ctx); ok {
		return evidence, preparedregion.ErrInvalidPreparedRegion
	}
	if _, ok := enginecontract.ObservationSessionFromContext(ctx); ok {
		return evidence, preparedregion.ErrInvalidPreparedRegion
	}
	if err := validatePreparedRegionDerivedSelection(capacity.engine, request, selection, decision, capsule, patch); err != nil {
		return evidence, err
	}
	selectionRequest, err := preparedRegionSelectionRequest(selection, decision, capsule, patch)
	if err != nil {
		return evidence, err
	}
	runContext, cancel := context.WithTimeout(ctx, capacity.engine.config.Timeout)
	if capacity.engine.preparedRegions != nil {
		runContext = context.WithValue(runContext, preparedRegionContextKey{}, capacity.engine.preparedRegions)
	}
	if err := callSourceValidation(runContext, capacity.module, request); err != nil {
		cancel()
		return evidence, withGuestDiagnostic(err, capacity.stderr.String())
	}
	evidence.SourceValidations = 1
	if err := callStatusWithBytes(runContext, capacity.module, "runtime_select_prepared_region_execution", selectionRequest); err != nil {
		cancel()
		return evidence, withGuestDiagnostic(err, capacity.stderr.String())
	}
	evidence.SelectionCompiles = 1
	if capacity.stdout.Used() {
		cancel()
		return evidence, ErrGuestStdoutBypass
	}
	capacity.runContext = runContext
	capacity.cancel = cancel
	capacity.request = append([]byte(nil), request...)
	return evidence, nil
}

func (capacity *PreparedRegionFinalCapacity) Execute(ctx context.Context, request []byte) (payload []byte, evidence PreparedRegionDerivedExecutionEvidence, executionErr error) {
	if capacity == nil || ctx == nil {
		return nil, evidence, ErrPreparedRegionDerivedCapacityConsumed
	}
	capacity.mu.Lock()
	defer capacity.mu.Unlock()
	if capacity.closed || capacity.consumed || !capacity.compiled || capacity.module == nil {
		return nil, evidence, ErrPreparedRegionDerivedCapacityConsumed
	}
	capacity.consumed = true
	evidence.PreparedCapacity = true
	defer func() { executionErr = errors.Join(executionErr, capacity.closeLocked()) }()
	if !bytes.Equal(request, capacity.request) || ctx.Err() != nil || capacity.runContext.Err() != nil {
		if ctx.Err() != nil {
			return nil, evidence, ctx.Err()
		}
		if capacity.runContext.Err() != nil {
			return nil, evidence, capacity.runContext.Err()
		}
		return nil, evidence, preparedregion.ErrInvalidPreparedRegion
	}
	runRequest, err := runtimeconfig.DecodeRunRequest(request)
	if err != nil {
		return nil, evidence, err
	}
	payload, err = callExecute(capacity.runContext, capacity.module, request, capacity.engine.config.MaxResponseBytes)
	if err != nil {
		return nil, evidence, withGuestDiagnostic(err, capacity.stderr.String())
	}
	evidence.FormalGuestExecutions = 1
	if capacity.stdout.Used() {
		return payload, evidence, ErrGuestStdoutBypass
	}
	if _, err := runtimeconfig.DecodeAndValidateGuestRunResponse(runRequest, payload); err != nil && !errors.Is(err, runtimeconfig.ErrRunResultSchemaMismatch) {
		return payload, evidence, err
	}
	payload, err = projectHostEvidence(payload, nil, 0, "", nil, capacity.engine.config.MaxResponseBytes)
	if err != nil {
		return nil, evidence, err
	}
	if _, err := runtimeconfig.DecodeAndValidateRunResponse(runRequest, payload); err != nil {
		return payload, evidence, err
	}
	return payload, evidence, nil
}

func (capacity *PreparedRegionFinalCapacity) Close(context.Context) error {
	if capacity == nil {
		return nil
	}
	capacity.mu.Lock()
	defer capacity.mu.Unlock()
	return capacity.closeLocked()
}

func (capacity *PreparedRegionFinalCapacity) closeLocked() error {
	if capacity.closed {
		return nil
	}
	capacity.closed = true
	if capacity.cancel != nil {
		capacity.cancel()
		capacity.cancel = nil
	}
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

func validatePreparedRegionDerivedSelection(engine *Engine, request []byte, selection preparedregion.PreparedRegionExecutionSelection, decision preparedregion.PreparedRegionDecision, capsule preparedregion.PreparedRegionCapsule, patch preparedregion.PreparedRegionPatch) error {
	if len(request) == 0 || uint64(len(request)) > uint64(engine.config.MaxRequestBytes) {
		return errors.New("request exceeds configured bounds")
	}
	runRequest, err := runtimeconfig.DecodeRunRequest(request)
	if err != nil {
		return err
	}
	if err := runtimeconfig.AdmitRunRequirements(runRequest); err != nil {
		return err
	}
	if err := runtimeconfig.EvaluateRunCompatibility(runRequest, engine.config.ExecutionProfile); err != nil {
		return err
	}
	if selection.Validate(decision, capsule, patch) != nil || engine.preparedRegions.ValidateReady(decision, capsule) != nil {
		return preparedregion.ErrInvalidPreparedRegion
	}
	sourceDigest := sha256.Sum256([]byte(runRequest.Code))
	if selection.FinalSourceSHA256 != fmt.Sprintf("sha256:%x", sourceDigest[:]) {
		return preparedregion.ErrInvalidPreparedRegion
	}
	profileSHA256, err := runtimeconfig.ExecutionProfileBindingSHA256(engine.config)
	if err != nil || decision.ExecutionProfileSHA256 != profileSHA256 {
		return preparedregion.ErrInvalidPreparedRegion
	}
	return nil
}

func preparedRegionSelectionRequest(selection preparedregion.PreparedRegionExecutionSelection, decision preparedregion.PreparedRegionDecision, capsule preparedregion.PreparedRegionCapsule, patch preparedregion.PreparedRegionPatch) ([]byte, error) {
	decisionRaw, sealedDecision, err := preparedregion.SealPreparedRegionDecision(decision.PreparedRegionBinding)
	if err != nil || sealedDecision != decision {
		return nil, preparedregion.ErrInvalidPreparedRegion
	}
	patchRaw, sealedPatch, err := preparedregion.SealPreparedRegionPatch(patch.PreparedRegionPatchBinding)
	if err != nil || sealedPatch != patch {
		return nil, preparedregion.ErrInvalidPreparedRegion
	}
	selectionRaw, sealedSelection, err := preparedregion.SealPreparedRegionExecutionSelection(decision, capsule, patch)
	if err != nil || sealedSelection != selection {
		return nil, preparedregion.ErrInvalidPreparedRegion
	}
	return json.Marshal(map[string]string{"decision": string(decisionRaw), "patch": string(patchRaw), "selection": string(selectionRaw)})
}

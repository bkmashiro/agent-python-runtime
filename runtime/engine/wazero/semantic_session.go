package wazero

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	"github.com/tetratelabs/wazero/api"
)

var (
	ErrSemanticAnalysisSessionClosed       = errors.New("semantic analysis session closed")
	ErrSemanticAnalysisSessionLimit        = errors.New("semantic analysis session limit exceeded")
	ErrSemanticAnalysisSessionAuthority    = errors.New("semantic analysis session cannot carry workspace or Broker authority")
	ErrSemanticAnalysisEngineClosing       = errors.New("semantic analysis engine is closing")
	ErrSemanticAnalysisSessionsActive      = errors.New("semantic analysis engine still has active sessions")
	ErrSemanticAnalysisCapacityReady       = errors.New("semantic analysis capacity already prepared or served")
	ErrSemanticAnalysisCapacityUnavailable = errors.New("semantic analysis prepared capacity unavailable")
)

// SemanticAnalysisSessionLimits bound all mutable analyzer state to one source-generation Run.
type SemanticAnalysisSessionLimits struct {
	MaxRequests               uint32
	MaxCumulativeRequestBytes uint64
	MaxDuration               time.Duration
}

type SemanticAnalysisSessionProvisionEvidence struct {
	ModuleInstantiations uint32 `json:"module_instantiations"`
	InitializeCalls      uint32 `json:"initialize_calls"`
	RuntimeInitCalls     uint32 `json:"runtime_init_calls"`
	PreparedHit          bool   `json:"prepared_hit"`
	COWHit               bool   `json:"cow_hit"`
	NeverServed          bool   `json:"never_served"`
	BrokerAvailable      bool   `json:"broker_available"`
	WorkspaceMounted     bool   `json:"workspace_mounted"`
}

func (limits SemanticAnalysisSessionLimits) validate(engine *Engine) error {
	if engine == nil || limits.MaxRequests == 0 || limits.MaxRequests > 1024 ||
		limits.MaxCumulativeRequestBytes == 0 || limits.MaxDuration <= 0 || limits.MaxDuration > engine.config.Timeout {
		return ErrSemanticAnalysisSessionLimit
	}
	maximumBytes := uint64(engine.config.MaxRequestBytes) * uint64(limits.MaxRequests)
	if limits.MaxCumulativeRequestBytes > maximumBytes {
		return ErrSemanticAnalysisSessionLimit
	}
	return nil
}

// SemanticAnalysisSession owns at most one private analyzer Guest. It initializes
// lazily on the first exact request, serializes all calls, and is terminal after
// close, cancellation, a bound violation, or any Guest failure.
type SemanticAnalysisSession struct {
	engine *Engine
	limits SemanticAnalysisSessionLimits

	mu              sync.Mutex
	context         context.Context
	cancel          context.CancelFunc
	stopContext     func() bool
	started         time.Time
	requests        uint32
	cumulativeBytes uint64
	module          api.Module
	prepared        *preparedInstance
	releaseCOW      func()
	engineLease     bool
	stderr          *boundedDiagnostic
	stdout          *forbiddenStdout
	lifecycle       SemanticAnalysisLifecycleEvidence
	closed          bool
	recorded        bool
}

var _ enginecontract.Runner = (*SemanticAnalysisSession)(nil)
var _ enginecontract.SemanticAnalyzer = (*SemanticAnalysisSession)(nil)

func (engine *Engine) NewSemanticAnalysisSession(ctx context.Context, limits SemanticAnalysisSessionLimits) (*SemanticAnalysisSession, error) {
	if engine == nil || ctx == nil || !engine.config.Mechanisms.SemanticAnalysis {
		return nil, runtimeconfig.ErrMechanismDisabled
	}
	properties := engine.Properties()
	if properties.WorkspaceMounted || properties.CapabilityBrokerAvailable {
		return nil, ErrSemanticAnalysisSessionAuthority
	}
	if err := limits.validate(engine); err != nil {
		return nil, err
	}
	if err := engine.acquireSemanticSession(); err != nil {
		return nil, err
	}
	sessionContext, cancel := context.WithCancel(ctx)
	session := &SemanticAnalysisSession{
		engine: engine, limits: limits, context: sessionContext, cancel: cancel, started: time.Now(),
		stderr: &boundedDiagnostic{}, stdout: &forbiddenStdout{}, engineLease: true,
	}
	session.stopContext = context.AfterFunc(sessionContext, func() { _ = session.Close(context.Background()) })
	return session, nil
}

func (session *SemanticAnalysisSession) Properties() enginecontract.Properties {
	if session == nil || session.engine == nil {
		return enginecontract.Properties{}
	}
	return session.engine.Properties()
}

// Run is deliberately unavailable: an analyzer session cannot execute Agent source.
func (session *SemanticAnalysisSession) Run(context.Context, []byte, string) ([]byte, error) {
	return nil, runtimeconfig.ErrMechanismDisabled
}

// Prepare initializes this session's private capacity without issuing an
// analyzer request. Explicit benchmark capacities fail closed rather than
// falling back to a request-time fresh Guest.
func (session *SemanticAnalysisSession) Prepare(ctx context.Context) (evidence SemanticAnalysisSessionProvisionEvidence, prepareErr error) {
	if session == nil || ctx == nil || session.engine == nil || !session.engine.config.Mechanisms.PreparedRuntime {
		return evidence, runtimeconfig.ErrMechanismDisabled
	}
	properties := session.engine.Properties()
	evidence.BrokerAvailable = properties.CapabilityBrokerAvailable
	evidence.WorkspaceMounted = properties.WorkspaceMounted
	if properties.WorkspaceMounted || properties.CapabilityBrokerAvailable {
		return evidence, ErrSemanticAnalysisSessionAuthority
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed || session.context.Err() != nil {
		return evidence, ErrSemanticAnalysisSessionClosed
	}
	if session.module != nil || session.requests != 0 {
		return evidence, ErrSemanticAnalysisCapacityReady
	}
	if err := session.ensureModuleLocked(ctx); err != nil {
		session.lifecycle.Failures++
		_ = session.closeLocked()
		return evidence, err
	}
	evidence.ModuleInstantiations = session.lifecycle.ModuleInstantiations
	evidence.InitializeCalls = session.lifecycle.InitializeCalls
	evidence.RuntimeInitCalls = session.lifecycle.RuntimeInitCalls
	evidence.PreparedHit = session.lifecycle.PreparedHits == 1
	evidence.COWHit = session.lifecycle.COWHits == 1
	evidence.NeverServed = session.requests == 0 && session.lifecycle.Invocations == 0
	if session.lifecycle.FreshFallbacks != 0 || !evidence.NeverServed || (!evidence.PreparedHit && !evidence.COWHit) {
		_ = session.closeLocked()
		return evidence, ErrSemanticAnalysisCapacityUnavailable
	}
	return evidence, nil
}

func (session *SemanticAnalysisSession) AnalyzeSemantic(ctx context.Context, request []byte) (payload []byte, analysisErr error) {
	return session.callQualificationGuest(ctx, request, "runtime_analyze_source")
}

// TransformSourcePass runs a registered source transform inside the same
// authority-free exact Guest used for semantic analysis.
func (session *SemanticAnalysisSession) TransformSourcePass(ctx context.Context, request []byte) ([]byte, error) {
	return session.callQualificationGuest(ctx, request, "runtime_transform_source_pass")
}

// EmitPreparedRegionPatch asks the same private authority-free target Guest to
// validate one source-bound decision and emit only the derived-AST identity
// binding. It cannot execute Agent source or access Broker/workspace state.
func (session *SemanticAnalysisSession) EmitPreparedRegionPatch(ctx context.Context, request []byte) ([]byte, error) {
	return session.callQualificationGuest(ctx, request, "runtime_emit_prepared_region_patch")
}

func (session *SemanticAnalysisSession) callQualificationGuest(ctx context.Context, request []byte, export string) (payload []byte, analysisErr error) {
	if session == nil || ctx == nil {
		return nil, ErrSemanticAnalysisSessionClosed
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed || session.context.Err() != nil {
		return nil, ErrSemanticAnalysisSessionClosed
	}
	if session.requests >= session.limits.MaxRequests || len(request) == 0 ||
		uint64(len(request)) > uint64(session.engine.config.MaxRequestBytes) ||
		session.cumulativeBytes+uint64(len(request)) > session.limits.MaxCumulativeRequestBytes ||
		time.Since(session.started) >= session.limits.MaxDuration {
		session.lifecycle.Invocations++
		session.lifecycle.Failures++
		_ = session.closeLocked()
		return nil, ErrSemanticAnalysisSessionLimit
	}
	session.requests++
	session.cumulativeBytes += uint64(len(request))
	session.lifecycle.Invocations++

	remaining := session.limits.MaxDuration - time.Since(session.started)
	callContext, cancel := context.WithTimeout(ctx, remaining)
	stop := context.AfterFunc(session.context, cancel)
	defer func() {
		stop()
		cancel()
	}()
	if err := session.ensureModuleLocked(callContext); err != nil {
		session.lifecycle.Failures++
		_ = session.closeLocked()
		return nil, err
	}
	started := time.Now()
	payload, err := callGuestResponse(callContext, session.module, export, request, session.engine.config.MaxResponseBytes)
	session.lifecycle.AnalyzeNanos += uint64(time.Since(started))
	if err != nil {
		session.lifecycle.Failures++
		wrapped := withGuestDiagnostic(err, session.stderr.String())
		_ = session.closeLocked()
		return nil, wrapped
	}
	if session.stdout.Used() {
		session.lifecycle.Failures++
		_ = session.closeLocked()
		return payload, ErrGuestStdoutBypass
	}
	session.lifecycle.Successes++
	return payload, nil
}

func (session *SemanticAnalysisSession) ensureModuleLocked(ctx context.Context) error {
	if session.module != nil {
		return nil
	}
	if session.engine.config.Mechanisms.PreparedRuntime {
		started := time.Now()
		provisioned, err := session.engine.ensurePreparedWithResult(ctx)
		provisionNanos := uint64(time.Since(started))
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, errCOWEngineClosing) {
				return err
			}
			if provisioned {
				session.lifecycle.PreparedProvisionFailures++
				session.lifecycle.PreparedProvisionNanos += provisionNanos
			}
			session.recordFreshFallback(true)
			return session.ensureFreshModuleLocked(ctx)
		}
		if provisioned {
			session.lifecycle.PreparedProvisions++
			session.lifecycle.PreparedProvisionNanos += provisionNanos
			session.lifecycle.ModuleInstantiations++
			session.lifecycle.InitializeCalls++
			session.lifecycle.RuntimeInitCalls++
		}
		cowRuntime, release, cowSelected, err := session.engine.acquireCOWRuntime()
		if err != nil {
			return err
		}
		if cowSelected {
			cloneStarted := time.Now()
			prepared, cloneLifecycle, prepareErr := cowRuntime.prepare(ctx, session.engine)
			session.lifecycle.COWCloneNanos += uint64(time.Since(cloneStarted))
			session.lifecycle.ModuleInstantiations += cloneLifecycle.ModuleInstantiations
			session.lifecycle.InitializeCalls += cloneLifecycle.InitializeCalls
			if prepareErr != nil {
				release()
				if ctx.Err() != nil {
					return fmt.Errorf("prepare semantic analyzer COW slot: %w", prepareErr)
				}
				session.recordFreshFallback(true)
				return session.ensureFreshModuleLocked(ctx)
			}
			session.prepared = prepared
			session.releaseCOW = release
			session.lifecycle.COWHits++
			session.engine.preparedMu.Lock()
			session.engine.preparedState.PreparedRuns++
			session.engine.preparedMu.Unlock()
		} else {
			session.prepared = session.engine.takePrepared()
			if session.prepared != nil {
				session.lifecycle.PreparedHits++
			} else {
				session.recordFreshFallback(false)
			}
		}
		if session.prepared != nil {
			session.module = session.prepared.module
			session.stderr = session.prepared.stderr
			session.stdout = session.prepared.stdout
			return nil
		}
	}
	return session.ensureFreshModuleLocked(ctx)
}

func (session *SemanticAnalysisSession) recordFreshFallback(updatePreparedState bool) {
	session.lifecycle.FreshFallbacks++
	if updatePreparedState {
		session.engine.preparedMu.Lock()
		session.engine.preparedState.FreshFallbackRuns++
		session.engine.preparedMu.Unlock()
	}
}

func (session *SemanticAnalysisSession) ensureFreshModuleLocked(ctx context.Context) error {
	started := time.Now()
	session.lifecycle.ModuleInstantiations++
	module, err := session.engine.runtime.InstantiateModule(ctx, session.engine.compiled, session.engine.baseModuleConfig(session.stderr, session.stdout))
	session.lifecycle.InstantiateNanos += uint64(time.Since(started))
	if err != nil {
		return fmt.Errorf("instantiate semantic analyzer session Guest: %w", err)
	}
	session.module = module
	started = time.Now()
	session.lifecycle.InitializeCalls++
	if err := callNoArgs(ctx, module, "_initialize"); err != nil {
		session.lifecycle.InitializeNanos += uint64(time.Since(started))
		return withGuestDiagnostic(err, session.stderr.String())
	}
	session.lifecycle.InitializeNanos += uint64(time.Since(started))
	started = time.Now()
	session.lifecycle.RuntimeInitCalls++
	if err := callStatusWithBytes(ctx, module, "runtime_init", []byte("{}")); err != nil {
		session.lifecycle.RuntimeInitNanos += uint64(time.Since(started))
		return withGuestDiagnostic(err, session.stderr.String())
	}
	session.lifecycle.RuntimeInitNanos += uint64(time.Since(started))
	return nil
}

func (session *SemanticAnalysisSession) Close(context.Context) error {
	if session == nil {
		return nil
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.closeLocked()
}

func (session *SemanticAnalysisSession) closeLocked() error {
	if session.closed {
		return nil
	}
	session.closed = true
	if session.stopContext != nil {
		session.stopContext()
	}
	session.cancel()
	started := time.Now()
	var err error
	if session.module != nil {
		err = session.module.Close(context.Background())
		session.module = nil
	}
	if session.prepared != nil && session.prepared.temporary != nil {
		err = errors.Join(err, session.prepared.temporary.Close())
	}
	session.prepared = nil
	if session.releaseCOW != nil {
		session.releaseCOW()
		session.releaseCOW = nil
	}
	session.stderr.mu.Lock()
	session.stderr.buffer = nil
	session.stderr.truncated = false
	session.stderr.mu.Unlock()
	session.lifecycle.CloseNanos += uint64(time.Since(started))
	if !session.recorded {
		session.engine.semanticLifecycle.add(session.lifecycle)
		session.recorded = true
	}
	if session.engineLease {
		session.engine.releaseSemanticSession()
		session.engineLease = false
	}
	return err
}

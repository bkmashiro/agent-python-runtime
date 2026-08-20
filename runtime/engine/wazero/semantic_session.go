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
	ErrSemanticAnalysisSessionClosed    = errors.New("semantic analysis session closed")
	ErrSemanticAnalysisSessionLimit     = errors.New("semantic analysis session limit exceeded")
	ErrSemanticAnalysisSessionAuthority = errors.New("semantic analysis session cannot carry workspace or Broker authority")
)

// SemanticAnalysisSessionLimits bound all mutable analyzer state to one source-generation Run.
type SemanticAnalysisSessionLimits struct {
	MaxRequests               uint32
	MaxCumulativeRequestBytes uint64
	MaxDuration               time.Duration
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
	sessionContext, cancel := context.WithCancel(ctx)
	session := &SemanticAnalysisSession{
		engine: engine, limits: limits, context: sessionContext, cancel: cancel, started: time.Now(),
		stderr: &boundedDiagnostic{}, stdout: &forbiddenStdout{},
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

func (session *SemanticAnalysisSession) AnalyzeSemantic(ctx context.Context, request []byte) (payload []byte, analysisErr error) {
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
	payload, err := callGuestResponse(callContext, session.module, "runtime_analyze_source", request, session.engine.config.MaxResponseBytes)
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
	session.stderr.mu.Lock()
	session.stderr.buffer = nil
	session.stderr.truncated = false
	session.stderr.mu.Unlock()
	session.lifecycle.CloseNanos += uint64(time.Since(started))
	if !session.recorded {
		session.engine.semanticLifecycle.add(session.lifecycle)
		session.recorded = true
	}
	return err
}

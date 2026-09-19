package durable

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	wazerort "github.com/tetratelabs/wazero"
)

// RecoveryMode is an explicit Host declaration for an unresolved external call.
type RecoveryMode string

const (
	RetrySafe  RecoveryMode = "retry_safe"
	Idempotent RecoveryMode = "idempotent"
	Lookup     RecoveryMode = "lookup"
	Manual     RecoveryMode = "manual"
	WaitMode   RecoveryMode = "wait"
)

var (
	ErrInvalidRunner = errors.New("invalid durable runner")
	ErrParked        = errors.New("durable attempt parked")
	ErrBlocked       = errors.New("durable run blocked")
	ErrCancelled     = errors.New("durable run cancelled")
)

// ParkError is an internal attempt stop. Broker treats it as a control error;
// the wazero Host import closes the current module instead of returning a
// catchable Python exception.
type ParkError struct {
	RunID    string
	WaitID   string
	Sequence uint32
	Reason   string
}

func (err *ParkError) Error() string {
	if err == nil {
		return ErrParked.Error()
	}
	return fmt.Sprintf("%s: run=%s sequence=%d wait=%s reason=%s", ErrParked, err.RunID, err.Sequence, err.WaitID, err.Reason)
}
func (err *ParkError) Unwrap() error { return ErrParked }

// OperationKey returns the stable operation key supplied to a live Handler.
func OperationKey(ctx context.Context) (string, bool) {
	key, ok := ctx.Value(operationKeyContextKey{}).(string)
	return key, ok && key != ""
}

type operationKeyContextKey struct{}
type replayResultContextKey struct{}

type replayResult struct {
	result json.RawMessage
	err    error
}

// LookupState is the trusted resolver result for a previously pending call.
type LookupState string

const (
	LookupDone           LookupState = "done"
	LookupPending        LookupState = "pending"
	LookupSafeToDispatch LookupState = "safe_to_dispatch"
	LookupUnknown        LookupState = "unknown"
)

type LookupResult struct {
	State  LookupState
	Result json.RawMessage
	Error  string
}

type ResolveFunc func(context.Context, capability.LoggedCall) (LookupResult, error)
type WaitFunc func(context.Context, capability.LoggedCall) (WaitSpec, error)

// Tool couples the existing capability registration with an explicit durable
// recovery declaration. Recovery is never inferred from effect or tool name.
type Tool struct {
	Spec     capability.Spec
	Grant    capability.Grant
	Handler  capability.Handler
	Recovery RecoveryMode
	Resolve  ResolveFunc
	Wait     WaitFunc
}

type Runner struct {
	store              *Store
	artifact           []byte
	artifactSHA256     string
	artifactIdentity   runtimeconfig.VerifiedArtifactIdentity
	executionProfile   runtimeconfig.ExecutionProfile
	environmentVersion string
	plan               *capability.Plan
	tools              map[string]Tool
	toolSnapshot       json.RawMessage

	mu               sync.Mutex
	active           map[string]context.CancelFunc
	compilationCache wazerort.CompilationCache
	closed           bool
}

// NewRunner validates the artifact identity and freezes the capability plan.
// Each Resume constructs a fresh wazero Engine; no Guest memory is persisted.
func NewRunner(store *Store, artifact []byte, identity runtimeconfig.VerifiedArtifactIdentity, environmentVersion string, tools []Tool) (*Runner, error) {
	if store == nil || len(artifact) == 0 || environmentVersion == "" || identity.ArtifactSHA256 == "" {
		return nil, ErrInvalidRunner
	}
	digest := sha256.Sum256(artifact)
	artifactSHA256 := fmt.Sprintf("sha256:%x", digest[:])
	if artifactSHA256 != identity.ArtifactSHA256 {
		return nil, fmt.Errorf("%w: artifact identity mismatch", ErrInvalidRunner)
	}
	profile, err := runtimeconfig.NewExecutionProfile(identity.ProfileID, identity.QualifiedImportRoots)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid execution environment: %w", ErrInvalidRunner, err)
	}
	profile, err = profile.BindVerifiedArtifact(identity)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid execution environment: %w", ErrInvalidRunner, err)
	}
	registry := capability.NewRegistry()
	toolMap := make(map[string]Tool, len(tools))
	for _, tool := range tools {
		if tool.Recovery != RetrySafe && tool.Recovery != Idempotent && tool.Recovery != Lookup && tool.Recovery != Manual && tool.Recovery != WaitMode {
			return nil, fmt.Errorf("%w: tool %q has no recovery declaration", ErrInvalidRunner, tool.Spec.Name)
		}
		if (tool.Handler == nil && tool.Recovery != WaitMode) || tool.Spec.Name == "" || toolMap[tool.Spec.Name].Spec.Name != "" {
			return nil, ErrInvalidRunner
		}
		if tool.Recovery == Lookup && tool.Resolve == nil {
			return nil, fmt.Errorf("%w: lookup tool %q has no resolver", ErrInvalidRunner, tool.Spec.Name)
		}
		if tool.Recovery == WaitMode && tool.Wait == nil {
			return nil, fmt.Errorf("%w: wait tool %q has no wait declaration", ErrInvalidRunner, tool.Spec.Name)
		}
		wrapped := tool
		wrapped.Handler = replayHandler{next: tool.Handler}
		if err := registry.Register(tool.Spec, tool.Grant, wrapped.Handler); err != nil {
			return nil, fmt.Errorf("register durable tool %q: %w", tool.Spec.Name, err)
		}
		toolMap[tool.Spec.Name] = tool
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 1 << 20})
	if err != nil {
		return nil, fmt.Errorf("seal durable capability plan: %w", err)
	}
	toolSnapshot, err := encodeToolSnapshot(plan, toolMap)
	if err != nil {
		return nil, fmt.Errorf("encode durable tool declaration: %w", err)
	}
	return &Runner{
		store: store, artifact: append([]byte(nil), artifact...), artifactSHA256: artifactSHA256,
		artifactIdentity: identity, executionProfile: profile, environmentVersion: environmentVersion,
		plan: plan, tools: toolMap, toolSnapshot: toolSnapshot, active: make(map[string]context.CancelFunc),
		compilationCache: wazerort.NewCompilationCache(),
	}, nil
}

type toolDeclaration struct {
	Spec     capability.Spec `json:"spec"`
	Grant    string          `json:"grant"`
	Recovery RecoveryMode    `json:"recovery"`
}

func encodeToolSnapshot(plan *capability.Plan, tools map[string]Tool) (json.RawMessage, error) {
	if plan == nil {
		return nil, ErrInvalidRunner
	}
	specs := plan.Specs()
	grants := plan.Grants()
	grantByName := make(map[string]string, len(grants))
	for _, grant := range grants {
		grantByName[grant.Capability] = grant.PolicySHA256
	}
	declarations := make([]toolDeclaration, 0, len(specs))
	for _, spec := range specs {
		tool, ok := tools[spec.Name]
		if !ok {
			return nil, fmt.Errorf("%w: missing tool %q", ErrInvalidRunner, spec.Name)
		}
		grant, ok := grantByName[spec.Name]
		if !ok || grant == "" {
			return nil, fmt.Errorf("%w: missing grant for tool %q", ErrInvalidRunner, spec.Name)
		}
		declarations = append(declarations, toolDeclaration{Spec: spec, Grant: grant, Recovery: tool.Recovery})
	}
	sort.Slice(declarations, func(left, right int) bool {
		return declarations[left].Spec.Name < declarations[right].Spec.Name
	})
	encoded, err := json.Marshal(declarations)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(encoded), nil
}

type replayHandler struct{ next capability.Handler }

func (handler replayHandler) Call(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	if replay, ok := ctx.Value(replayResultContextKey{}).(replayResult); ok {
		return append(json.RawMessage(nil), replay.result...), replay.err
	}
	if handler.next == nil {
		return nil, errors.New("wait decision handler requires a persisted decision")
	}
	return handler.next.Call(ctx, args)
}

// Create persists a new pinned run definition. It does not execute Guest code.
func (runner *Runner) Create(ctx context.Context, definition Definition) (Run, error) {
	if runner == nil || runner.store == nil || len(runner.toolSnapshot) == 0 {
		return Run{}, ErrInvalidRunner
	}
	if len(definition.Tools) != 0 && !bytes.Equal(definition.Tools, runner.toolSnapshot) {
		return Run{}, fmt.Errorf("%w: tool declaration snapshot does not match runner", ErrInvalidRunner)
	}
	definition.Tools = append(json.RawMessage(nil), runner.toolSnapshot...)
	if err := runner.validateDefinition(definition); err != nil {
		return Run{}, err
	}
	if err := runner.store.Create(ctx, definition); err != nil {
		return Run{}, err
	}
	return runner.store.Get(ctx, definition.ID)
}

func (runner *Runner) validateDefinition(definition Definition) error {
	if runner == nil || len(runner.toolSnapshot) == 0 || definition.ID == "" || definition.Code == "" || definition.EnvironmentVersion != runner.environmentVersion || definition.ArtifactSHA256 != runner.artifactSHA256 {
		return fmt.Errorf("%w: pinned definition does not match runner", ErrInvalidRunner)
	}
	if len(definition.Inputs) == 0 || !json.Valid(definition.Inputs) {
		return fmt.Errorf("%w: invalid inputs", ErrInvalidRunner)
	}
	if len(definition.Tools) == 0 || !bytes.Equal(definition.Tools, runner.toolSnapshot) {
		return fmt.Errorf("%w: tool declaration snapshot does not match runner", ErrInvalidRunner)
	}
	return nil
}

// Resume claims one run, executes it against a fresh deterministic Guest, and
// releases the owner on every return. Completed responses and errors are
// committed by the Broker journal before Guest delivery.
func (runner *Runner) Resume(ctx context.Context, runID string) (json.RawMessage, error) {
	if runner == nil || runner.store == nil || runID == "" {
		return nil, ErrInvalidRunner
	}
	release, err := runner.store.Claim(runID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = release() }()
	run, err := runner.store.Get(ctx, runID)
	if err != nil {
		return nil, err
	}
	if run.Status == StatusBlocked {
		return nil, fmt.Errorf("%w: %s", ErrBlocked, run.Reason)
	}
	if definitionErr := runner.validateDefinition(run.Definition); definitionErr != nil {
		if blockErr := runner.store.SetState(context.Background(), runID, StatusBlocked, nil, definitionErr.Error()); blockErr != nil && !errors.Is(blockErr, ErrConflict) {
			return nil, errors.Join(ErrBlocked, blockErr)
		}
		return nil, errors.Join(ErrBlocked, definitionErr)
	}
	if run.Status == StatusCancelled {
		return nil, ErrCancelled
	}
	if run.Status == StatusCompleted || run.Status == StatusFailed {
		return append(json.RawMessage(nil), run.Outcome...), nil
	}

	attemptCtx, cancel := context.WithCancel(ctx)
	runner.mu.Lock()
	if runner.closed {
		runner.mu.Unlock()
		cancel()
		return nil, ErrInvalidRunner
	}
	runner.active[runID] = cancel
	runner.mu.Unlock()
	defer func() {
		cancel()
		runner.mu.Lock()
		delete(runner.active, runID)
		runner.mu.Unlock()
	}()

	request, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{RunID: runID, Code: run.Definition.Code, Inputs: append(json.RawMessage(nil), run.Definition.Inputs...)})
	if err != nil {
		return nil, err
	}
	config, err := runner.runConfig(run.Definition)
	if err != nil {
		return nil, err
	}

	journal := &journal{runner: runner, runID: runID}
	var broker *capability.Broker
	factory := wazeroengine.Factory{BrokerFactory: func(brokerCtx context.Context) (*capability.Broker, error) {
		created, createErr := capability.NewBroker(capability.Config{RunIdentity: runID, Plan: runner.plan, CallJournal: journal})
		if createErr == nil {
			broker = created
		}
		return created, createErr
	}, CompilationCache: runner.compilationCache}
	guest, err := factory.New(attemptCtx, runner.artifact, config)
	if err != nil {
		return nil, err
	}
	payload, runErr := guest.Run(attemptCtx, request, runner.plan.PythonPrelude())
	closeErr := guest.Close(context.Background())
	if runErr == nil {
		runErr = closeErr
	} else if closeErr != nil {
		runErr = errors.Join(runErr, closeErr)
	}
	if errors.Is(runErr, ErrParked) {
		return nil, runErr
	}
	if errors.Is(runErr, ErrHistoryMismatch) || errors.Is(runErr, ErrBlocked) {
		return nil, runner.blockAttempt(attemptCtx, runID, runErr.Error())
	}
	if runErr != nil {
		return nil, runErr
	}
	if attemptCtx.Err() != nil {
		return nil, attemptCtx.Err()
	}
	if broker == nil {
		return nil, runner.blockAttempt(attemptCtx, runID, "broker was not created")
	}
	count, countErr := runner.store.CallCount(attemptCtx, runID)
	if countErr != nil {
		return nil, countErr
	}
	if count != broker.CallCount() {
		return nil, runner.blockAttempt(attemptCtx, runID, "call history was not fully consumed")
	}
	if reason, businessError := pythonBusinessError(payload); businessError {
		if err := runner.store.SetState(context.Background(), runID, StatusFailed, payload, reason); err != nil {
			return nil, err
		}
		return append(json.RawMessage(nil), payload...), nil
	}
	if err := runner.store.SetState(attemptCtx, runID, StatusCompleted, payload, ""); err != nil {
		return nil, err
	}
	return append(json.RawMessage(nil), payload...), nil
}

func (runner *Runner) runConfig(definition Definition) (runtimeconfig.RunConfig, error) {
	config := runtimeconfig.DefaultRunConfig()
	deterministic, err := runtimeconfig.NewDeterministicVerificationProfile(runner.artifactSHA256, definition.Seed)
	if err != nil {
		return runtimeconfig.RunConfig{}, err
	}
	profile := runner.executionProfile
	config.ExecutionProfile = &profile
	config.DeterministicVerification = &deterministic
	// Durable v1 is deliberately fresh: no workspace, optimization/prefix,
	// COW, PLM, or cold-I/O mechanisms are enabled here.
	config.Mechanisms = runtimeconfig.MechanismSet{}
	return config, nil
}

func pythonBusinessError(payload []byte) (string, bool) {
	var response struct {
		Status string `json:"status"`
		Error  *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(payload, &response) != nil || response.Status != "error" || response.Error == nil {
		return "", false
	}
	reason := response.Error.Code
	if response.Error.Message != "" {
		reason += ": " + response.Error.Message
	}
	return reason, reason != ""
}

func (runner *Runner) blockAttempt(ctx context.Context, runID, reason string) error {
	if err := runner.store.SetState(context.Background(), runID, StatusBlocked, nil, reason); err != nil {
		return errors.Join(ErrBlocked, err)
	}
	return fmt.Errorf("%w: %s", ErrBlocked, reason)
}

// Decide durably records a wait decision. Resume is intentionally explicit.
func (runner *Runner) Decide(ctx context.Context, waitID string, decision Decision) error {
	if runner == nil || waitID == "" {
		return ErrInvalidRunner
	}
	return runner.store.ResolveWait(ctx, waitID, decision)
}

// Cancel persists a terminal state and promptly notifies attempts owned by this
// Runner by cancelling their contexts. Other owners observe it on their next
// store operation or result submission. An already-dispatched operation may
// finish and be recorded late; Cancel does not roll back external effects.
func (runner *Runner) Cancel(ctx context.Context, runID string) error {
	if runner == nil || runID == "" {
		return ErrInvalidRunner
	}
	runner.mu.Lock()
	cancel := runner.active[runID]
	runner.mu.Unlock()
	if err := runner.store.SetState(ctx, runID, StatusCancelled, nil, "cancelled"); err != nil {
		return err
	}
	if cancel != nil {
		cancel()
	}
	return nil
}

// Close refuses to close runner-owned state while an attempt is using it. The
// runner has no shared global cache and never closes caller-owned state.
func (runner *Runner) Close(ctx context.Context) error {
	if runner == nil {
		return nil
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.active) != 0 {
		return ErrBusy
	}
	if runner.closed {
		return nil
	}
	runner.closed = true
	if runner.compilationCache == nil {
		return nil
	}
	return runner.compilationCache.Close(ctx)
}

type journal struct {
	runner *Runner
	runID  string
}

func (journal *journal) Call(ctx context.Context, logged capability.LoggedCall, dispatch func(context.Context) ([]byte, error)) ([]byte, error) {
	record, created, err := journal.runner.store.BeginCall(ctx, journal.runID, logged)
	if err != nil {
		return nil, err
	}
	if record.State == "completed" {
		return append(json.RawMessage(nil), record.Outcome...), nil
	}

	operationCtx := context.WithValue(ctx, operationKeyContextKey{}, record.OperationKey)
	tool, ok := journal.runner.tools[logged.Capability]
	if !ok {
		return journal.dispatchAndComplete(operationCtx, logged, dispatch)
	}
	if created {
		if tool.Recovery == WaitMode {
			return journal.wait(operationCtx, logged, tool, dispatch)
		}
		return journal.dispatchAndComplete(operationCtx, logged, dispatch)
	}

	switch tool.Recovery {
	case RetrySafe, Idempotent:
		return journal.dispatchAndComplete(operationCtx, logged, dispatch)
	case Lookup:
		lookup, resolveErr := tool.Resolve(operationCtx, logged)
		if resolveErr != nil {
			return nil, resolveErr
		}
		switch lookup.State {
		case LookupSafeToDispatch:
			return journal.dispatchAndComplete(operationCtx, logged, dispatch)
		case LookupDone:
			if (len(lookup.Result) == 0 && lookup.Error == "") || (len(lookup.Result) != 0 && !json.Valid(lookup.Result)) {
				return nil, fmt.Errorf("%w: lookup returned invalid result", ErrBlocked)
			}
			return journal.replayAndComplete(operationCtx, logged, dispatch, lookup.Result, lookup.Error)
		case LookupPending:
			return nil, &ParkError{RunID: journal.runID, Sequence: logged.Sequence, Reason: "lookup pending"}
		default:
			return nil, fmt.Errorf("%w: lookup result is unknown", ErrBlocked)
		}
	case Manual:
		return nil, fmt.Errorf("%w: manual recovery required", ErrBlocked)
	case WaitMode:
		return journal.wait(operationCtx, logged, tool, dispatch)
	default:
		return nil, ErrInvalidRunner
	}
}

func (journal *journal) dispatchAndComplete(ctx context.Context, logged capability.LoggedCall, dispatch func(context.Context) ([]byte, error)) ([]byte, error) {
	response, err := dispatch(ctx)
	if err != nil {
		return nil, err
	}
	return journal.complete(ctx, logged, response)
}

func (journal *journal) replayAndComplete(ctx context.Context, logged capability.LoggedCall, dispatch func(context.Context) ([]byte, error), result json.RawMessage, errorText string) ([]byte, error) {
	replay := replayResult{result: append(json.RawMessage(nil), result...)}
	if errorText != "" {
		replay.err = errors.New(errorText)
	}
	ctx = context.WithValue(ctx, replayResultContextKey{}, replay)
	return journal.dispatchAndComplete(ctx, logged, dispatch)
}

func (journal *journal) wait(ctx context.Context, logged capability.LoggedCall, tool Tool, dispatch func(context.Context) ([]byte, error)) ([]byte, error) {
	waitID := fmt.Sprintf("%s/wait/%d", journal.runID, logged.Sequence)
	wait, err := journal.runner.store.GetWait(ctx, waitID)
	if errors.Is(err, ErrNotFound) {
		spec, waitErr := tool.Wait(ctx, logged)
		if waitErr != nil {
			return nil, waitErr
		}
		wait, err = journal.runner.store.EnsureWait(ctx, journal.runID, logged.Sequence, spec)
	}
	if err != nil {
		return nil, err
	}
	if wait.Decision == nil && wait.Spec.Deadline != nil && !time.Now().Before(*wait.Spec.Deadline) {
		if err := journal.runner.store.ResolveWait(ctx, wait.ID, Decision{Error: "wait expired"}); err != nil {
			return nil, err
		}
		wait, err = journal.runner.store.GetWait(ctx, wait.ID)
		if err != nil {
			return nil, err
		}
	}
	if wait.Decision == nil {
		return nil, &ParkError{RunID: journal.runID, WaitID: wait.ID, Sequence: logged.Sequence, Reason: "waiting for decision"}
	}
	decision := wait.Decision
	return journal.replayAndComplete(ctx, logged, dispatch, decision.Result, decision.Error)
}

func (journal *journal) complete(ctx context.Context, logged capability.LoggedCall, response []byte) ([]byte, error) {
	persisted, err := journal.runner.store.CompleteCall(ctx, journal.runID, logged.Sequence, append(json.RawMessage(nil), response...))
	if err != nil {
		return nil, err
	}
	return append(json.RawMessage(nil), persisted...), nil
}

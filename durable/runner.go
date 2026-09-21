package durable

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	pysolate "github.com/bkmashiro/agent-python-runtime"
)

// RecoveryMode states what the host can safely do after a recorded call has
// been durably reserved but its result is not yet known.
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

// SchedulingClass controls only local Executor capacity while a Host callback
// is live. It does not change durable recovery or replay semantics.
type SchedulingClass string

const (
	// Inline keeps the running slot for the whole Host call. It is the default.
	Inline SchedulingClass = "inline"
	// ExternalIO lets an Executor reuse the running slot while the opted-in
	// Host call blocks. The live Guest still counts against MaxResident.
	ExternalIO SchedulingClass = "external_io"
)

// Tool is the durable declaration for one Host tool. Version and Recovery
// are persisted with a run; changing either one makes that run non-resumable.
// Scheduling is Host-local resource policy and is deliberately not persisted.
type Tool struct {
	Name       string
	Version    string
	Call       pysolate.Tool
	Recovery   RecoveryMode
	Lookup     LookupFunc
	Wait       WaitFunc
	Scheduling SchedulingClass
	// Observer receives Host-local timing data and is not persisted with the
	// durable declaration. It should return quickly.
	Observer ToolObserver
}

// LookupFunc resolves a previously reserved external call. Done may contain a
// JSON result, an error-only result, or both only when the caller deliberately
// uses the ordinary wire outcome. Pending leaves the run resumable.
type LookupFunc func(context.Context, json.RawMessage) (LookupResult, error)

// WaitFunc declares a durable wait. It is called only for a new wait row;
// the persisted row is authoritative on all later attempts.
type WaitFunc func(context.Context, json.RawMessage) (WaitSpec, error)

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

// ParkError means that the Guest was stopped without changing the run's
// terminal state. The caller may resolve a wait or retry the run later.
type ParkError struct {
	Kind     ParkKind
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

// OperationKey returns the stable run/sequence key supplied to a live tool.
func OperationKey(ctx context.Context) (string, bool) {
	key, ok := ctx.Value(operationKeyContextKey{}).(string)
	return key, ok && key != ""
}

type operationKeyContextKey struct{}

type toolDeclaration struct {
	Name     string       `json:"name"`
	Version  string       `json:"version"`
	Recovery RecoveryMode `json:"recovery"`
}

// Runner owns the durable store, compiled artifact, and tool declarations.
// Guest instances are always created afresh by RunRecorded.
// Preparation is an optional immutable image for one recorded seed.
// COW requires Linux; no per-seed cache or silent fallback is created.
type Preparation struct {
	Seed string
	COW  bool
}

type Runner struct {
	preparedSeed       string
	store              *Store
	core               *pysolate.Runner
	artifactSHA256     string
	environmentVersion string
	tools              map[string]Tool
	toolSnapshot       json.RawMessage

	mu     sync.Mutex
	active map[string]context.CancelFunc
	closed bool
}

// NewRunner validates the durable declarations and compiles the artifact once.
// The compiled core is reused for fresh Guest instances on every attempt.
func NewRunner(ctx context.Context, store *Store, artifact []byte, environmentVersion string, tools []Tool, preparation ...Preparation) (*Runner, error) {
	if store == nil || len(artifact) == 0 || environmentVersion == "" {
		return nil, ErrInvalidRunner
	}

	if len(preparation) > 1 || (len(preparation) == 1 && preparation[0].Seed == "") {
		return nil, ErrInvalidRunner
	}
	toolMap := make(map[string]Tool, len(tools))
	for _, tool := range tools {
		if tool.Name == "" || tool.Version == "" {
			return nil, fmt.Errorf("%w: tool name and version are required", ErrInvalidRunner)
		}
		if _, exists := toolMap[tool.Name]; exists {
			return nil, fmt.Errorf("%w: duplicate tool %q", ErrInvalidRunner, tool.Name)
		}
		switch tool.Scheduling {
		case "", Inline:
			tool.Scheduling = Inline
		case ExternalIO:
			if tool.Recovery == WaitMode {
				return nil, fmt.Errorf("%w: wait tool %q cannot use external I/O scheduling", ErrInvalidRunner, tool.Name)
			}
		default:
			return nil, fmt.Errorf("%w: tool %q has invalid scheduling class", ErrInvalidRunner, tool.Name)
		}
		switch tool.Recovery {
		case RetrySafe, Idempotent, Manual:
			if tool.Call == nil {
				return nil, fmt.Errorf("%w: tool %q has no call", ErrInvalidRunner, tool.Name)
			}
		case Lookup:
			if tool.Call == nil {
				return nil, fmt.Errorf("%w: tool %q has no call", ErrInvalidRunner, tool.Name)
			}
			if tool.Lookup == nil {
				return nil, fmt.Errorf("%w: lookup tool %q has no resolver", ErrInvalidRunner, tool.Name)
			}
		case WaitMode:
			if tool.Wait == nil {
				return nil, fmt.Errorf("%w: wait tool %q has no wait declaration", ErrInvalidRunner, tool.Name)
			}
			if tool.Call == nil {
				tool.Call = waitToolPlaceholder
			}
		default:
			return nil, fmt.Errorf("%w: tool %q has no recovery declaration", ErrInvalidRunner, tool.Name)
		}
		toolMap[tool.Name] = tool
	}

	snapshot := make([]toolDeclaration, 0, len(toolMap))
	manifest := make(pysolate.Manifest, len(toolMap))
	for _, tool := range toolMap {
		snapshot = append(snapshot, toolDeclaration{Name: tool.Name, Version: tool.Version, Recovery: tool.Recovery})
		manifest[tool.Name] = pysolate.ToolSpec{Call: scheduledTool(tool)}
	}
	sort.Slice(snapshot, func(left, right int) bool { return snapshot[left].Name < snapshot[right].Name })
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("%w: encode tool declarations: %v", ErrInvalidRunner, err)
	}
	var core *pysolate.Runner
	preparedSeed := ""
	if len(preparation) == 0 {
		core, err = pysolate.New(ctx, artifact, manifest)
	} else {
		preparedSeed = preparation[0].Seed
		if preparation[0].COW {
			core, err = pysolate.NewPreparedRecordedCOW(ctx, artifact, manifest, preparedSeed)
		} else {
			core, err = pysolate.NewPreparedRecorded(ctx, artifact, manifest, preparedSeed)
		}
	}
	if err != nil {
		return nil, err
	}

	return &Runner{
		preparedSeed:       preparedSeed,
		store:              store,
		core:               core,
		artifactSHA256:     core.ArtifactID(),
		environmentVersion: environmentVersion,
		tools:              toolMap,
		toolSnapshot:       json.RawMessage(encoded),
		active:             make(map[string]context.CancelFunc),
	}, nil
}

func scheduledTool(tool Tool) pysolate.Tool {
	return func(ctx context.Context, args json.RawMessage) (any, error) {
		return executeScheduled(ctx, tool, ToolCall, args, func() (any, error) {
			return tool.Call(ctx, args)
		})
	}
}

func executeScheduled[T any](ctx context.Context, tool Tool, operation ToolOperation, args json.RawMessage, call func() (T, error)) (result T, err error) {
	observation := ToolObservation{
		Name: tool.Name, Version: tool.Version, Operation: operation,
		Scheduling: tool.Scheduling, ArgumentBytes: len(args),
	}
	observation.OperationKey, _ = OperationKey(ctx)
	defer func() {
		observation.Outcome = classifyToolOutcome(err)
		if tool.Observer != nil {
			tool.Observer.ObserveTool(observation)
		}
	}()

	if tool.Scheduling == ExternalIO {
		started := time.Now()
		if err = yieldExecution(ctx); err != nil {
			observation.QueueDuration = time.Since(started)
			return result, err
		}
		observation.QueueDuration = time.Since(started)
	}

	started := time.Now()
	result, err = call()
	observation.ServiceDuration = time.Since(started)
	if tool.Scheduling == ExternalIO {
		started = time.Now()
		err = errors.Join(err, reacquireExecution(ctx))
		observation.ResumeDuration = time.Since(started)
	}
	return result, err
}

func classifyToolOutcome(err error) ToolOutcome {
	switch {
	case err == nil:
		return ToolSucceeded
	case errors.Is(err, context.DeadlineExceeded):
		return ToolDeadline
	case errors.Is(err, context.Canceled):
		return ToolCancelled
	default:
		return ToolFailed
	}
}

func waitToolPlaceholder(context.Context, json.RawMessage) (any, error) {
	return nil, errors.New("wait tool must be resolved by the durable journal")
}

// ArtifactID identifies the exact Guest artifact pinned by new runs.
func (runner *Runner) ArtifactID() string {
	if runner == nil {
		return ""
	}
	return runner.artifactSHA256
}

// Create persists a run definition and pins this runner's declaration
// snapshot. It does not execute Guest code.
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
	if runner == nil || definition.ID == "" || definition.Code == "" ||
		definition.Seed == "" || definition.EnvironmentVersion != runner.environmentVersion ||
		definition.ArtifactSHA256 != runner.artifactSHA256 {
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

// Resume claims one run and executes it with the shared compiled core and a
// fresh Guest. PythonError is terminal; infrastructure errors stay resumable.
func (runner *Runner) Resume(ctx context.Context, runID string) (pysolate.Output, error) {
	if runner == nil || runner.store == nil || runner.core == nil || runID == "" {
		return pysolate.Output{}, ErrInvalidRunner
	}

	runner.mu.Lock()
	closed := runner.closed
	runner.mu.Unlock()
	if closed {
		return pysolate.Output{}, ErrInvalidRunner
	}

	release, err := runner.store.Claim(runID)
	if err != nil {
		return pysolate.Output{}, err
	}
	defer func() { _ = release() }()

	run, err := runner.store.Get(ctx, runID)
	if err != nil {
		return pysolate.Output{}, err
	}
	if run.Status == StatusBlocked {
		return pysolate.Output{}, fmt.Errorf("%w: %s", ErrBlocked, run.Reason)
	}
	if definitionErr := runner.validateDefinition(run.Definition); definitionErr != nil {
		blockErr := runner.store.SetState(context.Background(), runID, StatusBlocked, nil, definitionErr.Error())
		if blockErr != nil && !errors.Is(blockErr, ErrConflict) {
			return pysolate.Output{}, errors.Join(ErrBlocked, blockErr)
		}
		return pysolate.Output{}, errors.Join(ErrBlocked, definitionErr)
	}
	if run.Status == StatusCancelled {
		return pysolate.Output{}, ErrCancelled
	}
	if run.Status == StatusCompleted || run.Status == StatusFailed {
		var output pysolate.Output
		if len(run.Outcome) == 0 || json.Unmarshal(run.Outcome, &output) != nil {
			return output, fmt.Errorf("%w: stored output is invalid", ErrInvalidRunner)
		}
		if run.Status == StatusFailed {
			return output, &pysolate.PythonError{Message: run.Reason}
		}
		return output, nil
	}

	if runner.preparedSeed != "" && run.Definition.Seed != runner.preparedSeed {
		return pysolate.Output{}, fmt.Errorf("%w: preparation seed does not match run", ErrInvalidRunner)
	}

	attemptCtx, cancel := context.WithCancel(ctx)
	runner.mu.Lock()
	if runner.closed {
		runner.mu.Unlock()
		cancel()
		return pysolate.Output{}, ErrInvalidRunner
	}
	runner.active[runID] = cancel
	runner.mu.Unlock()
	defer func() {
		cancel()
		runner.mu.Lock()
		delete(runner.active, runID)
		runner.mu.Unlock()
	}()

	// Cancel may have committed between the first read and active registration.
	latest, err := runner.store.Get(context.Background(), runID)
	if err != nil {
		return pysolate.Output{}, err
	}
	if latest.Status == StatusCancelled {
		cancel()
		return pysolate.Output{}, ErrCancelled
	}

	journal := &journal{runner: runner, runID: runID}
	output, runErr := runner.core.RunRecorded(attemptCtx, run.Definition.Code,
		json.RawMessage(run.Definition.Inputs), run.Definition.Seed, journal)

	latest, statusErr := runner.store.Get(context.Background(), runID)
	if statusErr != nil {
		return output, statusErr
	}
	if latest.Status == StatusCancelled {
		return output, ErrCancelled
	}

	var pythonErr *pysolate.PythonError
	completed := runErr == nil || errors.As(runErr, &pythonErr)
	if completed {
		remaining, countErr := runner.store.hasCall(context.Background(), runID, journal.count())
		if countErr != nil {
			return output, countErr
		}
		if remaining {
			return output, runner.blockAttempt(runID, "recorded call history was not fully consumed")
		}
	}

	if runErr != nil {
		if pythonErr != nil {
			stored, encodeErr := json.Marshal(output)
			if encodeErr != nil {
				return output, encodeErr
			}
			reason := pythonErr.Message
			if reason == "" {
				reason = pythonErr.Error()
			}
			if stateErr := runner.store.SetState(context.Background(), runID, StatusFailed, stored, reason); stateErr != nil {
				if current, getErr := runner.store.Get(context.Background(), runID); getErr == nil && current.Status == StatusCancelled {
					return output, ErrCancelled
				}
				return output, stateErr
			}
			return output, runErr
		}
		if errors.Is(runErr, ErrHistoryMismatch) || errors.Is(runErr, ErrBlocked) {
			return output, runner.blockAttempt(runID, runErr.Error())
		}
		return output, runErr
	}

	stored, err := json.Marshal(output)
	if err != nil {
		return output, err
	}
	if err := runner.store.SetState(context.Background(), runID, StatusCompleted, stored, ""); err != nil {
		if current, getErr := runner.store.Get(context.Background(), runID); getErr == nil && current.Status == StatusCancelled {
			return output, ErrCancelled
		}
		return output, err
	}
	return output, nil
}

func (runner *Runner) blockAttempt(runID, reason string) error {
	if err := runner.store.SetState(context.Background(), runID, StatusBlocked, nil, reason); err != nil {
		return errors.Join(ErrBlocked, err)
	}
	return fmt.Errorf("%w: %s", ErrBlocked, reason)
}

// Decide records a wait decision. It never executes Guest code.
func (runner *Runner) Decide(ctx context.Context, waitID string, decision Decision) error {
	if runner == nil || runner.store == nil || waitID == "" {
		return ErrInvalidRunner
	}
	return runner.store.ResolveWait(ctx, waitID, decision)
}

// Cancel commits cancellation before notifying a local attempt. A late tool
// result may still be recorded, but Store will not let it reopen the run.
func (runner *Runner) Cancel(ctx context.Context, runID string) error {
	if runner == nil || runner.store == nil || runID == "" {
		return ErrInvalidRunner
	}
	if err := runner.store.SetState(ctx, runID, StatusCancelled, nil, "cancelled"); err != nil {
		return err
	}
	runner.mu.Lock()
	cancel := runner.active[runID]
	runner.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

// Close refuses to close while an attempt is using the runner. The Store is
// caller-owned and is therefore not closed here.
func (runner *Runner) Close(ctx context.Context) error {
	if runner == nil {
		return nil
	}
	runner.mu.Lock()
	if len(runner.active) != 0 {
		runner.mu.Unlock()
		return ErrBusy
	}
	if runner.closed {
		runner.mu.Unlock()
		return nil
	}
	runner.closed = true
	core := runner.core
	runner.mu.Unlock()
	if core == nil {
		return nil
	}
	return core.Close(ctx)
}

type journal struct {
	runner   *Runner
	runID    string
	sequence uint32
	history  []Call
	live     bool
}

func (journal *journal) count() uint32 {
	return journal.sequence
}

func (journal *journal) nextCall() uint32 {
	sequence := journal.sequence
	journal.sequence++
	return sequence
}

// Call implements pysolate.Journal. The sequence is assigned by this fresh
// attempt; Store then decides whether this is a new, replayed, or completed
// call before any external dispatch is allowed.
func (journal *journal) Call(ctx context.Context, tool string, args json.RawMessage, next func(context.Context) []byte) ([]byte, error) {
	sequence := journal.nextCall()
	logged := LoggedCall{
		Sequence:   sequence,
		CallID:     fmt.Sprintf("call-%d", sequence),
		Capability: tool,
		Arguments:  append(json.RawMessage(nil), args...),
	}
	if !journal.live {
		if len(journal.history) == 0 {
			var err error
			journal.history, err = journal.runner.store.readCompleted(ctx, journal.runID, sequence)
			if err != nil {
				return nil, err
			}
		}
		if len(journal.history) > 0 {
			record := journal.history[0]
			journal.history[0] = Call{}
			journal.history = journal.history[1:]
			if record.CallID != logged.CallID || record.Tool != tool || !bytes.Equal(record.Arguments, args) {
				return nil, ErrHistoryMismatch
			}
			return record.Outcome, nil
		}
		journal.live = true
	}
	record, created, err := journal.runner.store.BeginCall(ctx, journal.runID, logged)
	if err != nil {
		return nil, err
	}
	if record.State == CallCompleted {
		return append([]byte(nil), record.Outcome...), nil
	}

	operationCtx := context.WithValue(ctx, operationKeyContextKey{}, record.OperationKey)
	declared, ok := journal.runner.tools[tool]
	if !ok {
		return journal.dispatchAndComplete(operationCtx, logged, next)
	}
	if created {
		if declared.Recovery == WaitMode {
			return journal.wait(operationCtx, logged, declared)
		}
		return journal.dispatchAndComplete(operationCtx, logged, next)
	}

	switch declared.Recovery {
	case RetrySafe, Idempotent:
		return journal.dispatchAndComplete(operationCtx, logged, next)
	case Lookup:
		return journal.lookup(operationCtx, logged, declared, next)
	case Manual:
		return nil, fmt.Errorf("%w: manual recovery required", ErrBlocked)
	case WaitMode:
		return journal.wait(operationCtx, logged, declared)
	default:
		return nil, ErrInvalidRunner
	}
}

func (journal *journal) dispatchAndComplete(ctx context.Context, logged LoggedCall, next func(context.Context) []byte) ([]byte, error) {
	response := next(ctx)
	return journal.complete(logged, response)
}

func (journal *journal) complete(logged LoggedCall, response []byte) ([]byte, error) {
	persisted, err := journal.runner.store.CompleteCall(context.Background(), journal.runID, logged.Sequence, append(json.RawMessage(nil), response...))
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), persisted...), nil
}

func (journal *journal) lookup(ctx context.Context, logged LoggedCall, tool Tool, next func(context.Context) []byte) ([]byte, error) {
	resolved, err := executeScheduled(ctx, tool, ToolLookup, logged.Arguments, func() (LookupResult, error) {
		return tool.Lookup(ctx, logged.Arguments)
	})
	if err != nil {
		return nil, err
	}
	switch resolved.State {
	case LookupSafeToDispatch:
		return journal.dispatchAndComplete(ctx, logged, next)
	case LookupPending:
		return nil, &ParkError{Kind: ParkLookup, RunID: journal.runID, Sequence: logged.Sequence, Reason: "lookup pending"}
	case LookupDone:
		response, err := lookupResponse(resolved)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrBlocked, err)
		}
		return journal.complete(logged, response)
	default:
		return nil, fmt.Errorf("%w: lookup result is unknown", ErrBlocked)
	}
}

func lookupResponse(result LookupResult) ([]byte, error) {
	if result.Error == "" && len(result.Result) == 0 {
		return nil, errors.New("lookup returned neither a result nor an error")
	}
	if len(result.Result) != 0 && !json.Valid(result.Result) {
		return nil, errors.New("lookup returned invalid JSON")
	}
	return encodeWireOutcome(result.Result, result.Error), nil
}

func encodeWireOutcome(value json.RawMessage, errorText string) []byte {
	var errorValue *string
	if errorText != "" {
		errorValue = &errorText
	}
	response := struct {
		Value json.RawMessage `json:"value"`
		Error *string         `json:"error,omitempty"`
	}{Value: value, Error: errorValue}
	encoded, err := json.Marshal(response)
	if err != nil {
		return []byte(`{"value":null,"error":"durable outcome encoding failed"}`)
	}
	return encoded
}

func (journal *journal) wait(ctx context.Context, logged LoggedCall, tool Tool) ([]byte, error) {
	waitID := fmt.Sprintf("%s/wait/%d", journal.runID, logged.Sequence)
	wait, err := journal.runner.store.GetWait(ctx, waitID)
	if errors.Is(err, ErrNotFound) {
		spec, declarationErr := tool.Wait(ctx, logged.Arguments)
		if declarationErr != nil {
			return nil, declarationErr
		}
		wait, err = journal.runner.store.EnsureWait(ctx, journal.runID, logged.Sequence, spec)
	}
	if err != nil {
		return nil, err
	}
	if wait.Decision == nil && wait.Spec.Deadline != nil && !time.Now().Before(*wait.Spec.Deadline) {
		if err := journal.runner.store.ResolveWait(context.Background(), wait.ID, Decision{Error: "wait expired"}); err != nil {
			return nil, err
		}
		wait, err = journal.runner.store.GetWait(context.Background(), wait.ID)
		if err != nil {
			return nil, err
		}
	}
	if wait.Decision == nil {
		return nil, &ParkError{Kind: ParkWait, RunID: journal.runID, WaitID: wait.ID, Sequence: logged.Sequence, Reason: "waiting for decision"}
	}
	response := encodeWireOutcome(wait.Decision.Result, wait.Decision.Error)
	return journal.complete(logged, response)
}

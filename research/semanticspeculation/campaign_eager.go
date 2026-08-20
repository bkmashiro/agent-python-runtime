package semanticspeculation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/streaming"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

type ProviderObservation struct {
	Attempts            uint32
	ResultBytes         uint64
	CostUnits           uint64
	ElapsedNanos        uint64
	Dispositions        PhysicalDispositions
	ReadyBeforeFinalize uint32
}

type EagerGuestTreatmentConfig struct {
	Artifact            []byte
	RunConfig           runtimeconfig.RunConfig
	Plan                *capability.Plan
	BrokerFactory       func(context.Context) (*capability.Broker, error)
	AllowedImportRoots  []string
	ProviderObservation func() ProviderObservation
	RunID               string
	WorkspaceRoot       string
	WorkspaceOwner      string
}

type eagerGuestRunResult struct {
	response []byte
	err      error
}

type EagerGuestTreatment struct {
	config    EagerGuestTreatmentConfig
	ctx       context.Context
	cancel    context.CancelFunc
	runner    streaming.StreamRunner
	broker    *capability.Broker
	prepares  chan string
	completed chan eagerGuestRunResult
	once      sync.Once
	manager   *workspace.Manager
	attempt   *workspace.Attempt
}

func NewEagerGuestTreatment(config EagerGuestTreatmentConfig) (*EagerGuestTreatment, error) {
	if len(config.Artifact) == 0 || config.Plan == nil || config.BrokerFactory == nil || config.RunID == "" || config.WorkspaceRoot == "" || config.WorkspaceOwner == "" {
		return nil, errors.New("invalid EAGER Guest treatment")
	}
	return &EagerGuestTreatment{config: config}, nil
}

func (t *EagerGuestTreatment) Begin(ctx context.Context, inputs json.RawMessage) error {
	if t == nil || t.runner != nil || ctx == nil {
		return errors.New("invalid EAGER Guest begin")
	}
	if err := os.MkdirAll(t.config.WorkspaceRoot, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(t.config.WorkspaceRoot, 0o700); err != nil {
		return err
	}
	manager, err := workspace.NewManager(t.config.WorkspaceRoot)
	if err != nil {
		return err
	}
	ref, err := manager.Create(nil, workspace.DefaultLimits())
	if err != nil {
		_ = manager.Close()
		return err
	}
	attempt, err := manager.ForkAttempt(ref)
	if err != nil {
		_ = manager.Close()
		return err
	}
	t.manager, t.attempt = manager, attempt
	factory := wazeroengine.Factory{WorkspaceManager: manager, WorkspaceRef: attempt.Ref(), WorkspaceOwner: t.config.WorkspaceOwner, BrokerFactory: func(inner context.Context) (*capability.Broker, error) {
		broker, err := t.config.BrokerFactory(inner)
		if err == nil {
			t.broker = broker
		}
		return broker, err
	}}
	runner, err := factory.New(ctx, t.config.Artifact, t.config.RunConfig)
	if err != nil {
		_ = attempt.Discard()
		_ = manager.Close()
		return err
	}
	streamRunner, ok := runner.(streaming.StreamRunner)
	if !ok {
		_ = runner.Close(ctx)
		_ = attempt.Discard()
		_ = manager.Close()
		return errors.New("Guest runner lacks stream support")
	}
	begin, err := BuildEagerComparatorBeginPrepare(EagerComparatorPrepareConfig{
		Inputs: inputs, Plan: t.config.Plan, AllowedImportRoots: t.config.AllowedImportRoots,
	})
	if err != nil {
		_ = runner.Close(ctx)
		_ = attempt.Discard()
		_ = manager.Close()
		return err
	}
	t.ctx, t.cancel = context.WithCancel(ctx)
	t.runner = streamRunner
	t.prepares = make(chan string, 512)
	t.completed = make(chan eagerGuestRunResult, 1)
	request, _ := json.Marshal(runtimeconfig.RunRequest{RunID: t.config.RunID, Code: "result = comparator_final", Inputs: json.RawMessage(`{}`)})
	go func() {
		response, runErr := t.runner.RunStream(t.ctx, request, t.prepares)
		t.completed <- eagerGuestRunResult{response: response, err: runErr}
	}()
	t.prepares <- begin
	return nil
}

func (t *EagerGuestTreatment) ObserveChunk(ctx context.Context, chunk string) error {
	if t == nil || t.prepares == nil {
		return errors.New("EAGER Guest treatment not begun")
	}
	fragment, err := BuildEagerComparatorChunkPrepare(chunk)
	if err != nil {
		return err
	}
	select {
	case t.prepares <- fragment:
		return nil
	case completed := <-t.completed:
		t.completed <- completed
		if completed.err != nil {
			return completed.err
		}
		return errors.New("EAGER Guest ended before source seal")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *EagerGuestTreatment) Finalize(ctx context.Context) (TreatmentOutcome, error) {
	if t == nil || t.prepares == nil {
		return TreatmentOutcome{}, errors.New("EAGER Guest treatment not begun")
	}
	select {
	case t.prepares <- BuildEagerComparatorFinishPrepare():
	case <-ctx.Done():
		return TreatmentOutcome{}, ctx.Err()
	}
	close(t.prepares)
	completed := <-t.completed
	closeErr := t.runner.Close(ctx)
	if completed.err != nil {
		return TreatmentOutcome{}, completed.err
	}
	if closeErr != nil {
		return TreatmentOutcome{}, closeErr
	}
	var envelope struct {
		Status string `json:"status"`
		Result struct {
			Outcome                string          `json:"outcome"`
			ErrorClass             string          `json:"error_class"`
			Result                 json.RawMessage `json:"result"`
			ResultPresent          bool            `json:"result_present"`
			PrefixPythonExecutions uint32          `json:"prefix_python_executions"`
			PythonExecutions       uint32          `json:"python_executions"`
		} `json:"result"`
	}
	if err := json.Unmarshal(completed.response, &envelope); err != nil || envelope.Status != "ok" {
		return TreatmentOutcome{}, fmt.Errorf("invalid EAGER Guest response: %w", err)
	}
	outcome := TreatmentOutcome{
		FinalProgramOutcome:    envelope.Result.Outcome,
		FinalPythonStarted:     envelope.Result.PythonExecutions > envelope.Result.PrefixPythonExecutions,
		PrefixPythonExecutions: envelope.Result.PrefixPythonExecutions,
		AuthorityDisposition:   "unchanged", WorkspaceDisposition: "untouched",
	}
	if envelope.Result.Outcome == "success" && envelope.Result.ResultPresent {
		var result any
		if json.Unmarshal(envelope.Result.Result, &result) != nil {
			return TreatmentOutcome{}, errors.New("invalid EAGER Guest result")
		}
		canonical, _ := json.Marshal(result)
		outcome.ResultSHA256 = syntheticDigest(canonical)
	} else if envelope.Result.Outcome == "syntax_error" {
		outcome.ErrorClass = "syntax_error"
	} else {
		outcome.ErrorClass = envelope.Result.ErrorClass
	}
	if t.broker != nil {
		outcome.LogicalCalls = uint32(t.broker.Calls())
	}
	if t.config.ProviderObservation != nil {
		provider := t.config.ProviderObservation()
		outcome.PhysicalAttempts = provider.Attempts
		outcome.PhysicalResultBytes = provider.ResultBytes
		outcome.ProviderCostUnits = provider.CostUnits
		outcome.PhysicalDispositions = provider.Dispositions
		outcome.ReadyBeforeFinalize = provider.ReadyBeforeFinalize
	}
	if outcome.LogicalCalls > 0 {
		outcome.AuthorityDisposition = "read_consumed"
	}
	if outcome.FinalProgramOutcome == "success" {
		if _, err := t.attempt.Publish(); err != nil {
			_ = t.manager.Close()
			return TreatmentOutcome{}, err
		}
		outcome.WorkspaceDisposition = "published"
	} else {
		if err := t.attempt.Discard(); err != nil {
			_ = t.manager.Close()
			return TreatmentOutcome{}, err
		}
		outcome.WorkspaceDisposition = "discarded"
	}
	if err := t.manager.Close(); err != nil {
		return TreatmentOutcome{}, err
	}
	return outcome, nil
}

func (t *EagerGuestTreatment) Cancel(ctx context.Context) error {
	if t == nil {
		return nil
	}
	t.once.Do(func() {
		if t.cancel != nil {
			t.cancel()
		}
		if t.prepares != nil {
			defer func() { _ = recover() }()
			close(t.prepares)
		}
	})
	if t.attempt != nil {
		_ = t.attempt.Discard()
	}
	if t.manager != nil {
		_ = t.manager.Close()
	}
	if t.runner != nil {
		return t.runner.Close(ctx)
	}
	return nil
}

package semanticspeculation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/playback"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

// SerialGuestTreatmentConfig configures the research-only whole-file treatment.
// The treatment deliberately uses the ordinary Guest Run path: source chunks
// are retained by the Host and admitted as one final request only after the
// frozen schedule has released them.
type SerialGuestTreatmentConfig struct {
	Artifact            []byte
	RunConfig           runtimeconfig.RunConfig
	Plan                *capability.Plan
	BrokerFactory       func(context.Context) (*capability.Broker, error)
	ProviderObservation func() ProviderObservation
	RunID               string
	WorkspaceRoot       string
	WorkspaceOwner      string
}

type SerialGuestTreatment struct {
	config               SerialGuestTreatmentConfig
	inputs               json.RawMessage
	source               strings.Builder
	runner               enginecontract.Runner
	broker               *capability.Broker
	manager              *workspace.Manager
	attempt              *workspace.Attempt
	begun                bool
	finalized            bool
	formalExecutionNanos uint64
	once                 sync.Once
}

func NewSerialGuestTreatment(config SerialGuestTreatmentConfig) (*SerialGuestTreatment, error) {
	if len(config.Artifact) == 0 || config.Plan == nil || config.BrokerFactory == nil || config.RunID == "" ||
		config.WorkspaceRoot == "" || config.WorkspaceOwner == "" {
		return nil, errors.New("invalid serial whole-file Guest treatment")
	}
	return &SerialGuestTreatment{config: config}, nil
}

func (t *SerialGuestTreatment) Begin(ctx context.Context, inputs json.RawMessage) error {
	if t == nil || t.begun || t.runner != nil || ctx == nil || len(inputs) == 0 || !json.Valid(inputs) {
		return errors.New("invalid serial whole-file Guest begin")
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
	factory := wazeroengine.Factory{
		WorkspaceManager: manager,
		WorkspaceRef:     attempt.Ref(),
		WorkspaceOwner:   t.config.WorkspaceOwner,
		BrokerFactory: func(inner context.Context) (*capability.Broker, error) {
			broker, brokerErr := t.config.BrokerFactory(inner)
			if brokerErr == nil {
				t.broker = broker
			}
			return broker, brokerErr
		},
	}
	runner, err := factory.New(ctx, t.config.Artifact, t.config.RunConfig)
	if err != nil {
		_ = attempt.Discard()
		_ = manager.Close()
		return err
	}
	t.runner = runner
	t.inputs = append(json.RawMessage(nil), inputs...)
	t.begun = true
	return nil
}

func (t *SerialGuestTreatment) ObserveChunk(ctx context.Context, chunk string) error {
	if t == nil || !t.begun || t.runner == nil || t.finalized {
		return errors.New("serial whole-file Guest treatment not accepting chunks")
	}
	if ctx == nil {
		return errors.New("serial whole-file Guest chunk context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if chunk == "" {
		return errors.New("serial whole-file Guest chunks must be non-empty")
	}
	t.source.WriteString(chunk)
	return nil
}

func (t *SerialGuestTreatment) Finalize(ctx context.Context) (TreatmentOutcome, error) {
	if t == nil || !t.begun || t.runner == nil || t.finalized || ctx == nil {
		return TreatmentOutcome{}, errors.New("serial whole-file Guest treatment not ready to finalize")
	}
	t.finalized = true
	request, err := json.Marshal(runtimeconfig.RunRequest{
		RunID:  t.config.RunID,
		Code:   t.source.String(),
		Inputs: append(json.RawMessage(nil), t.inputs...),
	})
	if err != nil {
		return TreatmentOutcome{}, err
	}
	formalStarted := time.Now()
	responseBytes, runErr := t.runner.Run(ctx, request, t.config.Plan.PythonPrelude())
	t.formalExecutionNanos = uint64(time.Since(formalStarted))
	closeErr := t.runner.Close(ctx)
	if runErr != nil {
		if errors.Is(runErr, runtimeconfig.ErrAgentSourceInvalid) {
			outcome := TreatmentOutcome{
				FinalProgramOutcome:  "syntax_error",
				ErrorClass:           "syntax_error",
				AuthorityDisposition: "unchanged",
				WorkspaceDisposition: "discarded",
			}
			if discardErr := t.attempt.Discard(); discardErr != nil {
				return TreatmentOutcome{}, discardErr
			}
			if managerErr := t.manager.Close(); managerErr != nil {
				return TreatmentOutcome{}, managerErr
			}
			return outcome, nil
		}
		return TreatmentOutcome{}, errors.Join(runErr, closeErr)
	}
	if closeErr != nil {
		return TreatmentOutcome{}, closeErr
	}

	requestValue, err := runtimeconfig.DecodeRunRequest(request)
	if err != nil {
		return TreatmentOutcome{}, err
	}
	response, err := runtimeconfig.DecodeAndValidateRunResponse(requestValue, responseBytes)
	if err != nil {
		return TreatmentOutcome{}, fmt.Errorf("invalid serial Guest response: %w", err)
	}
	outcome := TreatmentOutcome{
		FinalPythonStarted:   true,
		AuthorityDisposition: "unchanged",
		WorkspaceDisposition: "discarded",
	}
	if t.broker != nil {
		outcome.LogicalCalls = t.broker.Calls()
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

	switch response.Status {
	case runtimeconfig.RunResponseOK:
		outcome.FinalProgramOutcome = "success"
		if response.ResultPresent != nil && *response.ResultPresent {
			var result any
			if err := json.Unmarshal(response.Result, &result); err != nil {
				return TreatmentOutcome{}, errors.New("invalid serial Guest result")
			}
			canonical, err := json.Marshal(result)
			if err != nil {
				return TreatmentOutcome{}, errors.New("invalid serial Guest result")
			}
			outcome.ResultSHA256, err = playback.CanonicalSHA256(canonical)
			if err != nil {
				return TreatmentOutcome{}, errors.New("invalid serial Guest result")
			}
		}
	case runtimeconfig.RunResponseError:
		outcome.FinalProgramOutcome = "runtime_error"
		if response.Error == nil || response.Error.ErrorType == nil || *response.Error.ErrorType == "" {
			return TreatmentOutcome{}, errors.New("serial Guest error has no error class")
		}
		outcome.ErrorClass = *response.Error.ErrorType
	default:
		return TreatmentOutcome{}, errors.New("serial Guest response has an invalid status")
	}

	if outcome.FinalProgramOutcome == "success" {
		if _, err := t.attempt.Publish(); err != nil {
			return TreatmentOutcome{}, err
		}
		outcome.WorkspaceDisposition = "published"
	} else {
		if err := t.attempt.Discard(); err != nil {
			return TreatmentOutcome{}, err
		}
	}
	if err := t.manager.Close(); err != nil {
		return TreatmentOutcome{}, err
	}
	return outcome, nil
}

func (t *SerialGuestTreatment) FormalExecutionNanos() uint64 {
	if t == nil {
		return 0
	}
	return t.formalExecutionNanos
}

func (t *SerialGuestTreatment) Cancel(ctx context.Context) error {
	if t == nil {
		return nil
	}
	var closeErr error
	t.once.Do(func() {
		if t.runner != nil {
			closeErr = t.runner.Close(ctx)
		}
		if t.attempt != nil {
			_ = t.attempt.Discard()
		}
		if t.manager != nil {
			_ = t.manager.Close()
		}
	})
	return closeErr
}

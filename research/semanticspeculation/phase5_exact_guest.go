package semanticspeculation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"runtime"
	"sync"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
)

var ErrPhase5DerivedOperationsUnavailable = errors.New("phase 5 derived Exact Guest operations are not yet provisioned")

// Phase5ExactGuestOperations owns one run-private Exact Guest operation graph.
// The first delivered slice supports the complete original lane; derived methods
// fail closed until their three-capacity state machine is attached.
type Phase5ExactGuestOperations struct {
	mu       sync.Mutex
	artifact []byte
	config   runtimeconfig.RunConfig

	finalEngine   *wazeroengine.Engine
	finalCapacity *wazeroengine.PreparedRegionFinalCapacity
	request       []byte
	snapshot      Phase5ExecutionSnapshot
	teardown      bool
}

func NewPhase5ExactGuestOperations(artifact []byte, config runtimeconfig.RunConfig) (*Phase5ExactGuestOperations, error) {
	if len(artifact) == 0 || config.ExecutionProfile == nil {
		return nil, errors.New("invalid phase 5 Exact Guest operations config")
	}
	config.Mechanisms.SemanticAnalysis = true
	config.Mechanisms.PreparedRuntime = true
	config.Mechanisms.MemoryCOW = runtime.GOOS == "linux"
	return &Phase5ExactGuestOperations{artifact: append([]byte(nil), artifact...), config: config}, nil
}

func (operations *Phase5ExactGuestOperations) Provision(ctx context.Context, kind Phase5CapacityKind) error {
	if operations == nil || ctx == nil {
		return errors.New("invalid phase 5 provisioning")
	}
	operations.mu.Lock()
	defer operations.mu.Unlock()
	if operations.teardown || kind != Phase5FinalCapacity || operations.finalEngine != nil {
		if kind == Phase5AnalyzerCapacity || kind == Phase5ScratchCapacity {
			return ErrPhase5DerivedOperationsUnavailable
		}
		return errors.New("phase 5 final capacity already provisioned or closed")
	}
	engine, err := wazeroengine.New(ctx, operations.artifact, operations.config)
	if err != nil {
		return err
	}
	capacity, evidence, err := engine.PreparePreparedRegionFinal(ctx)
	if err != nil {
		_ = engine.Close(context.Background())
		return err
	}
	if !evidence.NeverServed || evidence.RuntimeInitCalls != 1 || evidence.ModuleInstantiations == 0 || evidence.BrokerAvailable || evidence.WorkspaceMounted || (runtime.GOOS == "linux" && !evidence.COWHit) || (runtime.GOOS != "linux" && !evidence.PreparedHit) {
		_ = capacity.Close(context.Background())
		_ = engine.Close(context.Background())
		return errors.New("phase 5 final capacity evidence drift")
	}
	operations.finalEngine = engine
	operations.finalCapacity = capacity
	operations.snapshot.FinalRuntimeInitCount = evidence.RuntimeInitCalls
	return nil
}

type phase5TimerGap struct {
	once sync.Once
	done <-chan time.Time
}

func (gap *phase5TimerGap) Wait(ctx context.Context) error {
	if gap == nil || ctx == nil {
		return errors.New("invalid phase 5 finalization gap")
	}
	var result error
	gap.once.Do(func() {
		select {
		case <-ctx.Done():
			result = context.Cause(ctx)
		case <-gap.done:
		}
	})
	return result
}

func (operations *Phase5ExactGuestOperations) BeginFinalizationGap(ctx context.Context, duration time.Duration) (Phase5FinalizationGap, error) {
	if operations == nil || ctx == nil || duration < 0 {
		return nil, errors.New("invalid phase 5 finalization gap")
	}
	return &phase5TimerGap{done: time.After(duration)}, nil
}

func (operations *Phase5ExactGuestOperations) Analyze(context.Context, Phase5ExecutionInput) error {
	return ErrPhase5DerivedOperationsUnavailable
}
func (operations *Phase5ExactGuestOperations) EmitPatch(context.Context, Phase5ExecutionInput) error {
	return ErrPhase5DerivedOperationsUnavailable
}
func (operations *Phase5ExactGuestOperations) ExecuteScratch(context.Context, Phase5ExecutionInput) error {
	return ErrPhase5DerivedOperationsUnavailable
}
func (operations *Phase5ExactGuestOperations) SealCapsule(context.Context) error {
	return ErrPhase5DerivedOperationsUnavailable
}
func (operations *Phase5ExactGuestOperations) ValidateSelection(context.Context, Phase5ExecutionInput) error {
	return ErrPhase5DerivedOperationsUnavailable
}
func (operations *Phase5ExactGuestOperations) CompileDerived(context.Context, Phase5ExecutionInput) error {
	return ErrPhase5DerivedOperationsUnavailable
}
func (operations *Phase5ExactGuestOperations) ExecuteDerived(context.Context, Phase5ExecutionInput) error {
	return ErrPhase5DerivedOperationsUnavailable
}

func (operations *Phase5ExactGuestOperations) ExecuteOriginal(ctx context.Context, input Phase5ExecutionInput) error {
	if operations == nil || ctx == nil || input.Source == "" || input.OutputName == "" {
		return errors.New("invalid phase 5 original execution")
	}
	operations.mu.Lock()
	defer operations.mu.Unlock()
	if operations.teardown || operations.finalCapacity == nil || len(operations.request) != 0 {
		return errors.New("phase 5 original execution capacity unavailable")
	}
	sourceDigest := sha256.Sum256([]byte(input.Source))
	runID := "p5-" + hex.EncodeToString(sourceDigest[:8])
	request, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{RunID: runID, Code: input.Source, Inputs: json.RawMessage(`{}`)})
	if err != nil {
		return err
	}
	payload, evidence, err := operations.finalCapacity.ExecuteOriginal(ctx, request)
	operations.request = append([]byte(nil), request...)
	if err != nil {
		return err
	}
	if !evidence.PreparedCapacity || evidence.ModuleInstantiations != 0 || evidence.RuntimeInitCalls != 0 || evidence.SourceValidations != 1 || evidence.FormalGuestExecutions != 1 || evidence.BrokerAvailable || evidence.WorkspaceMounted {
		return errors.New("phase 5 original execution evidence drift")
	}
	runRequest, err := runtimeconfig.DecodeRunRequest(request)
	if err != nil {
		return err
	}
	response, err := runtimeconfig.DecodeAndValidateRunResponse(runRequest, payload)
	if err != nil {
		return err
	}
	operations.snapshot.FormalGuestExecutions = evidence.FormalGuestExecutions
	operations.snapshot.ActualDisposition = "ready_consumed"
	if response.Status == runtimeconfig.RunResponseOK {
		operations.snapshot.ActualOutcome = "success"
		operations.snapshot.ResultSHA256 = phase5Digest(response.Result)
	} else {
		operations.snapshot.ActualOutcome = "error"
		if response.Error != nil {
			operations.snapshot.ErrorClass = response.Error.Code
			operations.snapshot.ErrorMessageSHA256 = phase5Digest([]byte(response.Error.Message))
			if response.Error.Traceback != nil {
				operations.snapshot.TracebackSHA256 = phase5Digest([]byte(*response.Error.Traceback))
			}
		}
	}
	logs, err := json.Marshal(response.Logs)
	if err != nil {
		return err
	}
	operations.snapshot.LogsSHA256 = phase5Digest(logs)
	return nil
}

func (operations *Phase5ExactGuestOperations) Teardown(ctx context.Context) error {
	if operations == nil || ctx == nil {
		return errors.New("invalid phase 5 teardown")
	}
	operations.mu.Lock()
	defer operations.mu.Unlock()
	if operations.teardown {
		return nil
	}
	operations.teardown = true
	var err error
	if operations.finalCapacity != nil {
		err = errors.Join(err, operations.finalCapacity.Close(ctx))
	}
	if operations.finalEngine != nil {
		err = errors.Join(err, operations.finalEngine.Close(ctx))
	}
	operations.snapshot.AuthorityTerminalDisposition = "none"
	operations.snapshot.WorkspaceTerminalDisposition = "unmounted"
	return err
}

func (operations *Phase5ExactGuestOperations) Snapshot() Phase5ExecutionSnapshot {
	if operations == nil {
		return Phase5ExecutionSnapshot{}
	}
	operations.mu.Lock()
	defer operations.mu.Unlock()
	return operations.snapshot
}

func phase5Digest(value []byte) string {
	digest := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(digest[:])
}

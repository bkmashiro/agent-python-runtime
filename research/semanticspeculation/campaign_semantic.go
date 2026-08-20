package semanticspeculation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	enginecontract "github.com/bkmashiro/agent-python-runtime/runtime/engine"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/playback"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

type SemanticPreDispatchTreatmentConfig struct {
	Artifact            []byte
	RunConfig           runtimeconfig.RunConfig
	Plan                *capability.Plan
	ImportClosureSHA256 string
	PhysicalReadBudget  uint32
	RunID               string
	WorkspaceRoot       string
	WorkspaceOwner      string
}

type semanticGenerationResult struct {
	generated semantic.GeneratedSource
	err       error
}

type semanticPreDispatchLauncher struct{}

func (semanticPreDispatchLauncher) Launch(run func()) { go run() }

type SemanticPreDispatchTreatment struct {
	config     SemanticPreDispatchTreatmentConfig
	inputs     json.RawMessage
	ctx        context.Context
	cancel     context.CancelFunc
	analyzer   *wazeroengine.Engine
	runner     enginecontract.Runner
	controller *semantic.StreamingSemanticPreDispatch
	admission  *semantic.StreamingPrefixAdmission
	manager    *workspace.Manager
	attempt    *workspace.Attempt
	broker     *capability.Broker
	source     strings.Builder
	chunks     chan string
	generated  chan semanticGenerationResult
	begun      bool
	finalized  bool
	once       sync.Once
}

func NewSemanticPreDispatchTreatment(config SemanticPreDispatchTreatmentConfig) (*SemanticPreDispatchTreatment, error) {
	if len(config.Artifact) == 0 || config.Plan == nil || config.RunConfig.ExecutionProfile == nil ||
		config.ImportClosureSHA256 == "" || config.PhysicalReadBudget == 0 || config.RunID == "" ||
		config.WorkspaceRoot == "" || config.WorkspaceOwner == "" {
		return nil, errors.New("invalid semantic pre-dispatch treatment")
	}
	return &SemanticPreDispatchTreatment{config: config}, nil
}

func (t *SemanticPreDispatchTreatment) Begin(ctx context.Context, inputs json.RawMessage) error {
	if t == nil || t.begun || ctx == nil || len(inputs) == 0 || !json.Valid(inputs) {
		return errors.New("invalid semantic pre-dispatch begin")
	}
	if err := os.MkdirAll(t.config.WorkspaceRoot, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(t.config.WorkspaceRoot, 0o700); err != nil {
		return err
	}
	runContext, cancel := context.WithCancel(ctx)
	t.ctx, t.cancel = runContext, cancel

	analyzerConfig := t.config.RunConfig
	analyzerConfig.Mechanisms = runtimeconfig.MechanismSet{SemanticAnalysis: true}
	analyzer, err := wazeroengine.New(runContext, t.config.Artifact, analyzerConfig)
	if err != nil {
		return err
	}
	t.analyzer = analyzer
	budget, err := semantic.NewPreDispatchBudget(t.config.PhysicalReadBudget)
	if err != nil {
		return t.failBegin(err)
	}
	controller, err := semantic.NewStreamingSemanticPreDispatch(t.config.Plan, budget, semanticPreDispatchLauncher{})
	if err != nil {
		return t.failBegin(err)
	}
	t.controller = controller
	admission, err := semantic.NewStreamingPrefixAdmission(t.config.Plan, controller, semantic.PreissueContext{
		StreamEpoch:         "phase3-" + t.config.RunID,
		WorkflowEpoch:       "phase3-workflow-" + t.config.RunID,
		FreshnessEpoch:      "phase3-freshness-v1",
		ExpiryEpoch:         "phase3-expiry-v1",
		PrivacyPartition:    "phase3-private-" + t.config.RunID,
		ParentLineageSHA256: digestCampaignText("phase3-parent-" + t.config.RunID),
	})
	if err != nil {
		return t.failBegin(err)
	}
	t.admission = admission

	manager, err := workspace.NewManager(t.config.WorkspaceRoot)
	if err != nil {
		return t.failBegin(err)
	}
	base, err := manager.Create(nil, workspace.DefaultLimits())
	if err != nil {
		_ = manager.Close()
		return t.failBegin(err)
	}
	attempt, err := manager.ForkAttempt(base)
	if err != nil {
		_ = manager.Close()
		return t.failBegin(err)
	}
	t.manager, t.attempt = manager, attempt
	executionConfig := t.config.RunConfig
	executionConfig.Mechanisms = runtimeconfig.MechanismSet{
		StagedObservation: true, PrivateWorkspace: true, SemanticAnalysis: true, SemanticPreDispatch: true,
	}
	runner, err := (wazeroengine.Factory{
		WorkspaceManager: manager, WorkspaceRef: attempt.Ref(), WorkspaceOwner: t.config.WorkspaceOwner,
		BrokerFactory: func(context.Context) (*capability.Broker, error) {
			broker, brokerErr := capability.NewBroker(capability.Config{
				RunIdentity: t.config.RunID, Plan: t.config.Plan, StagedClaimer: controller, SemanticPreDispatch: true,
			})
			if brokerErr == nil {
				t.broker = broker
			}
			return broker, brokerErr
		},
	}).New(runContext, t.config.Artifact, executionConfig)
	if err != nil {
		_ = attempt.Discard()
		_ = manager.Close()
		return t.failBegin(err)
	}
	t.runner = runner
	t.inputs = append(json.RawMessage(nil), inputs...)
	t.chunks = make(chan string, 32)
	t.generated = make(chan semanticGenerationResult, 1)
	artifactDigest := sha256.Sum256(t.config.Artifact)
	bindings := semantic.Bindings{
		ArtifactSHA256:         "sha256:" + hex.EncodeToString(artifactDigest[:]),
		ExecutionProfileSHA256: analyzer.Properties().ExecutionProfileBindingSHA256,
		ImportClosureSHA256:    t.config.ImportClosureSHA256,
		CapabilityPlanSHA256:   t.config.Plan.Identity(),
	}
	go func() {
		generated, generationErr := semantic.GenerateVerifiedSourceWithPreDispatch(runContext, semantic.VerifiedSourceGenerationConfig{
			Analyzer: analyzer, Plan: t.config.Plan, Bindings: bindings, Admission: admission, SourceChunks: t.chunks,
		})
		t.generated <- semanticGenerationResult{generated: generated, err: generationErr}
	}()
	t.begun = true
	return nil
}

func (t *SemanticPreDispatchTreatment) failBegin(err error) error {
	if t.analyzer != nil {
		_ = t.analyzer.Close(context.Background())
	}
	if t.cancel != nil {
		t.cancel()
	}
	return err
}

func (t *SemanticPreDispatchTreatment) ObserveChunk(ctx context.Context, chunk string) error {
	if t == nil || !t.begun || t.finalized || chunk == "" || ctx == nil {
		return errors.New("semantic pre-dispatch treatment not accepting chunks")
	}
	select {
	case t.chunks <- chunk:
		t.source.WriteString(chunk)
		return nil
	case result := <-t.generated:
		t.generated <- result
		if result.err != nil {
			return result.err
		}
		return errors.New("semantic source generation ended before final source")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *SemanticPreDispatchTreatment) Finalize(ctx context.Context) (TreatmentOutcome, error) {
	if t == nil || !t.begun || t.finalized || ctx == nil {
		return TreatmentOutcome{}, errors.New("semantic pre-dispatch treatment not ready to finalize")
	}
	t.finalized = true
	readyAtFinalize := t.controller.Snapshot().PhysicalFinishes
	close(t.chunks)
	generation := <-t.generated
	if generation.err != nil {
		_ = t.runner.Close(ctx)
		_ = t.attempt.Discard()
		_ = t.manager.Close()
		_ = t.analyzer.Close(ctx)
		if errors.Is(generation.err, runtimeconfig.ErrAgentSourceInvalid) || errors.Is(generation.err, semantic.ErrInvalidAnalysis) && t.exactSourceInvalid(ctx) {
			snapshot := t.controller.Snapshot()
			return TreatmentOutcome{
				FinalProgramOutcome: "syntax_error", ErrorClass: "syntax_error", AuthorityDisposition: "unchanged", WorkspaceDisposition: "discarded",
				PhysicalAttempts: snapshot.PhysicalIssues, PhysicalResultBytes: snapshot.PhysicalResultBytes, ProviderCostUnits: snapshot.ProviderCostUnits,
				ReadyBeforeFinalize: readyAtFinalize, PhysicalDispositions: controllerDispositions(snapshot),
			}, nil
		}
		return TreatmentOutcome{}, generation.err
	}
	request, err := json.Marshal(runtimeconfig.RunRequest{RunID: t.config.RunID, Code: generation.generated.Source(), Inputs: t.inputs})
	if err != nil {
		return TreatmentOutcome{}, err
	}
	execution, err := semantic.ExecuteGeneratedSourceOutcome(ctx, t.runner, t.attempt, request, t.config.Plan.StreamingPythonPrelude(), generation.generated)
	if err != nil {
		_ = t.manager.Close()
		_ = t.analyzer.Close(ctx)
		return TreatmentOutcome{}, err
	}
	defer t.manager.Close()
	defer t.analyzer.Close(ctx)
	decodedRequest, err := runtimeconfig.DecodeRunRequest(request)
	if err != nil {
		return TreatmentOutcome{}, err
	}
	response, err := runtimeconfig.DecodeAndValidateRunResponse(decodedRequest, execution.Response)
	if err != nil {
		return TreatmentOutcome{}, errors.New("invalid semantic pre-dispatch Guest response")
	}
	snapshot := t.controller.Snapshot()
	outcome := TreatmentOutcome{
		FinalPythonStarted: true, LogicalCalls: snapshot.LogicalClaims, PhysicalAttempts: snapshot.PhysicalIssues,
		PhysicalResultBytes: snapshot.PhysicalResultBytes, ProviderCostUnits: snapshot.ProviderCostUnits,
		ReadyBeforeFinalize: readyAtFinalize, PhysicalDispositions: controllerDispositions(snapshot),
		AuthorityDisposition: "unchanged", WorkspaceDisposition: execution.WorkspaceDisposition,
	}
	if outcome.LogicalCalls > 0 {
		outcome.AuthorityDisposition = "read_consumed"
	}
	switch response.Status {
	case runtimeconfig.RunResponseOK:
		if response.ResultPresent == nil || !*response.ResultPresent {
			return TreatmentOutcome{}, errors.New("semantic pre-dispatch success has no result")
		}
		var value any
		if json.Unmarshal(response.Result, &value) != nil {
			return TreatmentOutcome{}, errors.New("invalid semantic pre-dispatch result")
		}
		canonical, _ := json.Marshal(value)
		outcome.ResultSHA256, err = playback.CanonicalSHA256(canonical)
		if err != nil {
			return TreatmentOutcome{}, err
		}
		outcome.FinalProgramOutcome = "success"
	case runtimeconfig.RunResponseError:
		if response.Error == nil || response.Error.ErrorType == nil || *response.Error.ErrorType == "" {
			return TreatmentOutcome{}, errors.New("semantic pre-dispatch error has no error class")
		}
		outcome.FinalProgramOutcome = "runtime_error"
		outcome.ErrorClass = *response.Error.ErrorType
	default:
		return TreatmentOutcome{}, errors.New("invalid semantic pre-dispatch Guest status")
	}
	return outcome, nil
}

// exactSourceInvalid confirms an analyzer decode failure with the exact target
// Guest parser. The probe has no Broker or workspace mount, so valid source can
// perform no authority-bearing effect; only ErrAgentSourceInvalid is admitted as
// a syntax outcome.
func (t *SemanticPreDispatchTreatment) exactSourceInvalid(ctx context.Context) bool {
	config := t.config.RunConfig
	config.Mechanisms = runtimeconfig.MechanismSet{}
	probe, err := wazeroengine.New(ctx, t.config.Artifact, config)
	if err != nil {
		return false
	}
	defer probe.Close(ctx)
	request, err := json.Marshal(runtimeconfig.RunRequest{RunID: t.config.RunID + "-syntax-probe", Code: t.source.String(), Inputs: t.inputs})
	if err != nil {
		return false
	}
	_, err = probe.Run(ctx, request, "")
	return errors.Is(err, runtimeconfig.ErrAgentSourceInvalid)
}

func controllerDispositions(snapshot semantic.StreamingPreDispatchSnapshot) PhysicalDispositions {
	return PhysicalDispositions{Consumed: snapshot.Consumed, Orphaned: snapshot.Orphaned, Cancelled: snapshot.Cancelled, Failed: snapshot.Failed}
}

func (t *SemanticPreDispatchTreatment) Cancel(ctx context.Context) error {
	if t == nil {
		return nil
	}
	var closeErr error
	t.once.Do(func() {
		if t.cancel != nil {
			t.cancel()
		}
		if t.runner != nil {
			closeErr = t.runner.Close(ctx)
		}
		if t.attempt != nil {
			_ = t.attempt.Discard()
		}
		if t.manager != nil {
			_ = t.manager.Close()
		}
		if t.analyzer != nil {
			_ = t.analyzer.Close(ctx)
		}
	})
	return closeErr
}

func digestCampaignText(value string) string {
	digest := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(digest[:])
}

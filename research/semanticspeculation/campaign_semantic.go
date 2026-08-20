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
	"time"

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
	ProviderObservation func() ProviderObservation
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

const SemanticTreatmentLifecycleSchemaVersion = "pysolate.semantic-treatment-lifecycle.v4"

type SemanticTreatmentLifecycleEvidence struct {
	SchemaVersion         string                                         `json:"schema_version"`
	Analyzer              wazeroengine.SemanticAnalysisLifecycleEvidence `json:"analyzer"`
	AnalyzerPrepared      wazeroengine.PreparedState                     `json:"analyzer_prepared"`
	AnalyzerPreparedImage wazeroengine.PreparedImageState                `json:"analyzer_prepared_image"`
	BeginNanos            uint64                                         `json:"begin_nanos"`
	AnalyzerEngineNanos   uint64                                         `json:"analyzer_engine_nanos"`
	WorkspaceSetupNanos   uint64                                         `json:"workspace_setup_nanos"`
	FormalEngineNanos     uint64                                         `json:"formal_engine_nanos"`
	SourceGenerationNanos uint64                                         `json:"source_generation_nanos"`
	AdmissionNanos        uint64                                         `json:"admission_nanos"`
	ProviderNanos         uint64                                         `json:"provider_nanos"`
	VisiblePrefixes       uint32                                         `json:"visible_prefixes"`
	SkippedPrefixes       uint32                                         `json:"skipped_prefixes"`
	AnalyzerSessions      uint32                                         `json:"analyzer_sessions"`
	FormalGuestExecutions uint32                                         `json:"formal_guest_executions"`
	FormalExecutionNanos  uint64                                         `json:"formal_execution_nanos"`
}

type semanticPreDispatchLauncher struct{}

func (semanticPreDispatchLauncher) Launch(run func()) { go run() }

type SemanticPreDispatchTreatment struct {
	config                SemanticPreDispatchTreatmentConfig
	inputs                json.RawMessage
	ctx                   context.Context
	cancel                context.CancelFunc
	analyzer              *wazeroengine.Engine
	runner                enginecontract.Runner
	controller            *semantic.StreamingSemanticPreDispatch
	admission             *semantic.StreamingPrefixAdmission
	manager               *workspace.Manager
	attempt               *workspace.Attempt
	broker                *capability.Broker
	source                strings.Builder
	chunks                chan string
	generated             chan semanticGenerationResult
	lifecycleMu           sync.Mutex
	generationStarted     time.Time
	beginNanos            uint64
	analyzerEngineNanos   uint64
	workspaceSetupNanos   uint64
	formalEngineNanos     uint64
	sourceGenerationNanos uint64
	admissionNanos        uint64
	visiblePrefixes       uint32
	skippedPrefixes       uint32
	analyzerSessions      uint32
	formalGuestExecutions uint32
	formalExecutionNanos  uint64
	begun                 bool
	finalized             bool
	once                  sync.Once
}

func NewSemanticPreDispatchTreatment(config SemanticPreDispatchTreatmentConfig) (*SemanticPreDispatchTreatment, error) {
	if len(config.Artifact) == 0 || config.Plan == nil || config.RunConfig.ExecutionProfile == nil ||
		config.ImportClosureSHA256 == "" || config.PhysicalReadBudget == 0 || config.RunID == "" ||
		config.WorkspaceRoot == "" || config.WorkspaceOwner == "" {
		return nil, errors.New("invalid semantic pre-dispatch treatment")
	}
	return &SemanticPreDispatchTreatment{config: config}, nil
}

func (t *SemanticPreDispatchTreatment) Begin(ctx context.Context, inputs json.RawMessage) (beginErr error) {
	beginStarted := time.Now()
	defer func() {
		if t != nil {
			t.lifecycleMu.Lock()
			t.beginNanos = uint64(time.Since(beginStarted))
			t.lifecycleMu.Unlock()
		}
	}()
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
	analyzerConfig.Mechanisms = runtimeconfig.MechanismSet{
		SemanticAnalysis: true,
		PreparedRuntime:  t.config.RunConfig.Mechanisms.PreparedRuntime,
		MemoryCOW:        t.config.RunConfig.Mechanisms.MemoryCOW,
	}
	analyzerStarted := time.Now()
	analyzer, err := wazeroengine.New(runContext, t.config.Artifact, analyzerConfig)
	t.lifecycleMu.Lock()
	t.analyzerEngineNanos = uint64(time.Since(analyzerStarted))
	t.lifecycleMu.Unlock()
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

	workspaceStarted := time.Now()
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
	t.lifecycleMu.Lock()
	t.workspaceSetupNanos = uint64(time.Since(workspaceStarted))
	t.lifecycleMu.Unlock()
	executionConfig := t.config.RunConfig
	executionConfig.Mechanisms = runtimeconfig.MechanismSet{
		StagedObservation: true, PrivateWorkspace: true, SemanticAnalysis: true, SemanticPreDispatch: true,
	}
	formalEngineStarted := time.Now()
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
	t.lifecycleMu.Lock()
	t.formalEngineNanos = uint64(time.Since(formalEngineStarted))
	t.lifecycleMu.Unlock()
	if err != nil {
		_ = attempt.Discard()
		_ = manager.Close()
		return t.failBegin(err)
	}
	t.runner = runner
	t.inputs = append(json.RawMessage(nil), inputs...)
	t.chunks = make(chan string, 32)
	t.generated = make(chan semanticGenerationResult, 1)
	t.generationStarted = time.Now()
	artifactDigest := sha256.Sum256(t.config.Artifact)
	bindings := semantic.Bindings{
		ArtifactSHA256:         "sha256:" + hex.EncodeToString(artifactDigest[:]),
		ExecutionProfileSHA256: analyzer.Properties().ExecutionProfileBindingSHA256,
		ImportClosureSHA256:    t.config.ImportClosureSHA256,
		CapabilityPlanSHA256:   t.config.Plan.Identity(),
	}
	readiness, err := semantic.NewConservativePrefixReadinessFilter(t.config.Plan)
	if err != nil {
		return t.failBegin(err)
	}
	analyzerEngine := analyzer
	const analyzerSessionMaxRequests = uint32(32)
	session, err := analyzerEngine.NewSemanticAnalysisSession(runContext, wazeroengine.SemanticAnalysisSessionLimits{
		MaxRequests: analyzerSessionMaxRequests, MaxCumulativeRequestBytes: uint64(analyzerSessionMaxRequests) * semantic.MaxDocumentBytes,
		MaxDuration: t.config.RunConfig.Timeout,
	})
	if err != nil {
		return t.failBegin(err)
	}
	t.lifecycleMu.Lock()
	t.analyzerSessions++
	t.lifecycleMu.Unlock()
	go func() {
		generated, generationErr := semantic.GenerateVerifiedSourceWithPreDispatch(runContext, semantic.VerifiedSourceGenerationConfig{
			Plan: t.config.Plan, Bindings: bindings, Admission: admission, SourceChunks: t.chunks,
			ShouldAnalyzePrefix: readiness.ShouldAnalyzePrefix,
			Analyze: func(ctx context.Context, source string, bindings semantic.Bindings, plan *capability.Plan) (semantic.VerifiedAnalysis, error) {
				request, requestErr := semantic.NewRequest(source, bindings, plan)
				if requestErr != nil {
					return semantic.VerifiedAnalysis{}, requestErr
				}
				return semantic.AnalyzeVerifiedSession(ctx, session, request)
			},
			Observe: func(event semantic.VerifiedSourceGenerationEvent) {
				t.lifecycleMu.Lock()
				switch event.Phase {
				case "prefix_visible":
					t.visiblePrefixes++
				case "prefix_skipped":
					t.skippedPrefixes++
				case "prefix_admitted":
					t.admissionNanos += event.ElapsedNanos
				}
				t.lifecycleMu.Unlock()
			},
		})
		closeErr := session.Close(context.Background())
		t.generated <- semanticGenerationResult{generated: generated, err: errors.Join(generationErr, closeErr)}
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
	t.lifecycleMu.Lock()
	if !t.generationStarted.IsZero() && t.sourceGenerationNanos == 0 {
		t.sourceGenerationNanos = uint64(time.Since(t.generationStarted))
	}
	t.lifecycleMu.Unlock()
	if generation.err != nil {
		_ = t.runner.Close(ctx)
		_ = t.attempt.Discard()
		_ = t.manager.Close()
		_ = t.analyzer.Close(ctx)
		if errors.Is(generation.err, runtimeconfig.ErrAgentSourceInvalid) || errors.Is(generation.err, semantic.ErrInvalidAnalysis) && t.exactSourceInvalid(ctx) {
			return t.syntaxErrorOutcome(readyAtFinalize)
		}
		return TreatmentOutcome{}, generation.err
	}
	request, err := json.Marshal(runtimeconfig.RunRequest{RunID: t.config.RunID, Code: generation.generated.Source(), Inputs: t.inputs})
	if err != nil {
		return TreatmentOutcome{}, err
	}
	formalStarted := time.Now()
	execution, err := semantic.ExecuteGeneratedSourceOutcome(ctx, t.runner, t.attempt, request, t.config.Plan.StreamingPythonPrelude(), generation.generated)
	t.lifecycleMu.Lock()
	t.formalGuestExecutions++
	t.formalExecutionNanos += uint64(time.Since(formalStarted))
	t.lifecycleMu.Unlock()
	if err != nil {
		_ = t.runner.Close(ctx)
		_ = t.attempt.Discard()
		_ = t.manager.Close()
		_ = t.analyzer.Close(ctx)
		if errors.Is(err, runtimeconfig.ErrAgentSourceInvalid) {
			return t.syntaxErrorOutcome(readyAtFinalize)
		}
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
	providerAttempts, providerBytes, providerCost, dispositions, liveFallbackCalls, err := t.providerOutcome(snapshot)
	if err != nil {
		return TreatmentOutcome{}, err
	}
	outcome := TreatmentOutcome{
		FinalPythonStarted: true, LogicalCalls: snapshot.LogicalClaims + liveFallbackCalls, PhysicalAttempts: providerAttempts,
		PhysicalResultBytes: providerBytes, ProviderCostUnits: providerCost,
		ReadyBeforeFinalize: readyAtFinalize, PhysicalDispositions: dispositions,
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

func (t *SemanticPreDispatchTreatment) LifecycleEvidence() SemanticTreatmentLifecycleEvidence {
	result := SemanticTreatmentLifecycleEvidence{SchemaVersion: SemanticTreatmentLifecycleSchemaVersion}
	if t == nil {
		return result
	}
	if t.analyzer != nil {
		result.Analyzer = t.analyzer.SemanticAnalysisLifecycleEvidence()
		result.AnalyzerPrepared = t.analyzer.PreparedState()
		result.AnalyzerPreparedImage = t.analyzer.PreparedImageState()
	} else {
		result.Analyzer = wazeroengine.SemanticAnalysisLifecycleEvidence{SchemaVersion: wazeroengine.SemanticAnalysisLifecycleSchemaVersion}
	}
	providerNanos := uint64(0)
	if t.config.ProviderObservation != nil {
		providerNanos = t.config.ProviderObservation().ElapsedNanos
	}
	t.lifecycleMu.Lock()
	result.BeginNanos = t.beginNanos
	result.AnalyzerEngineNanos = t.analyzerEngineNanos
	result.WorkspaceSetupNanos = t.workspaceSetupNanos
	result.FormalEngineNanos = t.formalEngineNanos
	result.SourceGenerationNanos = t.sourceGenerationNanos
	result.AdmissionNanos = t.admissionNanos
	result.ProviderNanos = providerNanos
	result.VisiblePrefixes = t.visiblePrefixes
	result.SkippedPrefixes = t.skippedPrefixes
	result.AnalyzerSessions = t.analyzerSessions
	result.FormalGuestExecutions = t.formalGuestExecutions
	result.FormalExecutionNanos = t.formalExecutionNanos
	t.lifecycleMu.Unlock()
	return result
}

func (t *SemanticPreDispatchTreatment) providerOutcome(snapshot semantic.StreamingPreDispatchSnapshot) (uint32, uint64, uint64, PhysicalDispositions, uint32, error) {
	dispositions := controllerDispositions(snapshot)
	if t.config.ProviderObservation == nil {
		return snapshot.PhysicalIssues, snapshot.PhysicalResultBytes, snapshot.ProviderCostUnits, dispositions, 0, nil
	}
	provider := t.config.ProviderObservation()
	if provider.Attempts < snapshot.PhysicalIssues || provider.ResultBytes < snapshot.PhysicalResultBytes || provider.CostUnits < snapshot.ProviderCostUnits {
		return 0, 0, 0, PhysicalDispositions{}, 0, errors.New("provider observation is behind semantic pre-dispatch controller")
	}
	// Attempts not issued by the pre-dispatch controller are ordinary live
	// fallback calls consumed by the original logical call.
	liveFallbackCalls := provider.Attempts - snapshot.PhysicalIssues
	dispositions.Consumed += liveFallbackCalls
	return provider.Attempts, provider.ResultBytes, provider.CostUnits, dispositions, liveFallbackCalls, nil
}

func (t *SemanticPreDispatchTreatment) syntaxErrorOutcome(readyAtFinalize uint32) (TreatmentOutcome, error) {
	snapshot := t.controller.Snapshot()
	providerAttempts, providerBytes, providerCost, dispositions, _, err := t.providerOutcome(snapshot)
	if err != nil {
		return TreatmentOutcome{}, err
	}
	return TreatmentOutcome{
		FinalProgramOutcome: "syntax_error", ErrorClass: "syntax_error", AuthorityDisposition: "unchanged", WorkspaceDisposition: "discarded",
		PhysicalAttempts: providerAttempts, PhysicalResultBytes: providerBytes, ProviderCostUnits: providerCost,
		ReadyBeforeFinalize: readyAtFinalize, PhysicalDispositions: dispositions,
	}, nil
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

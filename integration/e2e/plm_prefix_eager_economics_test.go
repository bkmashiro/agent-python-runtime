package e2e_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/research/semanticspeculation"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/agentfunction"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/passplugin"
	"github.com/bkmashiro/agent-python-runtime/runtime/passregistration"
	"github.com/bkmashiro/agent-python-runtime/runtime/playback"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
	"github.com/bkmashiro/agent-python-runtime/runtime/sourcepatch"
	"github.com/bkmashiro/agent-python-runtime/runtime/workspace"
)

const (
	plmPrefixEagerSchema        = "pysolate.plm-prefix-eager-economics.v7"
	plmPrefixEagerProviderDelay = 1500 * time.Millisecond
)

type plmPrefixEagerChunk struct {
	offset time.Duration
	source string
}

type plmPrefixEagerCell struct {
	id                  string
	chunks              []plmPrefixEagerChunk
	providerResponses   map[string]string
	providerDelay       time.Duration
	expectedCalls       uint32
	expectedPrefixCalls uint32
	expectedResult      json.RawMessage
	includeImmediate    bool
}

var plmPrefixEagerCells = map[string]plmPrefixEagerCell{
	"one-read-gate-eligible-long-tail": {
		id: "one-read-gate-eligible-long-tail",
		chunks: []plmPrefixEagerChunk{
			{0, "value = sources.read(\"alpha\")\n"},
			{700 * time.Millisecond, "label = value.upper()\n"},
			{1400 * time.Millisecond, "result = [label, 12]\nprint(result)\n"},
		},
		providerResponses: map[string]string{"alpha": "alpha"},
		expectedCalls:     1, expectedPrefixCalls: 1, expectedResult: json.RawMessage(`["ALPHA",12]`), includeImmediate: true,
	},
	"one-read-gate-rejected-long-tail": {
		id: "one-read-gate-rejected-long-tail",
		chunks: []plmPrefixEagerChunk{
			{0, "value = sources.read(\"alpha\")\n"},
			{700 * time.Millisecond, "label = value.upper()\n"},
			{1400 * time.Millisecond, "result = [label, 12]\nprint(result)\n"},
		},
		providerResponses: map[string]string{"alpha": "alpha"},
		expectedCalls:     1, expectedPrefixCalls: 1, expectedResult: json.RawMessage(`["ALPHA",12]`), includeImmediate: true,
	},
	"two-read-gate-rejected-long-tail": {
		id: "two-read-gate-rejected-long-tail",
		chunks: []plmPrefixEagerChunk{
			{0, "left = sources.read(\"alpha\")\nright = sources.read(\"beta\")\n"},
			{700 * time.Millisecond, "left_label = left.upper()\nright_label = right.upper()\n"},
			{1400 * time.Millisecond, "result = [left_label, right_label, 12]\nprint(result)\n"},
		},
		providerResponses: map[string]string{"alpha": "alpha", "beta": "beta"},
		expectedCalls:     2, expectedPrefixCalls: 2, expectedResult: json.RawMessage(`["ALPHA","BETA",12]`),
	},
	"two-read-dependent-long-tail": {
		id: "two-read-dependent-long-tail",
		chunks: []plmPrefixEagerChunk{
			{0, "left = sources.read(\"alpha\")\nright = sources.read(left)\n"},
			{700 * time.Millisecond, "left_label = left.upper()\nright_label = right.upper()\n"},
			{1400 * time.Millisecond, "result = [left_label, right_label, 12]\nprint(result)\n"},
		},
		providerResponses: map[string]string{"alpha": "beta", "beta": "gamma"},
		expectedCalls:     2, expectedPrefixCalls: 1, expectedResult: json.RawMessage(`["BETA","GAMMA",12]`),
	},
	"one-read-gate-eligible-low-delay": {
		id: "one-read-gate-eligible-low-delay",
		chunks: []plmPrefixEagerChunk{
			{0, "value = sources.read(\"alpha\")\n"},
			{700 * time.Millisecond, "label = value.upper()\n"},
			{1400 * time.Millisecond, "result = [label, 12]\nprint(result)\n"},
		},
		providerResponses: map[string]string{"alpha": "alpha"}, providerDelay: 25 * time.Millisecond,
		expectedCalls: 1, expectedPrefixCalls: 1, expectedResult: json.RawMessage(`["ALPHA",12]`), includeImmediate: true,
	},
	"one-read-gate-eligible-short-tail": {
		id: "one-read-gate-eligible-short-tail",
		chunks: []plmPrefixEagerChunk{
			{0, "value = sources.read(\"alpha\")\n"},
			{25 * time.Millisecond, "label = value.upper()\n"},
			{50 * time.Millisecond, "result = [label, 12]\nprint(result)\n"},
		},
		providerResponses: map[string]string{"alpha": "alpha"}, providerDelay: 1500 * time.Millisecond,
		expectedCalls: 1, expectedPrefixCalls: 1, expectedResult: json.RawMessage(`["ALPHA",12]`), includeImmediate: true,
	},
	"compute-heavy-eager-favourable": {
		id: "compute-heavy-eager-favourable",
		chunks: []plmPrefixEagerChunk{
			{0, "acc = sum((i * 17) % 97 for i in range(20000000)) % 1000000007\n"},
			{1400 * time.Millisecond, "result = acc\nprint(result)\n"},
		},
		providerResponses: map[string]string{},
		expectedResult:    json.RawMessage(`959999907`),
	},
}

func TestPLMPrefixEagerCellsDefineLowDelayAndShortTail(t *testing.T) {
	lowDelay := plmPrefixEagerCells["one-read-gate-eligible-low-delay"]
	if lowDelay.providerDelay != 25*time.Millisecond || lowDelay.chunks[len(lowDelay.chunks)-1].offset != 1400*time.Millisecond {
		t.Fatalf("unexpected low-delay cell: %+v", lowDelay)
	}
	shortTail := plmPrefixEagerCells["one-read-gate-eligible-short-tail"]
	if shortTail.providerDelay != 1500*time.Millisecond || shortTail.chunks[len(shortTail.chunks)-1].offset != 50*time.Millisecond {
		t.Fatalf("unexpected short-tail cell: %+v", shortTail)
	}
}

func TestPLMPrefixEagerCellsDefineComputeHeavyCase(t *testing.T) {
	cell, ok := plmPrefixEagerCells["compute-heavy-eager-favourable"]
	if !ok {
		t.Fatal("compute-heavy cell is missing")
	}
	if cell.expectedCalls != 0 || cell.expectedPrefixCalls != 0 || cell.includeImmediate || cell.providerDelayDuration() != 0 {
		t.Fatalf("unexpected compute-heavy cell: %+v", cell)
	}
	if len(cell.chunks) != 2 || cell.chunks[0].offset != 0 || cell.chunks[1].offset != 1400*time.Millisecond {
		t.Fatalf("unexpected compute-heavy schedule: %+v", cell.chunks)
	}
}

func plmPrefixEagerCellFromEnv(t *testing.T) plmPrefixEagerCell {
	t.Helper()
	id := os.Getenv("PYSOLATE_PLM_PREFIX_EAGER_CELL")
	if id == "" {
		id = "one-read-gate-rejected-long-tail"
	}
	cell, ok := plmPrefixEagerCells[id]
	if !ok {
		t.Fatalf("unknown PLM prefix EAGER cell %q", id)
	}
	return cell
}

func (cell plmPrefixEagerCell) sourceText() string {
	var source strings.Builder
	for _, chunk := range cell.chunks {
		source.WriteString(chunk.source)
	}
	return source.String()
}

func (cell plmPrefixEagerCell) providerDelayDuration() time.Duration {
	if cell.expectedCalls == 0 {
		return 0
	}
	if cell.providerDelay != 0 {
		return cell.providerDelay
	}
	return plmPrefixEagerProviderDelay
}

func (cell plmPrefixEagerCell) chunkOffsetsMS() []int {
	offsets := make([]int, 0, len(cell.chunks))
	for _, chunk := range cell.chunks {
		offsets = append(offsets, int(chunk.offset/time.Millisecond))
	}
	return offsets
}

type plmPrefixEagerTracker struct {
	attempts      atomic.Uint32
	ready         atomic.Uint32
	active        atomic.Uint32
	maxConcurrent atomic.Uint32
	resultBytes   atomic.Uint64
	providerNanos atomic.Uint64
	finalized     atomic.Bool
	responses     map[string]string
	delay         time.Duration
}

func (tracker *plmPrefixEagerTracker) handler(ctx context.Context, arguments json.RawMessage) (json.RawMessage, error) {
	var request struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(arguments, &request); err != nil {
		return nil, fmt.Errorf("decode source path: %w", err)
	}
	if request.Path == "" {
		return nil, fmt.Errorf("source path is required")
	}
	body, ok := tracker.responses[request.Path]
	if !ok {
		return nil, fmt.Errorf("unknown source path %q", request.Path)
	}
	started := time.Now()
	defer func() { tracker.providerNanos.Add(uint64(time.Since(started))) }()
	tracker.attempts.Add(1)
	active := tracker.active.Add(1)
	for {
		maximum := tracker.maxConcurrent.Load()
		if active <= maximum || tracker.maxConcurrent.CompareAndSwap(maximum, active) {
			break
		}
	}
	defer tracker.active.Add(^uint32(0))
	timer := time.NewTimer(tracker.delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
	}
	if !tracker.finalized.Load() {
		tracker.ready.Add(1)
	}
	result, err := json.Marshal(map[string]string{"body": body})
	if err == nil {
		tracker.resultBytes.Add(uint64(len(result)))
	}
	return result, err
}

func (tracker *plmPrefixEagerTracker) observation() semanticspeculation.ProviderObservation {
	attempts := tracker.attempts.Load()
	return semanticspeculation.ProviderObservation{
		Attempts: attempts, ResultBytes: tracker.resultBytes.Load(), CostUnits: uint64(attempts),
		ReadyBeforeFinalize: tracker.ready.Load(),
		Dispositions:        semanticspeculation.PhysicalDispositions{Consumed: attempts},
	}
}

type plmPrefixEagerSample struct {
	Trial                       int                                                   `json:"trial"`
	Order                       int                                                   `json:"order"`
	Treatment                   string                                                `json:"treatment"`
	ColdNanos                   uint64                                                `json:"cold_nanos"`
	PostBeginNanos              uint64                                                `json:"post_begin_nanos"`
	BeginNanos                  uint64                                                `json:"begin_nanos"`
	FinalizeNanos               uint64                                                `json:"finalize_nanos"`
	Outcome                     semanticspeculation.TreatmentOutcome                  `json:"outcome"`
	SplitPhase                  capability.SplitPhaseSnapshot                         `json:"split_phase,omitempty"`
	PLMLifecycle                wazeroengine.PLMRunLifecycleEvidence                  `json:"plm_lifecycle,omitempty"`
	PrefixAnalysisNanos         uint64                                                `json:"prefix_analysis_nanos,omitempty"`
	PrefixAdmissionNanos        uint64                                                `json:"prefix_admission_nanos,omitempty"`
	ProviderNanos               uint64                                                `json:"provider_nanos,omitempty"`
	ProviderMaxConcurrent       uint32                                                `json:"provider_max_concurrent,omitempty"`
	FinalExecutionNanos         uint64                                                `json:"final_execution_nanos,omitempty"`
	PrefixAnalyzerInvocations   uint32                                                `json:"prefix_analyzer_invocations,omitempty"`
	AnalyzerProvision           wazeroengine.SemanticAnalysisSessionProvisionEvidence `json:"analyzer_provision,omitempty"`
	AnalyzerSessionPrepareNanos uint64                                                `json:"analyzer_session_prepare_nanos,omitempty"`
}

type plmPrefixEagerEvidence struct {
	SchemaVersion                string                                         `json:"schema_version"`
	CellID                       string                                         `json:"cell_id"`
	SourceCommit                 string                                         `json:"source_commit"`
	SourceTree                   string                                         `json:"source_tree"`
	HostID                       string                                         `json:"host_id"`
	ArtifactSHA256               string                                         `json:"artifact_sha256"`
	Runs                         int                                            `json:"runs"`
	ProviderDelayMS              int                                            `json:"provider_delay_ms"`
	ChunkOffsetsMS               []int                                          `json:"chunk_offsets_ms"`
	SourceSHA256                 string                                         `json:"source_sha256"`
	EagerEstimateScope           string                                         `json:"eager_estimate_scope"`
	AnalyzerCapacitySetupNanos   uint64                                         `json:"analyzer_capacity_setup_nanos"`
	AnalyzerCapacitySessionCount uint32                                         `json:"analyzer_capacity_session_count"`
	AnalyzerCapacityLifecycle    wazeroengine.SemanticAnalysisLifecycleEvidence `json:"analyzer_capacity_lifecycle"`
	Samples                      []plmPrefixEagerSample                         `json:"samples"`
}

type plmPrefixAnalysisResult struct {
	analysisNanos  uint64
	admissionNanos uint64
	err            error
}

type plmPrefixPreparedCapacity struct {
	artifact       []byte
	config         runtimeconfig.RunConfig
	profile        runtimeconfig.ExecutionProfile
	allowedImports []string
	plugins        *passplugin.Registry
	analyzer       *wazeroengine.Engine
	setupNanos     uint64
	sessions       atomic.Uint32
}

func newPLMPrefixPreparedCapacity(ctx context.Context, artifact []byte, config runtimeconfig.RunConfig) (*plmPrefixPreparedCapacity, error) {
	return newPLMPrefixPreparedCapacityForProfile(ctx, artifact, config, "base", []string{"json"})
}

func newPLMPrefixPreparedCapacityForProfile(ctx context.Context, artifact []byte, config runtimeconfig.RunConfig, profileID string, allowedImports []string) (*plmPrefixPreparedCapacity, error) {
	started := time.Now()
	artifactDigest := sha256.Sum256(artifact)
	artifactSHA := fmt.Sprintf("sha256:%x", artifactDigest[:])
	profile, err := runtimeconfig.NewExecutionProfile(profileID, allowedImports)
	if err != nil {
		return nil, err
	}
	profile, err = profile.BindVerifiedArtifact(runtimeconfig.VerifiedArtifactIdentity{
		ProfileID: profileID, ArtifactSHA256: artifactSHA, ManifestSHA256: testDigest("plm-prefix-eager-manifest"),
		ImportRoots: allowedImports, QualifiedImportRoots: allowedImports,
	})
	if err != nil {
		return nil, err
	}
	plugins := unifiedPassCatalogForExperiment()
	plugins, err = plugins.Enable(passregistration.SemanticPreDispatch, sourcepatch.PLMCapabilityCallsName)
	if err != nil {
		return nil, err
	}
	analysisConfig := config
	analysisConfig.ExecutionProfile = &profile
	analysisConfig.Mechanisms = runtimeconfig.MechanismSet{SemanticAnalysis: true}
	preparedPass := passregistration.PreparedRuntimeInstantiation
	if goruntime.GOOS == "linux" {
		preparedPass = passregistration.PrivateMemoryCOW
	}
	analysisConfig, _, err = passplugin.LowerDefaultRunConfig(analysisConfig, preparedPass)
	if err != nil {
		return nil, err
	}
	analyzer, err := wazeroengine.New(ctx, artifact, analysisConfig)
	if err != nil {
		return nil, err
	}
	if err := analyzer.PrepareSemanticRuntime(ctx); err != nil {
		_ = analyzer.Close(context.Background())
		return nil, err
	}
	return &plmPrefixPreparedCapacity{
		artifact: artifact, config: config, profile: profile, allowedImports: append([]string(nil), allowedImports...), plugins: plugins, analyzer: analyzer,
		setupNanos: uint64(time.Since(started)),
	}, nil
}

func (capacity *plmPrefixPreparedCapacity) NewSession(ctx context.Context, maxRequests uint32) (*wazeroengine.SemanticAnalysisSession, wazeroengine.SemanticAnalysisSessionProvisionEvidence, uint64, error) {
	started := time.Now()
	session, err := capacity.analyzer.NewSemanticAnalysisSession(ctx, wazeroengine.SemanticAnalysisSessionLimits{
		MaxRequests: maxRequests, MaxCumulativeRequestBytes: semantic.MaxDocumentBytes, MaxDuration: 30 * time.Second,
	})
	if err != nil {
		return nil, wazeroengine.SemanticAnalysisSessionProvisionEvidence{}, 0, err
	}
	provision, err := session.Prepare(ctx)
	if err != nil {
		_ = session.Close(context.Background())
		return nil, provision, 0, err
	}
	capacity.sessions.Add(1)
	return session, provision, uint64(time.Since(started)), nil
}

func (capacity *plmPrefixPreparedCapacity) SetupNanos() uint64 {
	return capacity.setupNanos
}

func (capacity *plmPrefixPreparedCapacity) SessionCount() uint32 {
	return capacity.sessions.Load()
}

func (capacity *plmPrefixPreparedCapacity) LifecycleEvidence() wazeroengine.SemanticAnalysisLifecycleEvidence {
	return capacity.analyzer.SemanticAnalysisLifecycleEvidence()
}

func (capacity *plmPrefixPreparedCapacity) Close(ctx context.Context) error {
	return capacity.analyzer.Close(ctx)
}

type plmPrefixTreatment struct {
	capacity                    *plmPrefixPreparedCapacity
	artifact                    []byte
	config                      runtimeconfig.RunConfig
	plan                        *capability.Plan
	adapter                     *e2ePLMAdapter
	tracker                     *plmPrefixEagerTracker
	expectedCalls               uint32
	expectedPrefixCalls         uint32
	runID                       string
	workspaceRoot               string
	source                      strings.Builder
	analyzerSession             *wazeroengine.SemanticAnalysisSession
	finalRunner                 *wazeroengine.Engine
	broker                      *capability.Broker
	table                       *capability.SplitPhaseTable
	admission                   *semantic.StreamingPrefixAdmission
	bindings                    semantic.Bindings
	plugins                     *passplugin.Registry
	manager                     *workspace.Manager
	attempt                     *workspace.Attempt
	prefixAnalysisNanos         uint64
	prefixAdmissionNanos        uint64
	prefixAnalyzerInvocations   uint32
	prefixIndex                 uint32
	analysisResult              chan plmPrefixAnalysisResult
	analyzerProvision           wazeroengine.SemanticAnalysisSessionProvisionEvidence
	analyzerSessionPrepareNanos uint64
	finalExecutionNanos         uint64

	split     capability.SplitPhaseSnapshot
	lifecycle wazeroengine.PLMRunLifecycleEvidence
}

func newPLMPrefixTreatment(capacity *plmPrefixPreparedCapacity, plan *capability.Plan, adapter *e2ePLMAdapter, tracker *plmPrefixEagerTracker, expectedCalls, expectedPrefixCalls uint32, runID, workspaceRoot string) *plmPrefixTreatment {
	return &plmPrefixTreatment{capacity: capacity, artifact: capacity.artifact, config: capacity.config, plan: plan, adapter: adapter, tracker: tracker, expectedCalls: expectedCalls, expectedPrefixCalls: expectedPrefixCalls, runID: runID, workspaceRoot: workspaceRoot}
}

func (treatment *plmPrefixTreatment) Begin(ctx context.Context, _ json.RawMessage) error {
	if err := os.MkdirAll(treatment.workspaceRoot, 0o700); err != nil {
		return err
	}
	manager, err := workspace.NewManager(treatment.workspaceRoot)
	if err != nil {
		return err
	}
	base, err := manager.Create(nil, workspace.DefaultLimits())
	if err != nil {
		return err
	}
	attempt, err := manager.ForkAttempt(base)
	if err != nil {
		return err
	}
	treatment.manager, treatment.attempt = manager, attempt
	artifactDigest := sha256.Sum256(treatment.artifact)
	artifactSHA := fmt.Sprintf("sha256:%x", artifactDigest[:])
	allowedImports := treatment.capacity.allowedImports
	treatment.plugins = treatment.capacity.plugins
	callBudget := treatment.expectedCalls
	if callBudget == 0 {
		callBudget = 1
	}
	session, provision, prepareNanos, err := treatment.capacity.NewSession(ctx, callBudget)
	if err != nil {
		return err
	}
	treatment.analyzerSession = session
	treatment.analyzerProvision = provision
	treatment.analyzerSessionPrepareNanos = prepareNanos

	broker, err := capability.NewBroker(capability.Config{RunIdentity: treatment.runID, Plan: treatment.plan})
	if err != nil {
		return err
	}
	treatment.broker = broker
	table, err := capability.NewSplitPhaseTable(broker, capability.SplitPhaseLimits{MaxCalls: callBudget, MaxCostUnits: uint64(callBudget), MaxResultBytes: uint64(callBudget) * 4096})
	if err != nil {
		return err
	}
	treatment.table = table
	admission, err := semantic.NewStreamingPLMPrefixAdmission(treatment.plan, table, semantic.PreissueContext{
		StreamEpoch: treatment.runID + "-stream", WorkflowEpoch: treatment.runID + "-workflow", FreshnessEpoch: treatment.runID + "-fresh",
		ExpiryEpoch: treatment.runID + "-expiry", PrivacyPartition: treatment.runID + "-private", ParentLineageSHA256: testDigest(treatment.runID + "-parent"),
	})
	if err != nil {
		return err
	}
	treatment.admission = admission
	treatment.bindings = semantic.Bindings{
		ArtifactSHA256: artifactSHA, ExecutionProfileSHA256: treatment.capacity.analyzer.Properties().ExecutionProfileBindingSHA256,
		ImportClosureSHA256: agentfunction.ImportClosureIdentity(allowedImports, allowedImports), CapabilityPlanSHA256: treatment.plan.Identity(),
	}

	finalConfig := treatment.config
	finalConfig.ExecutionProfile = &treatment.capacity.profile
	finalRunner, err := (wazeroengine.Factory{Passes: treatment.plugins, WorkspaceManager: treatment.manager, WorkspaceRef: treatment.attempt.Ref(), WorkspaceOwner: treatment.runID, BrokerFactory: func(context.Context) (*capability.Broker, error) { return treatment.broker, nil }}).New(ctx, treatment.artifact, finalConfig)
	if err != nil {
		return fmt.Errorf("create final PLM runner: %w", err)
	}
	finalEngine := trustedSemanticRunnerNoTest(finalRunner)
	if finalEngine == nil {
		return fmt.Errorf("PLM experiment requires wazero engine")
	}
	treatment.finalRunner = finalEngine
	return nil
}

func (treatment *plmPrefixTreatment) ObserveChunk(ctx context.Context, chunk string) error {
	treatment.source.WriteString(chunk)
	treatment.prefixIndex++
	prefix := treatment.source.String()
	if treatment.prefixIndex != 1 {
		return treatment.admission.RecordSkippedPrefix(prefix)
	}
	if treatment.analysisResult != nil {
		return fmt.Errorf("experiment fixture supports one prefix analysis")
	}
	result := make(chan plmPrefixAnalysisResult, 1)
	treatment.analysisResult = result
	go func() {
		request, err := semantic.NewRequest(prefix, treatment.bindings, treatment.plan)
		analysisNanos := uint64(0)
		admissionNanos := uint64(0)
		if err != nil {
			err = fmt.Errorf("build prefix request: %w", err)
		} else {
			var verified semantic.VerifiedAnalysis
			analysisStarted := time.Now()
			verified, err = semantic.AnalyzeVerifiedSession(ctx, treatment.analyzerSession, request)
			analysisNanos = uint64(time.Since(analysisStarted))
			if err != nil {
				err = fmt.Errorf("analyze prefix: %w", err)
			} else {
				added := uint32(0)
				admissionStarted := time.Now()
				added, err = treatment.admission.AdmitVerifiedPrefix(ctx, prefix, verified)
				admissionNanos = uint64(time.Since(admissionStarted))
				if err != nil {
					err = fmt.Errorf("admit prefix: %w", err)
				} else if added != treatment.expectedPrefixCalls {
					err = fmt.Errorf("prefix admission added %d calls", added)
				}
			}
		}
		result <- plmPrefixAnalysisResult{analysisNanos: analysisNanos, admissionNanos: admissionNanos, err: err}
	}()
	return nil
}

func (treatment *plmPrefixTreatment) Finalize(ctx context.Context) (semanticspeculation.TreatmentOutcome, error) {
	if treatment.analysisResult != nil {
		select {
		case analyzed := <-treatment.analysisResult:
			treatment.prefixAnalysisNanos = analyzed.analysisNanos
			treatment.prefixAdmissionNanos = analyzed.admissionNanos
			treatment.prefixAnalyzerInvocations = 1
			if analyzed.err != nil {
				return semanticspeculation.TreatmentOutcome{}, analyzed.err
			}
		case <-ctx.Done():
			return semanticspeculation.TreatmentOutcome{}, ctx.Err()
		}
	}
	fullSource := treatment.source.String()
	if err := treatment.admission.SealFinalSource(fullSource); err != nil {
		return semanticspeculation.TreatmentOutcome{}, fmt.Errorf("seal final source: %w", err)
	}
	request, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{RunID: treatment.runID, Code: fullSource, Inputs: json.RawMessage(`{}`)})
	if err != nil {
		return semanticspeculation.TreatmentOutcome{}, err
	}
	engine := treatment.finalRunner
	if engine == nil {
		return semanticspeculation.TreatmentOutcome{}, fmt.Errorf("PLM experiment final runner was not initialized")
	}
	executionStarted := time.Now()
	execution, err := treatment.plugins.ExecuteCapabilityHostScheduled(ctx, sourcepatch.PLMCapabilityCallsName, engine, request, treatment.plan.PythonPrelude(), passplugin.PLMCapabilityProjections(treatment.plan))
	treatment.finalExecutionNanos = uint64(time.Since(executionStarted))
	closeErr := engine.Close(ctx)
	sessionCloseErr := treatment.analyzerSession.Close(ctx)

	if err != nil {
		return semanticspeculation.TreatmentOutcome{}, fmt.Errorf("execute final PLM source: %w", err)
	}
	if closeErr != nil {
		return semanticspeculation.TreatmentOutcome{}, closeErr
	}
	if sessionCloseErr != nil {
		return semanticspeculation.TreatmentOutcome{}, sessionCloseErr
	}

	result, err := decodeSuccessfulGuestResult(execution.Payload)
	if err != nil {
		return semanticspeculation.TreatmentOutcome{}, err
	}
	resultSHA, err := playback.CanonicalSHA256(result)
	if err != nil {
		return semanticspeculation.TreatmentOutcome{}, err
	}
	treatment.split = engine.SplitPhaseEvidence()
	treatment.lifecycle = engine.PLMRunLifecycleEvidence()
	if _, err := treatment.attempt.Publish(); err != nil {
		return semanticspeculation.TreatmentOutcome{}, err
	}
	if err := treatment.manager.Close(); err != nil {
		return semanticspeculation.TreatmentOutcome{}, err
	}
	return semanticspeculation.TreatmentOutcome{
		FinalProgramOutcome: "success", FinalPythonStarted: true, ResultSHA256: resultSHA,
		LogicalCalls: treatment.broker.CallCount(), PhysicalAttempts: treatment.tracker.attempts.Load(), ReadyBeforeFinalize: treatment.tracker.ready.Load(),
		PhysicalDispositions: semanticspeculation.PhysicalDispositions{Consumed: treatment.tracker.attempts.Load()},
		AuthorityDisposition: "read_consumed", WorkspaceDisposition: "published",
	}, nil
}

func (treatment *plmPrefixTreatment) Cancel(ctx context.Context) error {
	if treatment.finalRunner != nil {
		_ = treatment.finalRunner.Close(ctx)
	}
	if treatment.analyzerSession != nil {
		_ = treatment.analyzerSession.Close(ctx)
	}

	if treatment.attempt != nil {
		_ = treatment.attempt.Discard()
	}
	if treatment.manager != nil {
		_ = treatment.manager.Close()
	}
	return nil
}

func trustedSemanticRunnerNoTest(runner interface{ Close(context.Context) error }) *wazeroengine.Engine {
	engine, _ := runner.(*wazeroengine.Engine)
	return engine
}

func unifiedPassCatalogForExperiment() *passplugin.Registry {
	registry, err := passplugin.NewDefaultUnifiedCatalog()
	if err != nil {
		panic(err)
	}
	return registry
}

func TestPLMPrefixPreparedCapacityUsesFreshSessions(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("pooled analyser capacity requires Linux copy-on-write")
	}
	artifact, err := osReadGuestArtifact(t)
	if err != nil {
		t.Fatal(err)
	}
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = 90 * time.Second
	capacity, err := newPLMPrefixPreparedCapacity(context.Background(), artifact, config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = capacity.Close(context.Background()) }()

	first, firstEvidence, _, err := capacity.NewSession(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	second, secondEvidence, _, err := capacity.NewSession(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("programs reused one analyser session")
	}
	if err := second.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, evidence := range []wazeroengine.SemanticAnalysisSessionProvisionEvidence{firstEvidence, secondEvidence} {
		if !evidence.COWHit || !evidence.NeverServed || evidence.BrokerAvailable || evidence.WorkspaceMounted || evidence.RuntimeInitCalls != 0 {
			t.Fatalf("session was not fresh private capacity: %+v", evidence)
		}
	}
	lifecycle := capacity.LifecycleEvidence()
	if capacity.SessionCount() != 2 || lifecycle.COWHits != 2 || lifecycle.PreparedProvisions != 0 || lifecycle.RuntimeInitCalls != 0 {
		t.Fatalf("capacity was not pooled across fresh sessions: sessions=%d lifecycle=%+v", capacity.SessionCount(), lifecycle)
	}
}

func TestPLMPrefixEagerEconomicsFixture(t *testing.T) {
	output := os.Getenv("PYSOLATE_PLM_PREFIX_EAGER_OUTPUT")
	if output == "" {
		t.Skip("set PYSOLATE_PLM_PREFIX_EAGER_OUTPUT to run")
	}
	artifact, err := osReadGuestArtifact(t)
	if err != nil {
		t.Fatal(err)
	}
	cell := plmPrefixEagerCellFromEnv(t)
	runs := 5
	if raw := os.Getenv("PYSOLATE_PLM_PREFIX_EAGER_RUNS"); raw != "" {
		runs, err = strconv.Atoi(raw)
		if err != nil || runs < 1 || runs > 10 {
			t.Fatal("runs must be in [1,10]")
		}
	}
	treatments := []string{"serial_whole_file", "pysolate_pooled_prefix"}
	if cell.includeImmediate {
		treatments = append(treatments, "immediate_dispatch_reference")
	}
	offset := 0
	if raw := os.Getenv("PYSOLATE_PLM_PREFIX_EAGER_ORDER_OFFSET"); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 || offset >= len(treatments) {
			t.Fatalf("order offset must be in [0,%d]", len(treatments)-1)
		}
	}
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = 90 * time.Second
	artifactDigest := sha256.Sum256(artifact)
	capacity, err := newPLMPrefixPreparedCapacity(context.Background(), artifact, config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := capacity.Close(context.Background()); closeErr != nil {
			t.Errorf("close pooled analyser capacity: %v", closeErr)
		}
	}()
	evidence := plmPrefixEagerEvidence{
		SchemaVersion: plmPrefixEagerSchema, CellID: cell.id, SourceCommit: os.Getenv("PYSOLATE_EXPERIMENT_SOURCE_COMMIT"), SourceTree: os.Getenv("PYSOLATE_EXPERIMENT_SOURCE_TREE"),
		HostID: os.Getenv("EVALUATION_HOST_ID"), ArtifactSHA256: fmt.Sprintf("sha256:%x", artifactDigest[:]), Runs: runs, ProviderDelayMS: int(cell.providerDelayDuration() / time.Millisecond),
		ChunkOffsetsMS: cell.chunkOffsetsMS(), SourceSHA256: testDigest(cell.sourceText()),
		EagerEstimateScope: "Immediate dispatch is a scheduling reference; supplied EAGER and the local published-gate route are measured separately",
	}
	expectedResultSHA, err := playback.CanonicalSHA256(cell.expectedResult)
	if err != nil {
		t.Fatal(err)
	}
	for trial := 0; trial < runs; trial++ {
		for order := 0; order < len(treatments); order++ {
			name := treatments[(trial+offset+order)%len(treatments)]
			tracker := &plmPrefixEagerTracker{responses: cell.providerResponses, delay: cell.providerDelayDuration()}
			adapter := &e2ePLMAdapter{handler: capability.HandlerFunc(tracker.handler)}
			planCallBudget := cell.expectedCalls
			if planCallBudget == 0 {
				planCallBudget = 1
			}
			plan := plmE2EPlan(t, planCallBudget, adapter)
			runID := fmt.Sprintf("plm-prefix-eager-%s-%d-%s", cell.id, trial, name)
			workspaceRoot := filepath.Join(t.TempDir(), runID)
			brokerFactory := func(context.Context) (*capability.Broker, error) {
				return capability.NewBroker(capability.Config{RunIdentity: runID, Plan: plan})
			}
			var treatment semanticspeculation.ScheduledTreatment
			switch name {
			case "serial_whole_file":
				runConfig := config
				runConfig.Mechanisms = runtimeconfig.MechanismSet{PrivateWorkspace: true}
				treatment, err = semanticspeculation.NewSerialGuestTreatment(semanticspeculation.SerialGuestTreatmentConfig{Artifact: artifact, RunConfig: runConfig, Plan: plan, BrokerFactory: brokerFactory, ProviderObservation: tracker.observation, RunID: runID, WorkspaceRoot: workspaceRoot, WorkspaceOwner: runID})
			case "immediate_dispatch_reference":
				treatment, err = semanticspeculation.NewEagerGuestTreatment(semanticspeculation.EagerGuestTreatmentConfig{Artifact: artifact, RunConfig: config, Plan: plan, BrokerFactory: brokerFactory, ProviderObservation: tracker.observation, RunID: runID, WorkspaceRoot: workspaceRoot, WorkspaceOwner: runID})
			case "pysolate_pooled_prefix":
				treatment = newPLMPrefixTreatment(capacity, plan, adapter, tracker, cell.expectedCalls, cell.expectedPrefixCalls, runID, workspaceRoot)
			}
			if err != nil {
				t.Fatal(err)
			}
			sample, runErr := runPLMPrefixEagerTreatment(context.Background(), trial, order, name, cell, tracker, treatment)
			if runErr != nil {
				t.Fatalf("trial=%d treatment=%s: %v", trial, name, runErr)
			}
			if plm, ok := treatment.(*plmPrefixTreatment); ok {
				sample.SplitPhase, sample.PLMLifecycle = plm.split, plm.lifecycle
				sample.PrefixAnalysisNanos, sample.PrefixAdmissionNanos = plm.prefixAnalysisNanos, plm.prefixAdmissionNanos
				sample.PrefixAnalyzerInvocations = plm.prefixAnalyzerInvocations
				sample.AnalyzerProvision = plm.analyzerProvision
				sample.AnalyzerSessionPrepareNanos = plm.analyzerSessionPrepareNanos
				sample.FinalExecutionNanos = plm.finalExecutionNanos
				if sample.PrefixAnalyzerInvocations != 1 || sample.SplitPhase.Reused != cell.expectedPrefixCalls || sample.SplitPhase.MaximumConcurrent != cell.expectedPrefixCalls {
					t.Fatalf("prefix path did not run or was not reused: analysis=%d split=%+v", sample.PrefixAnalyzerInvocations, sample.SplitPhase)
				}
				if sample.AnalyzerSessionPrepareNanos == 0 || sample.PrefixAnalysisNanos == 0 || sample.PrefixAdmissionNanos == 0 || sample.FinalExecutionNanos == 0 {
					t.Fatalf("prefix phase timing was not recorded: %+v", sample)
				}
				if !sample.AnalyzerProvision.NeverServed || sample.AnalyzerProvision.BrokerAvailable || sample.AnalyzerProvision.WorkspaceMounted || (goruntime.GOOS == "linux" && !sample.AnalyzerProvision.COWHit) {
					t.Fatalf("prefix analyzer was not provisioned before source arrival: %+v", sample.AnalyzerProvision)
				}
			}
			if sample.Outcome.FinalProgramOutcome != "success" || sample.Outcome.LogicalCalls != cell.expectedCalls || sample.Outcome.PhysicalAttempts != cell.expectedCalls || sample.Outcome.ResultSHA256 != expectedResultSHA {
				t.Fatalf("invalid outcome: %+v", sample.Outcome)
			}
			if name == "pysolate_pooled_prefix" && sample.ProviderMaxConcurrent != cell.expectedPrefixCalls {
				t.Fatalf("Pysolate did not overlap provider calls: %+v", sample)
			}
			if name == "serial_whole_file" && cell.expectedCalls > 0 && sample.ProviderMaxConcurrent != 1 {
				t.Fatalf("serial treatment overlapped provider calls: %+v", sample)
			}
			if cell.expectedCalls > 0 && sample.ProviderNanos == 0 {
				t.Fatalf("provider timing was not recorded: %+v", sample)
			}
			evidence.Samples = append(evidence.Samples, sample)
		}
	}
	evidence.AnalyzerCapacitySetupNanos = capacity.SetupNanos()
	evidence.AnalyzerCapacitySessionCount = capacity.SessionCount()
	evidence.AnalyzerCapacityLifecycle = capacity.LifecycleEvidence()
	if evidence.AnalyzerCapacitySessionCount != uint32(runs) || evidence.AnalyzerCapacityLifecycle.COWHits != uint32(runs) || evidence.AnalyzerCapacityLifecycle.RuntimeInitCalls != 0 {
		t.Fatalf("analyser capacity was not pooled: sessions=%d lifecycle=%+v", evidence.AnalyzerCapacitySessionCount, evidence.AnalyzerCapacityLifecycle)
	}
	encoded, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func runPLMPrefixEagerTreatment(ctx context.Context, trial, order int, name string, cell plmPrefixEagerCell, tracker *plmPrefixEagerTracker, treatment semanticspeculation.ScheduledTreatment) (plmPrefixEagerSample, error) {
	coldStarted := time.Now()
	beginStarted := time.Now()
	if err := treatment.Begin(ctx, json.RawMessage(`{}`)); err != nil {
		return plmPrefixEagerSample{}, err
	}
	beginNanos := uint64(time.Since(beginStarted))
	postBeginStarted := time.Now()
	for _, chunk := range cell.chunks {
		due := postBeginStarted.Add(chunk.offset)
		if delay := time.Until(due); delay > 0 {
			time.Sleep(delay)
		}
		if err := treatment.ObserveChunk(ctx, chunk.source); err != nil {
			return plmPrefixEagerSample{}, err
		}
	}
	tracker.finalized.Store(true)
	finalizeStarted := time.Now()
	outcome, err := treatment.Finalize(ctx)
	return plmPrefixEagerSample{Trial: trial, Order: order, Treatment: name, ColdNanos: uint64(time.Since(coldStarted)), PostBeginNanos: uint64(time.Since(postBeginStarted)), BeginNanos: beginNanos, FinalizeNanos: uint64(time.Since(finalizeStarted)), ProviderNanos: tracker.providerNanos.Load(), ProviderMaxConcurrent: tracker.maxConcurrent.Load(), Outcome: outcome}, err
}

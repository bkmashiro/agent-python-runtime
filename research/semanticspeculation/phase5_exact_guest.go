package semanticspeculation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/agentfunction"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/preparedregion"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

var ErrPhase5DerivedOperationsUnavailable = errors.New("phase 5 derived Exact Guest operations are not yet provisioned")

// Phase5ExactGuestOperations owns one run-private Exact Guest operation graph.
// The first delivered slice supports the complete original lane; derived methods
// fail closed until their three-capacity state machine is attached.
type Phase5ExactGuestOperations struct {
	mu       sync.Mutex
	artifact []byte
	config   runtimeconfig.RunConfig

	finalEngine     *wazeroengine.Engine
	finalCapacity   *wazeroengine.PreparedRegionFinalCapacity
	analyzerEngine  *wazeroengine.Engine
	analyzerSession *wazeroengine.SemanticAnalysisSession
	scratchEngine   *wazeroengine.Engine
	scratchCapacity *wazeroengine.PreparedRegionScratchCapacity
	preparedTable   *preparedregion.PreparedRegionTable
	derivedMode     bool
	analysis        *semantic.Analysis
	candidate       *semantic.CandidateRegion
	analysisSHA256  string
	decision        preparedregion.PreparedRegionDecision
	decisionRaw     []byte
	liveInsRaw      []byte
	patch           preparedregion.PreparedRegionPatch
	scratchResult   preparedregion.PreparedRegionScratchResult
	capsule         preparedregion.PreparedRegionCapsule
	request         []byte
	snapshot        Phase5ExecutionSnapshot
	teardown        bool
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
	if operations.teardown {
		return errors.New("phase 5 operations already closed")
	}
	newEngine := func(table *preparedregion.PreparedRegionTable) (*wazeroengine.Engine, error) {
		if table == nil {
			return wazeroengine.New(ctx, operations.artifact, operations.config)
		}
		runner, err := (wazeroengine.Factory{PreparedRegions: table}).New(ctx, operations.artifact, operations.config)
		if err != nil {
			return nil, err
		}
		engine, ok := runner.(*wazeroengine.Engine)
		if !ok {
			_ = runner.Close(ctx)
			return nil, errors.New("phase 5 final engine type drift")
		}
		return engine, nil
	}
	switch kind {
	case Phase5AnalyzerCapacity:
		if operations.analyzerEngine != nil || operations.finalEngine != nil {
			return errors.New("phase 5 analyzer capacity ordering drift")
		}
		engine, err := newEngine(nil)
		if err != nil {
			return err
		}
		session, err := engine.NewSemanticAnalysisSession(ctx, wazeroengine.SemanticAnalysisSessionLimits{MaxRequests: 2, MaxCumulativeRequestBytes: 32 * 1024, MaxDuration: 20 * time.Second})
		if err != nil {
			_ = engine.Close(context.Background())
			return err
		}
		evidence, err := session.Prepare(ctx)
		if err != nil {
			_ = session.Close(context.Background())
			_ = engine.Close(context.Background())
			return err
		}
		if !evidence.NeverServed || evidence.RuntimeInitCalls != 1 || evidence.BrokerAvailable || evidence.WorkspaceMounted || (runtime.GOOS == "linux" && !evidence.COWHit) || (runtime.GOOS != "linux" && !evidence.PreparedHit) {
			_ = session.Close(context.Background())
			_ = engine.Close(context.Background())
			return errors.New("phase 5 analyzer capacity evidence drift")
		}
		operations.derivedMode = true
		operations.analyzerEngine, operations.analyzerSession = engine, session
		operations.snapshot.AnalyzerSessionCount = 1
		operations.snapshot.AnalyzerRuntimeInitCount = evidence.RuntimeInitCalls
		return nil
	case Phase5ScratchCapacity:
		if !operations.derivedMode || operations.analyzerSession == nil || operations.scratchEngine != nil || operations.finalEngine != nil {
			return errors.New("phase 5 scratch capacity ordering drift")
		}
		engine, err := newEngine(nil)
		if err != nil {
			return err
		}
		capacity, evidence, err := engine.PreparePreparedRegionScratch(ctx)
		if err != nil {
			_ = engine.Close(context.Background())
			return err
		}
		if !evidence.NeverServed || evidence.RuntimeInitCalls != 1 || evidence.BrokerAvailable || evidence.WorkspaceMounted || (runtime.GOOS == "linux" && !evidence.COWHit) || (runtime.GOOS != "linux" && !evidence.PreparedHit) {
			_ = capacity.Close(context.Background())
			_ = engine.Close(context.Background())
			return errors.New("phase 5 scratch capacity evidence drift")
		}
		operations.scratchEngine, operations.scratchCapacity = engine, capacity
		operations.snapshot.ScratchRuntimeInitCount = evidence.RuntimeInitCalls
		return nil
	case Phase5FinalCapacity:
		if operations.finalEngine != nil || (operations.derivedMode && operations.scratchCapacity == nil) {
			return errors.New("phase 5 final capacity ordering drift")
		}
		var table *preparedregion.PreparedRegionTable
		var err error
		if operations.derivedMode {
			table, err = preparedregion.NewPreparedRegionTable(nil)
			if err != nil {
				return err
			}
		}
		engine, err := newEngine(table)
		if err != nil {
			if table != nil {
				_ = table.Close()
			}
			return err
		}
		capacity, evidence, err := engine.PreparePreparedRegionFinal(ctx)
		if err != nil {
			_ = engine.Close(context.Background())
			if table != nil {
				_ = table.Close()
			}
			return err
		}
		if !evidence.NeverServed || evidence.RuntimeInitCalls != 1 || evidence.ModuleInstantiations == 0 || evidence.BrokerAvailable || evidence.WorkspaceMounted || (runtime.GOOS == "linux" && !evidence.COWHit) || (runtime.GOOS != "linux" && !evidence.PreparedHit) {
			_ = capacity.Close(context.Background())
			_ = engine.Close(context.Background())
			if table != nil {
				_ = table.Close()
			}
			return errors.New("phase 5 final capacity evidence drift")
		}
		operations.finalEngine, operations.finalCapacity, operations.preparedTable = engine, capacity, table
		operations.snapshot.FinalRuntimeInitCount = evidence.RuntimeInitCalls
		return nil
	default:
		return errors.New("invalid phase 5 capacity kind")
	}
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

func (operations *Phase5ExactGuestOperations) Analyze(ctx context.Context, input Phase5ExecutionInput) error {
	if operations == nil || ctx == nil || input.Source == "" || input.OutputName == "" {
		return errors.New("invalid phase 5 analysis input")
	}
	operations.mu.Lock()
	defer operations.mu.Unlock()
	if operations.teardown || !operations.derivedMode || operations.analyzerSession == nil || operations.scratchCapacity == nil || operations.finalCapacity == nil || operations.analysis != nil {
		return ErrPhase5DerivedOperationsUnavailable
	}
	artifactSHA256 := phase5Digest(operations.artifact)
	profileSHA256 := operations.analyzerEngine.Properties().ExecutionProfileBindingSHA256
	request, err := semantic.NewRequest(input.Source, semantic.Bindings{
		ArtifactSHA256:         artifactSHA256,
		ExecutionProfileSHA256: profileSHA256,
		ImportClosureSHA256:    agentfunction.ImportClosureIdentity([]string{}, []string{}),
		CapabilityPlanSHA256:   phase5Digest([]byte("pysolate.phase5.empty-capability-plan.v1")),
	}, nil)
	if err != nil {
		return err
	}
	verified, err := semantic.AnalyzeVerifiedSession(ctx, operations.analyzerSession, request)
	if err != nil {
		return err
	}
	analysis, err := verified.Analysis()
	if err != nil {
		return err
	}
	if int(input.FocusRegionIndex) >= len(analysis.CandidateRegions) {
		return errors.New("phase 5 focus region is unavailable")
	}
	candidate := analysis.CandidateRegions[input.FocusRegionIndex]
	if !candidate.LocallyReusable() || len(candidate.LiveOuts) != 1 || candidate.LiveOuts[0] != input.OutputName {
		return errors.New("phase 5 focus region is not an exact scalar candidate")
	}
	analysisSHA256, _, err := analysis.Identity()
	if err != nil {
		return err
	}
	operations.analysis = &analysis
	operations.candidate = &candidate
	operations.analysisSHA256 = analysisSHA256
	return nil
}
func (operations *Phase5ExactGuestOperations) EmitPatch(ctx context.Context, input Phase5ExecutionInput) error {
	if operations == nil || ctx == nil {
		return errors.New("invalid phase 5 patch input")
	}
	operations.mu.Lock()
	defer operations.mu.Unlock()
	if operations.analysis == nil || operations.candidate == nil || operations.decisionRaw != nil || operations.analyzerSession == nil {
		return ErrPhase5DerivedOperationsUnavailable
	}
	candidate := *operations.candidate
	span := preparedregion.SourceSpan{StartLine: candidate.Span.StartLine, StartColumn: candidate.Span.StartColumn, EndLine: candidate.Span.EndLine, EndColumn: candidate.Span.EndColumn}
	regionSource, liveIns, err := phase5ExactScalarInputs(input.Source, candidate)
	if err != nil {
		return err
	}
	liveInsRaw, liveInsSHA256, err := preparedregion.SealPreparedRegionLiveIns(liveIns)
	if err != nil {
		return err
	}
	binding := preparedregion.PreparedRegionBinding{
		SourceSHA256: operations.analysis.SourceSHA256, ASTSHA256: operations.analysis.ASTSHA256,
		AnalysisSHA256: operations.analysisSHA256, RegionID: candidate.ID, RegionSpan: span,
		RegionSourceSHA256: phase5Digest([]byte(regionSource)), LiveInsSHA256: liveInsSHA256,
		EnvironmentSHA256:      phase5Digest([]byte("pysolate.phase5.environment.v1\x00" + operations.analysis.SourceSHA256 + "\x00" + operations.analysis.ExecutionProfileSHA256)),
		ExecutionProfileSHA256: operations.analysis.ExecutionProfileSHA256, ImportClosureSHA256: operations.analysis.ImportClosureSHA256,
		CapabilityPlanSHA256: operations.analysis.CapabilityPlanSHA256, PassConfigSHA256: phase5Digest([]byte("pysolate.phase5.scalar-pass.v1")),
		Codec: preparedregion.PreparedRegionCodecJSONScalarV1, OutputName: input.OutputName,
	}
	decisionRaw, decision, err := preparedregion.SealPreparedRegionDecision(binding)
	if err != nil {
		return err
	}
	emitRequest, err := json.Marshal(map[string]string{"decision": string(decisionRaw), "final_source": input.Source})
	if err != nil {
		return err
	}
	bindingRaw, err := operations.analyzerSession.EmitPreparedRegionPatch(ctx, emitRequest)
	if err != nil {
		return err
	}
	patchBinding, err := preparedregion.DecodePreparedRegionPatchBinding(bindingRaw)
	if err != nil {
		return err
	}
	_, patch, err := preparedregion.SealPreparedRegionPatch(patchBinding)
	if err != nil || patch.ValidateDecision(decision) != nil {
		return errors.Join(err, preparedregion.ErrInvalidPreparedRegion)
	}
	operations.decision, operations.decisionRaw, operations.liveInsRaw, operations.patch = decision, decisionRaw, liveInsRaw, patch
	operations.snapshot.DecisionSHA256 = decision.IdentitySHA256
	operations.snapshot.PatchSHA256 = patch.IdentitySHA256
	return nil
}
func (operations *Phase5ExactGuestOperations) ExecuteScratch(ctx context.Context, input Phase5ExecutionInput) error {
	if operations == nil || ctx == nil {
		return errors.New("invalid phase 5 scratch input")
	}
	operations.mu.Lock()
	defer operations.mu.Unlock()
	if operations.decisionRaw == nil || operations.liveInsRaw == nil || operations.scratchCapacity == nil || operations.scratchResult.Status != "" {
		return ErrPhase5DerivedOperationsUnavailable
	}
	request, err := json.Marshal(map[string]string{"decision": string(operations.decisionRaw), "final_source": input.Source, "live_ins": string(operations.liveInsRaw)})
	if err != nil {
		return err
	}
	result, evidence, err := operations.scratchCapacity.Execute(ctx, request, operations.decision)
	if err != nil {
		return err
	}
	if !evidence.PreparedCapacity || evidence.BrokerAvailable || evidence.WorkspaceMounted {
		return errors.New("phase 5 scratch capacity lifecycle drift")
	}
	operations.scratchResult = result
	operations.snapshot.ScratchGuestExecutions = 1
	return nil
}
func (operations *Phase5ExactGuestOperations) SealCapsule(context.Context) error {
	if operations == nil {
		return errors.New("invalid phase 5 capsule operation")
	}
	operations.mu.Lock()
	defer operations.mu.Unlock()
	if operations.scratchResult.Status == "" || operations.capsule.IdentitySHA256 != "" || operations.preparedTable == nil {
		return ErrPhase5DerivedOperationsUnavailable
	}
	_, capsule, err := preparedregion.PublishPreparedRegionScratchResult(operations.decision, operations.scratchResult)
	if err != nil {
		return err
	}
	if err := operations.preparedTable.Publish(operations.decision, capsule); err != nil {
		return err
	}
	operations.capsule = capsule
	operations.snapshot.CapsuleSHA256 = capsule.IdentitySHA256
	operations.snapshot.CapsuleBytes = capsule.PayloadBytes
	return nil
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
	if operations.scratchCapacity != nil {
		err = errors.Join(err, operations.scratchCapacity.Close(ctx))
	}
	if operations.analyzerSession != nil {
		err = errors.Join(err, operations.analyzerSession.Close(ctx))
	}
	if operations.finalEngine != nil {
		err = errors.Join(err, operations.finalEngine.Close(ctx))
	}
	if operations.scratchEngine != nil {
		err = errors.Join(err, operations.scratchEngine.Close(ctx))
	}
	if operations.analyzerEngine != nil {
		err = errors.Join(err, operations.analyzerEngine.Close(ctx))
	}
	if operations.preparedTable != nil {
		err = errors.Join(err, operations.preparedTable.Close())
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

func phase5ExactScalarInputs(source string, candidate semantic.CandidateRegion) (string, map[string]json.RawMessage, error) {
	lines := strings.Split(source, "\n")
	if candidate.Span.StartLine == 0 || candidate.Span.StartLine != candidate.Span.EndLine || int(candidate.Span.StartLine) > len(lines) {
		return "", nil, errors.New("phase 5 scalar region must occupy one source line")
	}
	line := lines[candidate.Span.StartLine-1]
	if int(candidate.Span.EndColumn) > len(line) || candidate.Span.StartColumn > candidate.Span.EndColumn {
		return "", nil, errors.New("phase 5 scalar region span is invalid")
	}
	region := line[candidate.Span.StartColumn:candidate.Span.EndColumn]
	liveIns := make(map[string]json.RawMessage, len(candidate.LiveIns))
	for _, name := range candidate.LiveIns {
		found := false
		for index := int(candidate.Span.StartLine) - 2; index >= 0; index-- {
			parts := strings.SplitN(strings.TrimSpace(lines[index]), "=", 2)
			if len(parts) != 2 || strings.TrimSpace(parts[0]) != name {
				continue
			}
			value := strings.TrimSpace(parts[1])
			if _, err := strconv.ParseInt(value, 10, 64); err != nil {
				return "", nil, errors.New("phase 5 live-in is not a signed int64 literal")
			}
			liveIns[name] = json.RawMessage(value)
			found = true
			break
		}
		if !found {
			return "", nil, errors.New("phase 5 live-in literal is unavailable")
		}
	}
	return region, liveIns, nil
}

func phase5Digest(value []byte) string {
	digest := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(digest[:])
}

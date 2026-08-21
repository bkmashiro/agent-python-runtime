package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	researchdata "github.com/bkmashiro/agent-python-runtime/research/prepareddataset"
	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/prepareddataset"
	"github.com/bkmashiro/agent-python-runtime/runtime/preparedregion"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

const (
	resourcePath  = "/workspace/input.npy"
	readDelay     = 200 * time.Millisecond
	generationGap = 500 * time.Millisecond
	digestA       = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

type report struct {
	SchemaVersion            string                `json:"schema_version"`
	ArtifactSHA256           string                `json:"artifact_sha256"`
	PlanSHA256               string                `json:"plan_sha256"`
	ContractSHA256           string                `json:"contract_sha256"`
	PreparationSHA256        string                `json:"preparation_sha256"`
	ClaimSHA256              string                `json:"claim_sha256"`
	CandidateCall            string                `json:"candidate_call"`
	PhysicalCapability       string                `json:"physical_capability"`
	NoContractStarts         uint64                `json:"no_contract_starts"`
	AuthorizedStarts         uint64                `json:"authorized_starts"`
	ArtificialReadDelayNanos uint64                `json:"artificial_read_delay_nanos"`
	ReadStartedNanos         uint64                `json:"read_started_nanos"`
	ReadCompletedNanos       uint64                `json:"read_completed_nanos"`
	DecodeCompletedNanos     uint64                `json:"decode_completed_nanos"`
	FinalSourceReleasedNanos uint64                `json:"final_source_released_nanos"`
	ReadBeforeFinal          bool                  `json:"read_before_final"`
	DecodeBeforeFinal        bool                  `json:"decode_before_final"`
	PhysicalOutcomeBytes     uint64                `json:"physical_outcome_bytes"`
	TypedMetadata            researchdata.Metadata `json:"typed_metadata"`
	Staged                   researchdata.Snapshot `json:"staged"`
	TokenClaims              uint64                `json:"token_claims"`
	TokenConsumed            uint64                `json:"token_consumed"`
	TokenDiscarded           uint64                `json:"token_discarded"`
	StagedObjectClaimJoined  bool                  `json:"staged_object_claim_joined"`
	Result                   json.RawMessage       `json:"result"`
	LogicalParity            bool                  `json:"logical_parity"`
	VerifiedTargetGuest      bool                  `json:"verified_target_guest"`
	LaterSyntaxRejected      bool                  `json:"later_syntax_rejected"`
}

type readHandler struct {
	mu        sync.Mutex
	file      string
	delay     time.Duration
	origin    time.Time
	starts    uint64
	started   uint64
	completed uint64
	body      []byte
}

func (handler *readHandler) Call(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var args struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(raw, &args) != nil || args.Path != resourcePath {
		return nil, errors.New("source selector mismatch")
	}
	handler.mu.Lock()
	handler.starts++
	handler.started = uint64(time.Since(handler.origin))
	handler.mu.Unlock()
	timer := time.NewTimer(handler.delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
	}
	body, err := os.ReadFile(handler.file)
	if err != nil {
		return nil, err
	}
	if len(body) != researchdata.CanonicalFileBytes || digestBytes(body) != researchdata.CanonicalFileSHA256 {
		return nil, errors.New("immutable source identity drifted")
	}
	handler.mu.Lock()
	handler.body = append([]byte(nil), body...)
	handler.completed = uint64(time.Since(handler.origin))
	handler.mu.Unlock()
	return json.Marshal(map[string]any{"bytes": len(body), "file_sha256": researchdata.CanonicalFileSHA256})
}

func (handler *readHandler) snapshot() (uint64, uint64, uint64, []byte) {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	return handler.starts, handler.started, handler.completed, append([]byte(nil), handler.body...)
}

type prepareResult struct {
	object          *researchdata.StagedObject
	outcome         capability.StagedCapabilityOutcome
	decodeCompleted uint64
	err             error
}

func main() {
	artifactRoot := flag.String("artifact-root", "", "verified numpy-core dist root")
	flag.Parse()
	wasm, profile, err := loadArtifact(*artifactRoot)
	if err != nil {
		fail(err)
	}
	temp, err := os.MkdirTemp("", "pysolate-prepared-data-")
	if err != nil {
		fail(err)
	}
	defer os.RemoveAll(temp)
	fixturePath := filepath.Join(temp, "input.npy")
	if err := os.WriteFile(fixturePath, researchdata.CanonicalFixture(), 0o400); err != nil {
		fail(err)
	}
	handler := &readHandler{file: fixturePath, delay: readDelay}
	plan, err := testPlan(handler)
	if err != nil {
		fail(err)
	}
	config := runtimeconfig.DefaultRunConfig()
	config.ExecutionProfile = &profile
	config.Mechanisms.SemanticAnalysis = true
	analyzer, err := wazeroengine.New(context.Background(), wasm, config)
	if err != nil {
		fail(err)
	}
	defer analyzer.Close(context.Background())
	bindings, err := analysisBindings(config, plan)
	if err != nil {
		fail(err)
	}
	prefix := "import numpy as np\ndataset = np.load('/workspace/input.npy', allow_pickle=False)\n"
	prefixRequest, err := prepareddataset.NewAnalysisRequest(prefix, bindings, plan)
	if err != nil {
		fail(err)
	}
	prefixVerified, err := semantic.AnalyzeVerified(context.Background(), analyzer, prefixRequest)
	if err != nil {
		fail(err)
	}
	prefixSHA := digestText(prefix)
	facts, err := prepareddataset.FactsFromVerifiedAnalysis(prefix, "stream-p3-1", prefixSHA, prefixVerified)
	if err != nil {
		fail(err)
	}
	decisionContext := contextBinding(profile, config)
	if _, err := prepareddataset.Decide(nil, facts, plan, decisionContext); !errors.Is(err, prepareddataset.ErrNoPreparedContract) {
		fail(errors.New("missing contract admitted"))
	}
	if starts, _, _, _ := handler.snapshot(); starts != 0 {
		fail(errors.New("physical read started without contract"))
	}
	declaration := declarationFor(facts, plan, profile, config)
	contract, err := prepareddataset.NewPreparedDataContract(declaration)
	if err != nil {
		fail(err)
	}
	decision, err := prepareddataset.Decide(&contract, facts, plan, decisionContext)
	if err != nil {
		fail(err)
	}
	physicalCall, err := decision.PhysicalCallSite()
	if err != nil {
		fail(err)
	}
	prepared, err := plan.PreparePreDispatch(physicalCall.Capability, physicalCall.CanonicalArguments)
	if err != nil {
		fail(err)
	}
	declared := contract.Declaration()
	object, err := researchdata.NewStagedObject(researchdata.HostReceipt{
		ContractSHA256: contract.Identity(), PreparationSHA256: decision.PreparationIdentity,
		FileSHA256: declared.FileSHA256, BodySHA256: declared.BodySHA256,
		ExecutionProfileSHA256: declared.ExecutionProfileSHA256, PrivacyPartition: declared.PrivacyPartition,
		Freshness:               declared.Freshness + ":" + declared.StreamEpoch,
		BudgetReservationSHA256: declared.BudgetReservationSHA256,
		MaxFileBytes:            declared.MaxFileBytes, MaxBodyBytes: declared.MaxBodyBytes,
	})
	if err != nil {
		fail(err)
	}
	if err := object.IssueRead(prepareddataset.PreparedFileBytes); err != nil {
		fail(err)
	}
	origin := time.Now()
	handler.mu.Lock()
	handler.origin = origin
	handler.mu.Unlock()
	preparedCh := make(chan prepareResult, 1)
	go func() {
		outcome, callErr := prepared.Call(context.Background())
		if callErr != nil {
			_ = object.Reject(callErr)
			preparedCh <- prepareResult{object: object, outcome: outcome, err: callErr}
			return
		}
		_, _, _, body := handler.snapshot()
		if err := object.VerifySource(researchdata.CanonicalFileSHA256); err != nil {
			_ = object.Reject(err)
			preparedCh <- prepareResult{object: object, outcome: outcome, err: err}
			return
		}
		if err := object.Decode(body); err != nil {
			_ = object.Reject(err)
			preparedCh <- prepareResult{object: object, outcome: outcome, err: err}
			return
		}
		if err := object.Seal(); err != nil {
			_ = object.Reject(err)
			preparedCh <- prepareResult{object: object, outcome: outcome, err: err}
			return
		}
		preparedCh <- prepareResult{object: object, outcome: outcome, decodeCompleted: uint64(time.Since(origin))}
	}()
	time.Sleep(generationGap)
	finalReleased := uint64(time.Since(origin))
	finalSource := prefix + "result = {'shape': list(dataset.shape), 'sum': int(dataset.sum())}\n"
	finalRequest, err := prepareddataset.NewAnalysisRequest(finalSource, bindings, plan)
	if err != nil {
		fail(err)
	}
	finalVerified, err := semantic.AnalyzeVerified(context.Background(), analyzer, finalRequest)
	if err != nil {
		fail(err)
	}
	claim, err := decision.Claim(finalSource, finalVerified)
	if err != nil {
		fail(err)
	}
	syntaxRejected := false
	invalidRequest, invalidErr := prepareddataset.NewAnalysisRequest(prefix+"result = (\n", bindings, plan)
	if invalidErr == nil {
		_, analyzeErr := semantic.AnalyzeVerified(context.Background(), analyzer, invalidRequest)
		syntaxRejected = analyzeErr != nil
	}
	if !syntaxRejected {
		fail(errors.New("later syntax error was not rejected"))
	}
	preparedResult := <-preparedCh
	if preparedResult.err != nil {
		fail(preparedResult.err)
	}
	materialized, err := object.MaterializeStaging()
	if err != nil {
		fail(err)
	}
	guestResult, tokenEvidence, err := runPreparedGuest(wasm, profile, finalSource, claim, materialized.Body)
	if err != nil {
		fail(err)
	}
	var observed struct {
		Shape []uint64 `json:"shape"`
		Sum   uint64   `json:"sum"`
	}
	if err := json.Unmarshal(guestResult, &observed); err != nil {
		fail(err)
	}
	parity := len(observed.Shape) == 2 && observed.Shape[0] == 1024 && observed.Shape[1] == 1024 && observed.Sum == 549755289600 &&
		materialized.Metadata.DType == researchdata.DTypeInt64LE && materialized.Metadata.BodySHA256 == researchdata.CanonicalBodySHA256
	if !parity {
		fail(fmt.Errorf("logical parity failed: result=%s metadata=%+v", guestResult, materialized.Metadata))
	}
	if tokenEvidence.Claims != 1 || tokenEvidence.Consumed != 1 || tokenEvidence.Discarded != 0 {
		fail(errors.New("dynamic claim token mismatch"))
	}
	// The scalar token proves dynamic reach, but the current Guest body-copy
	// transport is not object-bound. Retire the physical staging as orphaned
	// rather than misreporting an exact staged-object logical claim.
	if err := object.Orphan(); err != nil {
		fail(err)
	}
	var oracle struct {
		Shape []int `json:"shape"`
		Sum   int64 `json:"sum"`
	}
	if json.Unmarshal(guestResult, &oracle) != nil || len(oracle.Shape) != 2 || oracle.Shape[0] != 1024 || oracle.Shape[1] != 1024 || oracle.Sum != researchdata.CanonicalSum {
		fail(errors.New("baseline oracle mismatch"))
	}
	starts, readStarted, readCompleted, _ := handler.snapshot()
	snapshot := object.Snapshot()
	out := report{
		SchemaVersion: "pysolate.prepared-data-probe.v2", ArtifactSHA256: profile.ArtifactSHA256(), PlanSHA256: plan.Identity(),
		ContractSHA256: contract.Identity(), PreparationSHA256: decision.PreparationIdentity, ClaimSHA256: claim.ClaimIdentity,
		CandidateCall: prepareddataset.PreparedCall, PhysicalCapability: prepareddataset.PreparedCapability,
		NoContractStarts: 0, AuthorizedStarts: starts, ArtificialReadDelayNanos: uint64(readDelay),
		ReadStartedNanos: readStarted, ReadCompletedNanos: readCompleted, DecodeCompletedNanos: preparedResult.decodeCompleted,
		FinalSourceReleasedNanos: finalReleased, ReadBeforeFinal: readStarted < finalReleased, DecodeBeforeFinal: preparedResult.decodeCompleted < finalReleased,
		PhysicalOutcomeBytes: preparedResult.outcome.PhysicalResultBytes, TypedMetadata: materialized.Metadata, Staged: snapshot,
		TokenClaims: uint64(tokenEvidence.Claims), TokenConsumed: uint64(tokenEvidence.Consumed), TokenDiscarded: uint64(tokenEvidence.Discarded),
		StagedObjectClaimJoined: false,
		Result:                  guestResult, LogicalParity: parity, VerifiedTargetGuest: true, LaterSyntaxRejected: syntaxRejected,
	}
	if out.AuthorizedStarts != 1 || !out.ReadBeforeFinal || !out.DecodeBeforeFinal || snapshot.State != researchdata.StateOrphaned || snapshot.Counters.LogicalClaims != 0 || snapshot.Counters.PhysicalOrphans != 1 {
		fail(errors.New("P3 invariants failed"))
	}
	encoded, _ := json.Marshal(out)
	fmt.Println(string(encoded))
}

func runPreparedGuest(wasm []byte, profile runtimeconfig.ExecutionProfile, source string, claim prepareddataset.ClaimDecision, body []byte) (json.RawMessage, preparedregion.PreparedRegionTableEvidence, error) {
	analysisSHA := digestText("prepared-data-final-analysis")
	_, decision, err := preparedregion.SealPreparedRegionDecision(preparedregion.PreparedRegionBinding{
		SourceSHA256: claim.FinalSourceSHA256, ASTSHA256: analysisSHA, AnalysisSHA256: analysisSHA, RegionID: claim.ClaimIdentity,
		RegionSpan:         preparedregion.SourceSpan{StartLine: claim.CallSite.Span.StartLine, StartColumn: claim.CallSite.Span.StartColumn, EndLine: claim.CallSite.Span.EndLine, EndColumn: claim.CallSite.Span.EndColumn},
		RegionSourceSHA256: claim.ClaimIdentity, LiveInsSHA256: digestA, EnvironmentSHA256: digestA,
		ExecutionProfileSHA256: digestA, ImportClosureSHA256: digestA, CapabilityPlanSHA256: digestA,
		PassConfigSHA256: digestA, Codec: preparedregion.PreparedRegionCodecJSONScalarV1, OutputName: "dataset",
	})
	if err != nil {
		return nil, preparedregion.PreparedRegionTableEvidence{}, err
	}
	_, capsule, err := preparedregion.SealPreparedRegionCapsule(decision.IdentitySHA256, json.RawMessage(`true`))
	if err != nil {
		return nil, preparedregion.PreparedRegionTableEvidence{}, err
	}
	table, err := preparedregion.NewPreparedRegionTable([]preparedregion.PreparedRegionEntry{{Decision: decision, Capsule: capsule}})
	if err != nil {
		return nil, preparedregion.PreparedRegionTableEvidence{}, err
	}
	config := runtimeconfig.DefaultRunConfig()
	config.Timeout = 120 * time.Second
	config.MaxRequestBytes = 16 << 20
	config.MaxResponseBytes = 16 << 20
	config.ExecutionProfile = &profile
	config.Mechanisms.SemanticAnalysis = true
	runner, err := (wazeroengine.Factory{PreparedRegions: table}).New(context.Background(), wasm, config)
	if err != nil {
		return nil, preparedregion.PreparedRegionTableEvidence{}, err
	}
	defer runner.Close(context.Background())
	request, err := runtimeconfig.EncodeRunRequest(runtimeconfig.RunRequest{RunID: "prepared-data-p3", Code: source, Inputs: json.RawMessage(`{}`), Compatibility: &runtimeconfig.CompatibilityDeclaration{Profile: "numpy-core", Imports: []string{"numpy"}}})
	if err != nil {
		return nil, preparedregion.PreparedRegionTableEvidence{}, err
	}
	response, err := runner.Run(context.Background(), request, monkeypatchSource(decision.IdentitySHA256, body))
	if err != nil {
		return nil, preparedregion.PreparedRegionTableEvidence{}, err
	}
	var envelope struct {
		Status string          `json:"status"`
		Result json.RawMessage `json:"result"`
	}
	if json.Unmarshal(response, &envelope) != nil || envelope.Status != "ok" {
		return nil, table.Evidence(), fmt.Errorf("Guest response=%s", response)
	}
	return envelope.Result, table.Evidence(), nil
}

func monkeypatchSource(decision string, body []byte) string {
	payload := base64.StdEncoding.EncodeToString(body)
	return strings.Join([]string{
		"import base64 as _pd_base64, json as _pd_json, numpy as np, _agent_runtime_host as _pd_host",
		"_pd_body = bytearray(_pd_base64.b64decode(" + fmt.Sprintf("%q", payload) + "))",
		"def _pd_load(path, *, allow_pickle=False):",
		"    if path != '/workspace/input.npy' or allow_pickle is not False:",
		"        raise RuntimeError('prepared data call mismatch')",
		"    if _pd_json.loads(_pd_host.materialize_value(" + fmt.Sprintf("%q", decision) + ")) is not True:",
		"        raise RuntimeError('prepared data claim token mismatch')",
		"    return np.frombuffer(_pd_body, dtype=np.dtype('<i8')).reshape((1024, 1024))",
		"np.load = _pd_load",
	}, "\n") + "\n"
}

func declarationFor(facts prepareddataset.NumpyLoadFacts, plan *capability.Plan, profile runtimeconfig.ExecutionProfile, config runtimeconfig.RunConfig) prepareddataset.HostPreparedDataDeclaration {
	profileSHA, _ := runtimeconfig.ExecutionProfileBindingSHA256(config)
	imports := profile.QualifiedImports()
	sort.Strings(imports)
	importRaw, _ := json.Marshal(imports)
	return prepareddataset.HostPreparedDataDeclaration{
		SchemaVersion: prepareddataset.ContractSchemaVersion, Capability: prepareddataset.PreparedCapability, Call: prepareddataset.PreparedCall,
		CapabilityPlanSHA256: plan.Identity(), StreamEpoch: facts.StreamEpoch, AdmittedPrefixSHA256: facts.AdmittedPrefixSHA256,
		Span: facts.CallSite.Span, CanonicalArguments: facts.CallSite.CanonicalArguments, DynamicOccurrence: facts.CallSite.DynamicOccurrence,
		ResourceNamespace: "workspace", ResourcePath: resourcePath, SourcePolicy: prepareddataset.SourcePolicyImmutableWorkspaceRoot,
		WorkspaceRootSHA256: digestText("workspace-root"), FileSHA256: researchdata.CanonicalFileSHA256, BodySHA256: researchdata.CanonicalBodySHA256,
		Freshness: capability.FreshnessPlanEpoch, Unclaimed: capability.UnclaimedDiscardWithDisposition,
		LoaderKind: prepareddataset.LoaderNumpyNPYV1, AllowPickle: false, MMapMode: "", DType: "<i8", Shape: []uint64{1024, 1024},
		Order: "C", Endianness: "little", HeaderBytes: prepareddataset.PreparedHeaderBytes, ElementBytes: 8, CodecKind: prepareddataset.CodecNumpyNDArrayCV1,
		ArtifactSHA256: profile.ArtifactSHA256(), ExecutionProfileSHA256: profileSHA, ImportClosureSHA256: digestBytes(importRaw),
		RunIdentity: "run-p3-1", PrivacyPartition: "run-p3-private", BudgetReservationSHA256: digestText("p3-budget"),
		MaxFileBytes: prepareddataset.PreparedFileBytes, MaxBodyBytes: prepareddataset.PreparedBodyBytes, MaxResultBytes: prepareddataset.PreparedMaxResultBytes, CostUnits: 1,
	}
}

func contextBinding(profile runtimeconfig.ExecutionProfile, config runtimeconfig.RunConfig) prepareddataset.DecisionContext {
	profileSHA, _ := runtimeconfig.ExecutionProfileBindingSHA256(config)
	imports := profile.QualifiedImports()
	sort.Strings(imports)
	raw, _ := json.Marshal(imports)
	return prepareddataset.DecisionContext{
		RunIdentity: "run-p3-1", PrivacyPartition: "run-p3-private",
		WorkspaceRootSHA256: digestText("workspace-root"), FileSHA256: researchdata.CanonicalFileSHA256, BodySHA256: researchdata.CanonicalBodySHA256,
		ArtifactSHA256: profile.ArtifactSHA256(), ExecutionProfileSHA256: profileSHA, ImportClosureSHA256: digestBytes(raw),
		BudgetReservationSHA256: digestText("p3-budget"),
		FileBytes:               prepareddataset.PreparedFileBytes, BodyBytes: prepareddataset.PreparedBodyBytes, CostUnits: 1,
	}
}

func analysisBindings(config runtimeconfig.RunConfig, plan *capability.Plan) (semantic.Bindings, error) {
	profileSHA, err := runtimeconfig.ExecutionProfileBindingSHA256(config)
	if err != nil {
		return semantic.Bindings{}, err
	}
	imports := config.ExecutionProfile.QualifiedImports()
	sort.Strings(imports)
	raw, _ := json.Marshal(imports)
	return semantic.Bindings{ArtifactSHA256: config.ExecutionProfile.ArtifactSHA256(), ExecutionProfileSHA256: profileSHA, ImportClosureSHA256: digestBytes(raw), CapabilityPlanSHA256: plan.Identity()}, nil
}

func testPlan(handler *readHandler) (*capability.Plan, error) {
	registry := capability.NewRegistry()
	grant, err := capability.NewGrant(json.RawMessage(`{"scope":"prepared-data-prototype"}`))
	if err != nil {
		return nil, err
	}
	spec := capability.Spec{
		Name: prepareddataset.PreparedCapability, Version: "v1", HandlerIdentity: "prepared-data-read-v1", Description: "prototype immutable npy read", EffectClass: capability.EffectExternalRead, Playback: capability.PlaybackLiveOnly,
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"bytes":{"type":"integer","minimum":0,"maximum":8388736},"file_sha256":{"type":"string","maxLength":71,"pattern":"^sha256:[0-9a-f]{64}$"}},"required":["bytes","file_sha256"],"additionalProperties":false}`),
		Python:       &capability.PythonProjection{Module: "sources", Method: "read", Arguments: []string{"path"}}, ReadOnly: true, Idempotent: true,
		PreDispatch: &capability.PreDispatchContract{Resource: capability.ResourceReference{Namespace: "workspace", Argument: "path"}, Freshness: capability.FreshnessPlanEpoch, Unclaimed: capability.UnclaimedDiscardWithDisposition, Privacy: capability.PreDispatchPrivacyExactPartition, Coalescing: capability.PreDispatchCoalescingForbidden, MaxResultBytes: prepareddataset.PreparedMaxResultBytes, CostUnits: 1},
	}
	if err := registry.Register(spec, grant, capability.HandlerFunc(handler.Call)); err != nil {
		return nil, err
	}
	return registry.Seal(capability.PlanConfig{MaxCalls: 1})
}

func loadArtifact(root string) ([]byte, runtimeconfig.ExecutionProfile, error) {
	var zero runtimeconfig.ExecutionProfile
	if root == "" {
		return nil, zero, errors.New("artifact root is required")
	}
	read := func(name string) ([]byte, error) { return os.ReadFile(filepath.Join(root, name)) }
	wasm, err := read("agent-python-runtime-numpy-core.wasm")
	if err != nil {
		return nil, zero, err
	}
	manifest, err := read("manifest.json")
	if err != nil {
		return nil, zero, err
	}
	inventory, err := read("import-inventory.json")
	if err != nil {
		return nil, zero, err
	}
	qualification, err := read("import-qualification.json")
	if err != nil {
		return nil, zero, err
	}
	identity, err := runtimeconfig.VerifyDistributionArtifact("agent-python-runtime-numpy-core.wasm", wasm, manifest, inventory, qualification)
	if err != nil {
		return nil, zero, err
	}
	profile, err := runtimeconfig.NewExecutionProfile("numpy-core", []string{"base64", "datetime", "hashlib", "numpy"})
	if err != nil {
		return nil, zero, err
	}
	profile, err = profile.BindVerifiedArtifact(identity)
	return wasm, profile, err
}

func digestText(value string) string { return digestBytes([]byte(value)) }
func digestBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }

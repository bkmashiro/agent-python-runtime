package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	wazeroengine "github.com/bkmashiro/agent-python-runtime/runtime/engine/wazero"
	"github.com/bkmashiro/agent-python-runtime/runtime/prepareddataset"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

const prefixSource = "import numpy as np\ndataset = np.load('/workspace/input.npy', allow_pickle=False)\n"

type report struct {
	SchemaVersion       string `json:"schema_version"`
	ArtifactSHA256      string `json:"artifact_sha256"`
	PlanSHA256          string `json:"plan_sha256"`
	ContractSHA256      string `json:"contract_sha256"`
	PreparationSHA256   string `json:"preparation_sha256"`
	ClaimSHA256         string `json:"claim_sha256"`
	CandidateCall       string `json:"candidate_call"`
	PhysicalCapability  string `json:"physical_capability"`
	NoContractStarts    uint64 `json:"no_contract_starts"`
	AuthorizedStarts    uint64 `json:"authorized_starts"`
	FinalSourceSHA256   string `json:"final_source_sha256"`
	VerifiedTargetGuest bool   `json:"verified_target_guest"`
}

func main() {
	root := flag.String("artifact-root", "", "verified numpy-core dist root")
	flag.Parse()
	wasm, profile, err := loadArtifact(*root)
	if err != nil {
		fail(err)
	}
	var starts atomic.Uint64
	plan, err := newPlan(&starts)
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
	request, err := prepareddataset.NewAnalysisRequest(prefixSource, bindings, plan)
	if err != nil {
		fail(err)
	}
	verified, err := semantic.AnalyzeVerified(context.Background(), analyzer, request)
	if err != nil {
		fail(err)
	}
	prefixSHA := digestText(prefixSource)
	facts, err := prepareddataset.FactsFromVerifiedAnalysis(prefixSource, "stream-authority-1", prefixSHA, verified)
	if err != nil {
		analysis, _ := verified.Analysis()
		encoded, _ := json.Marshal(analysis.CallSites)
		fmt.Fprintf(os.Stderr, "call_sites=%s\n", encoded)
		fail(err)
	}
	contextBinding := decisionContext(profile, config)
	without, err := prepareddataset.Decide(nil, facts, plan, contextBinding)
	if !errors.Is(err, prepareddataset.ErrNoPreparedContract) || without.Allowed || starts.Load() != 0 {
		fail(errors.New("missing Host contract admitted physical work"))
	}
	declaration := declarationFor(facts, plan, profile, config)
	contract, err := prepareddataset.NewPreparedDataContract(declaration)
	if err != nil {
		fail(err)
	}
	decision, err := prepareddataset.Decide(&contract, facts, plan, contextBinding)
	if err != nil || !decision.Allowed {
		fail(fmt.Errorf("decision: %w", err))
	}
	prepared, err := plan.PreparePreDispatch(prepareddataset.PreparedCapability, json.RawMessage(`{"path":"/workspace/input.npy"}`))
	if err != nil {
		fail(err)
	}
	if _, err := prepared.Call(context.Background()); err != nil {
		fail(err)
	}
	if starts.Load() != 1 {
		fail(errors.New("authorized physical start count drifted"))
	}
	finalSource := prefixSource + "result = {'shape': list(dataset.shape), 'sum': int(dataset.sum())}\n"
	finalRequest, err := prepareddataset.NewAnalysisRequest(finalSource, bindings, plan)
	if err != nil {
		fail(err)
	}
	finalVerified, err := semantic.AnalyzeVerified(context.Background(), analyzer, finalRequest)
	if err != nil {
		fail(err)
	}
	claim, err := decision.Claim(finalSource, finalVerified)
	if err != nil || !claim.Allowed {
		fail(fmt.Errorf("claim: %w", err))
	}
	out := report{
		SchemaVersion: "pysolate.prepared-data-authority-probe.v1", ArtifactSHA256: profile.ArtifactSHA256(), PlanSHA256: plan.Identity(),
		ContractSHA256: contract.Identity(), PreparationSHA256: decision.PreparationIdentity, ClaimSHA256: claim.ClaimIdentity,
		CandidateCall: prepareddataset.PreparedCall, PhysicalCapability: prepareddataset.PreparedCapability,
		NoContractStarts: 0, AuthorizedStarts: starts.Load(), FinalSourceSHA256: claim.FinalSourceSHA256, VerifiedTargetGuest: true,
	}
	encoded, _ := json.Marshal(out)
	fmt.Println(string(encoded))
}

func newPlan(starts *atomic.Uint64) (*capability.Plan, error) {
	registry := capability.NewRegistry()
	grant, err := capability.NewGrant(json.RawMessage(`{"scope":"immutable-fixture"}`))
	if err != nil {
		return nil, err
	}
	spec := capability.Spec{
		Name: prepareddataset.PreparedCapability, Version: "pysolate.sources.read.v1", Description: "read one immutable fixture",
		EffectClass: capability.EffectExternalRead, Playback: capability.PlaybackLiveOnly, HandlerIdentity: "prepared-data-read-v1",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"body":{"type":"string"}},"required":["body"],"additionalProperties":false}`),
		Python:       &capability.PythonProjection{Module: "sources", Method: "read", Arguments: []string{"path"}}, ReadOnly: true, Idempotent: true,
		PreDispatch: &capability.PreDispatchContract{Resource: capability.ResourceReference{Namespace: "workspace", Argument: "path"}, Freshness: capability.FreshnessPlanEpoch, Unclaimed: capability.UnclaimedDiscardWithDisposition, Privacy: capability.PreDispatchPrivacyExactPartition, Coalescing: capability.PreDispatchCoalescingForbidden, MaxResultBytes: prepareddataset.PreparedMaxResultBytes, CostUnits: 1},
	}
	if err := registry.Register(spec, grant, capability.HandlerFunc(func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
		starts.Add(1)
		return json.RawMessage(`{"body":""}`), nil
	})); err != nil {
		return nil, err
	}
	return registry.Seal(capability.PlanConfig{MaxCalls: 1})
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

func declarationFor(facts prepareddataset.NumpyLoadFacts, plan *capability.Plan, profile runtimeconfig.ExecutionProfile, config runtimeconfig.RunConfig) prepareddataset.HostPreparedDataDeclaration {
	bindings, _ := analysisBindings(config, plan)
	return prepareddataset.HostPreparedDataDeclaration{
		SchemaVersion: prepareddataset.ContractSchemaVersion, Capability: prepareddataset.PreparedCapability, Call: prepareddataset.PreparedCall,
		CapabilityPlanSHA256: plan.Identity(), StreamEpoch: facts.StreamEpoch, AdmittedPrefixSHA256: facts.AdmittedPrefixSHA256,
		Span: facts.CallSite.Span, CanonicalArguments: append(json.RawMessage(nil), facts.CallSite.CanonicalArguments...), DynamicOccurrence: 1,
		ResourceNamespace: prepareddataset.PreparedResourceNamespace, ResourcePath: prepareddataset.PreparedResourcePath,
		SourcePolicy: prepareddataset.SourcePolicyImmutableWorkspaceRoot, WorkspaceRootSHA256: digestText("immutable-workspace-root"),
		FileSHA256: "sha256:3ce0191336981ea3fc1a28e281a236f1698123195f4f637af7e412628e38803f", BodySHA256: "sha256:a78cee677876b925402c15818acd3fc020a47754d9d1c26688914ea09070f8d0",
		Freshness: capability.FreshnessPlanEpoch, Unclaimed: capability.UnclaimedDiscardWithDisposition,
		LoaderKind: prepareddataset.LoaderNumpyNPYV1, AllowPickle: false, DType: prepareddataset.PreparedDType, Shape: []uint64{1024, 1024},
		Order: prepareddataset.PreparedOrder, Endianness: prepareddataset.PreparedEndianness, HeaderBytes: prepareddataset.PreparedHeaderBytes,
		ElementBytes: prepareddataset.PreparedElementBytes, CodecKind: prepareddataset.CodecNumpyNDArrayCV1,
		ArtifactSHA256: profile.ArtifactSHA256(), ExecutionProfileSHA256: bindings.ExecutionProfileSHA256, ImportClosureSHA256: bindings.ImportClosureSHA256,
		RunIdentity: "authority-probe-1", PrivacyPartition: "authority-probe-private", BudgetReservationSHA256: digestText("budget-reservation"),
		MaxFileBytes: prepareddataset.PreparedFileBytes, MaxBodyBytes: prepareddataset.PreparedBodyBytes, MaxResultBytes: prepareddataset.PreparedMaxResultBytes, CostUnits: 1,
	}
}

func decisionContext(profile runtimeconfig.ExecutionProfile, config runtimeconfig.RunConfig) prepareddataset.DecisionContext {
	profileSHA, _ := runtimeconfig.ExecutionProfileBindingSHA256(config)
	imports := profile.QualifiedImports()
	sort.Strings(imports)
	raw, _ := json.Marshal(imports)
	return prepareddataset.DecisionContext{
		WorkspaceRootSHA256: digestText("immutable-workspace-root"),
		FileSHA256:          "sha256:3ce0191336981ea3fc1a28e281a236f1698123195f4f637af7e412628e38803f", BodySHA256: "sha256:a78cee677876b925402c15818acd3fc020a47754d9d1c26688914ea09070f8d0",
		ArtifactSHA256: profile.ArtifactSHA256(), ExecutionProfileSHA256: profileSHA, ImportClosureSHA256: digestBytes(raw),
		RunIdentity: "authority-probe-1", PrivacyPartition: "authority-probe-private", BudgetReservationSHA256: digestText("budget-reservation"),
		FileBytes: prepareddataset.PreparedFileBytes, BodyBytes: prepareddataset.PreparedBodyBytes, CostUnits: 1,
	}
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

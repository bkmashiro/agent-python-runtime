package prepareddataset

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

func TestPreparedDataContractRequiresExplicitHostJoin(t *testing.T) {
	plan := testPlan(t)
	declaration := validDeclaration(plan.Identity())
	contract, err := NewPreparedDataContract(declaration)
	if err != nil {
		t.Fatal(err)
	}
	facts, err := factsForSource(baseSource(), declaration)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := Decide(&contract, facts, plan, validContext(declaration))
	if err != nil || !decision.Allowed || decision.PreparationIdentity == "" {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
	withoutContract, err := Decide(nil, facts, plan, validContext(declaration))
	if !errors.Is(err, ErrNoPreparedContract) || withoutContract.Allowed || withoutContract.PreparationIdentity != "" {
		t.Fatalf("without contract decision=%+v err=%v", withoutContract, err)
	}
	if _, err := NewPreparedDataContractFromPythonMetadata(map[string]any{
		"schema_version": ContractSchemaVersion,
		"capability":     "sources.read",
	}); !errors.Is(err, ErrHostContractRequired) {
		t.Fatalf("Python metadata created a Host contract: %v", err)
	}
	if facts.HasHostContract() {
		t.Fatal("authority-free facts unexpectedly carry a Host contract")
	}
}

func TestPreparedDataPreparationIdentityExcludesUnknownFinalSource(t *testing.T) {
	declaration := validDeclaration(testPlan(t).Identity())
	contract, err := NewPreparedDataContract(declaration)
	if err != nil {
		t.Fatal(err)
	}
	facts, err := factsForSource(baseSource(), declaration)
	if err != nil {
		t.Fatal(err)
	}
	first, err := Decide(&contract, facts, testPlan(t), validContext(declaration))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(first.PreparationIdentity, "final") {
		t.Fatal("preparation identity contains final-source material")
	}
	finalSource := baseSource() + "result = dataset.sum()\n"
	claim, err := first.claimWithCallSites(finalSource, []semantic.CallSite{testCallSite()})
	if err != nil || !claim.Allowed || claim.FinalSourceSHA256 == "" || claim.ClaimIdentity == first.PreparationIdentity {
		t.Fatalf("claim=%+v err=%v", claim, err)
	}
	if claim.PreparationIdentity != first.PreparationIdentity {
		t.Fatal("claim changed preparation identity")
	}
}

func TestPreparedDataContractRejectsEveryAuthoritySourceProfileFreshnessMutation(t *testing.T) {
	basePlan := testPlan(t)
	base := validDeclaration(basePlan.Identity())
	baseContract, err := NewPreparedDataContract(base)
	if err != nil {
		t.Fatal(err)
	}
	facts, err := factsForSource(baseSource(), base)
	if err != nil {
		t.Fatal(err)
	}
	mutations := map[string]func(*HostPreparedDataDeclaration){
		"schema":       func(v *HostPreparedDataDeclaration) { v.SchemaVersion = "pysolate.prepared-data-contract.v0" },
		"capability":   func(v *HostPreparedDataDeclaration) { v.Capability = "workspace.read" },
		"call":         func(v *HostPreparedDataDeclaration) { v.Call = "numpy.save" },
		"plan":         func(v *HostPreparedDataDeclaration) { v.CapabilityPlanSHA256 = digest('a') },
		"stream epoch": func(v *HostPreparedDataDeclaration) { v.StreamEpoch = "other-epoch" },
		"prefix":       func(v *HostPreparedDataDeclaration) { v.AdmittedPrefixSHA256 = digest('a') },
		"span":         func(v *HostPreparedDataDeclaration) { v.Span.StartColumn++ },
		"arguments": func(v *HostPreparedDataDeclaration) {
			v.CanonicalArguments = json.RawMessage(`{"allow_pickle":false,"path":"/workspace/other.npy"}`)
		},
		"occurrence":     func(v *HostPreparedDataDeclaration) { v.DynamicOccurrence = 2 },
		"workspace path": func(v *HostPreparedDataDeclaration) { v.ResourcePath = "/workspace/other.npy" },
		"source policy":  func(v *HostPreparedDataDeclaration) { v.SourcePolicy = "mutable_latest" },
		"workspace root": func(v *HostPreparedDataDeclaration) { v.WorkspaceRootSHA256 = digest('a') },
		"file digest":    func(v *HostPreparedDataDeclaration) { v.FileSHA256 = digest('a') },
		"body digest":    func(v *HostPreparedDataDeclaration) { v.BodySHA256 = digest('a') },
		"freshness":      func(v *HostPreparedDataDeclaration) { v.Freshness = "latest" },
		"unclaimed":      func(v *HostPreparedDataDeclaration) { v.Unclaimed = "ignore" },
		"loader":         func(v *HostPreparedDataDeclaration) { v.LoaderKind = "numpy_generic" },
		"allow pickle":   func(v *HostPreparedDataDeclaration) { v.AllowPickle = true },
		"mmap mode":      func(v *HostPreparedDataDeclaration) { v.MMapMode = "r" },
		"dtype":          func(v *HostPreparedDataDeclaration) { v.DType = ">i8" },
		"shape":          func(v *HostPreparedDataDeclaration) { v.Shape[0] = 2 },
		"order":          func(v *HostPreparedDataDeclaration) { v.Order = "F" },
		"endianness":     func(v *HostPreparedDataDeclaration) { v.Endianness = "big" },
		"codec":          func(v *HostPreparedDataDeclaration) { v.CodecKind = "other" },
		"artifact":       func(v *HostPreparedDataDeclaration) { v.ArtifactSHA256 = digest('a') },
		"profile":        func(v *HostPreparedDataDeclaration) { v.ExecutionProfileSHA256 = digest('a') },
		"imports":        func(v *HostPreparedDataDeclaration) { v.ImportClosureSHA256 = digest('a') },
		"run":            func(v *HostPreparedDataDeclaration) { v.RunIdentity = "other-run" },
		"privacy":        func(v *HostPreparedDataDeclaration) { v.PrivacyPartition = "other-partition" },
		"body budget":    func(v *HostPreparedDataDeclaration) { v.MaxBodyBytes = 1 << 20 },
		"result budget":  func(v *HostPreparedDataDeclaration) { v.MaxResultBytes = 1 << 20 },
		"file budget":    func(v *HostPreparedDataDeclaration) { v.MaxFileBytes = 1 << 20 },
		"cost budget":    func(v *HostPreparedDataDeclaration) { v.CostUnits = 2 },
		"reservation":    func(v *HostPreparedDataDeclaration) { v.BudgetReservationSHA256 = digest('a') },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			candidate := cloneDeclaration(base)
			mutate(&candidate)
			candidateContract, sealErr := NewPreparedDataContract(candidate)
			if sealErr != nil {
				return
			}
			decision, decisionErr := Decide(&candidateContract, facts, basePlan, validContext(base))
			if decisionErr == nil && decision.Allowed {
				t.Fatalf("mutation was admitted: %+v", decision)
			}
		})
	}
	_ = baseContract
}

func TestPreparedDataUnknownAndMissingFieldsFailClosed(t *testing.T) {
	declaration := validDeclaration(digest('1'))
	raw, err := json.Marshal(declaration)
	if err != nil {
		t.Fatal(err)
	}
	unknown := append(append([]byte(nil), raw[:len(raw)-1]...), []byte(`,"unexpected":true}`)...)
	if _, err := DecodePreparedDataContract(unknown); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("unknown field error=%v", err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	delete(document, "body_sha256")
	missing, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodePreparedDataContract(missing); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("missing field error=%v", err)
	}
}

func TestNumpyLoadFactsRejectUnverifiedDynamicAndDuplicateOccurrences(t *testing.T) {
	source := baseSource()
	declaration := validDeclaration(digest('1'))
	base := testCallSite()
	cases := map[string][]semantic.CallSite{
		"absent":            nil,
		"dynamic arguments": {func() semantic.CallSite { value := base; value.ArgumentsCanonical = false; return value }()},
		"may not reach":     {func() semantic.CallSite { value := base; value.NecessarilyReached = false; return value }()},
		"wrong surface":     {func() semantic.CallSite { value := base; value.Capability = PreparedCapability; return value }()},
		"duplicate":         {base, base},
	}
	for name, callSites := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := factsFromCallSites(source, declaration.StreamEpoch, digestText(source), callSites); !errors.Is(err, ErrNoEligiblePeephole) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestPreparedDataClaimRequiresPrefixExtensionAndExactOccurrence(t *testing.T) {
	plan := testPlan(t)
	declaration := validDeclaration(plan.Identity())
	contract, err := NewPreparedDataContract(declaration)
	if err != nil {
		t.Fatal(err)
	}
	facts, err := factsForSource(baseSource(), declaration)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := Decide(&contract, facts, plan, validContext(declaration))
	if err != nil {
		t.Fatal(err)
	}
	for name, source := range map[string]string{
		"not extension": "import numpy as np\ndataset = np.load('/workspace/input.npy', allow_pickle=False)\n",
		"path drift":    strings.Replace(baseSource(), "/workspace/input.npy", "/workspace/other.npy", 1),
		"option drift":  strings.Replace(baseSource(), "allow_pickle=False", "allow_pickle=True", 1),
		"alias drift":   strings.Replace(baseSource(), "import numpy as np", "import numpy as n", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if claim, err := decision.claimWithCallSites(source, []semantic.CallSite{testCallSite()}); err == nil || claim.Allowed {
				t.Fatalf("claim=%+v err=%v", claim, err)
			}
		})
	}
	if _, err := decision.claimWithCallSites(baseSource()+"dataset2 = np.load('/workspace/input.npy', allow_pickle=False)\n", []semantic.CallSite{testCallSite(), testCallSite()}); !errors.Is(err, ErrClaimMismatch) {
		t.Fatalf("duplicate occurrence error=%v", err)
	}
}

func TestNumpyLoadProjectionIsClosedAndAuthorityFree(t *testing.T) {
	projection := NumpyLoadProjection
	if projection.Name != PreparedCall || projection.Module != "np" || projection.Method != "load" ||
		projection.EffectClass != capability.EffectExternalRead || projection.Arguments == nil {
		t.Fatalf("projection=%+v", projection)
	}
	plan := testPlan(t)
	request, err := NewAnalysisRequest(baseSource(), semantic.Bindings{
		ArtifactSHA256: digest('1'), ExecutionProfileSHA256: digest('2'), ImportClosureSHA256: digest('3'), CapabilityPlanSHA256: plan.Identity(),
	}, plan)
	if err != nil {
		t.Fatal(err)
	}
	seenCall, seenCapability := false, false
	for _, candidate := range request.Capabilities {
		seenCall = seenCall || candidate.Name == PreparedCall
		seenCapability = seenCapability || candidate.Name == PreparedCapability
	}
	if !seenCall || !seenCapability {
		t.Fatalf("projections=%+v", request.Capabilities)
	}
	facts, err := factsFromCallSites(baseSource(), "stream-1", digestText(baseSource()), []semantic.CallSite{testCallSite()})
	if err != nil {
		t.Fatal(err)
	}
	if facts.CallSite.Capability != PreparedCall || facts.CallSite.DynamicOccurrence != 1 || !facts.CallSite.ArgumentsCanonical {
		t.Fatalf("facts=%+v", facts)
	}
}

func testCallSite() semantic.CallSite {
	canonical, _ := canonicalArguments(PreparedResourcePath)
	span := semantic.SourceSpan{StartLine: 2, StartColumn: 11, EndLine: 2, EndColumn: 61}
	return semantic.CallSite{
		ID: digestText("prepared-call"), Span: span, Capability: PreparedCall,
		ControlRegionID: digestText("prepared-control"), NecessarilyReached: true,
		ArgumentsCanonical: true, CanonicalArguments: canonical, DynamicOccurrence: 1,
	}
}

func factsForSource(source string, declaration HostPreparedDataDeclaration) (NumpyLoadFacts, error) {
	return factsFromCallSites(source, declaration.StreamEpoch, declaration.AdmittedPrefixSHA256, []semantic.CallSite{testCallSite()})
}

func validDeclaration(planIdentity string) HostPreparedDataDeclaration {
	source := baseSource()
	return HostPreparedDataDeclaration{
		SchemaVersion: ContractSchemaVersion, Capability: PreparedCapability, Call: PreparedCall, CapabilityPlanSHA256: planIdentity,
		StreamEpoch: "stream-epoch-1", AdmittedPrefixSHA256: digestText(source),
		Span:               semantic.SourceSpan{StartLine: 2, StartColumn: 11, EndLine: 2, EndColumn: 61},
		CanonicalArguments: json.RawMessage(`{"allow_pickle":false,"path":"/workspace/input.npy"}`), DynamicOccurrence: 1,
		ResourceNamespace: "workspace", ResourcePath: "/workspace/input.npy", SourcePolicy: SourcePolicyImmutableWorkspaceRoot,
		WorkspaceRootSHA256: digest('1'), FileSHA256: digest('2'), BodySHA256: digest('3'),
		Freshness: capability.FreshnessPlanEpoch, Unclaimed: capability.UnclaimedDiscardWithDisposition,
		LoaderKind: LoaderNumpyNPYV1, AllowPickle: false, MMapMode: "", DType: "<i8", Shape: []uint64{1024, 1024},
		Order: "C", Endianness: "little", HeaderBytes: PreparedHeaderBytes, ElementBytes: PreparedElementBytes, CodecKind: CodecNumpyNDArrayCV1,
		ArtifactSHA256: digest('4'), ExecutionProfileSHA256: digest('5'), ImportClosureSHA256: digest('6'),
		RunIdentity: "run-1", PrivacyPartition: "run-1-private", BudgetReservationSHA256: digest('7'),
		MaxFileBytes: 8_388_736, MaxBodyBytes: 8_388_608, MaxResultBytes: 4 << 10, CostUnits: 1,
	}
}

func validContext(declaration HostPreparedDataDeclaration) DecisionContext {
	return DecisionContext{
		WorkspaceRootSHA256: declaration.WorkspaceRootSHA256, FileSHA256: declaration.FileSHA256, BodySHA256: declaration.BodySHA256,
		ArtifactSHA256: declaration.ArtifactSHA256, ExecutionProfileSHA256: declaration.ExecutionProfileSHA256, ImportClosureSHA256: declaration.ImportClosureSHA256,
		RunIdentity: declaration.RunIdentity, PrivacyPartition: declaration.PrivacyPartition, BudgetReservationSHA256: declaration.BudgetReservationSHA256,
		FileBytes: declaration.MaxFileBytes, BodyBytes: declaration.MaxBodyBytes, CostUnits: declaration.CostUnits,
	}
}

func testPlan(t *testing.T) *capability.Plan {
	t.Helper()
	registry := capability.NewRegistry()
	grant, err := capability.NewGrant(json.RawMessage(`{"scope":"fixture"}`))
	if err != nil {
		t.Fatal(err)
	}
	spec := capability.Spec{
		Name: "sources.read", Version: "pysolate.sources.read.v1", Description: "read one source",
		EffectClass: capability.EffectExternalRead, Playback: capability.PlaybackLiveOnly, HandlerIdentity: "sources-read-v1",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"body":{"type":"string"}},"required":["body"],"additionalProperties":false}`),
		Python:       &capability.PythonProjection{Module: "sources", Method: "read", Arguments: []string{"path"}}, ReadOnly: true, Idempotent: true,
		PreDispatch: &capability.PreDispatchContract{Resource: capability.ResourceReference{Namespace: "workspace", Argument: "path"}, Freshness: capability.FreshnessPlanEpoch, Unclaimed: capability.UnclaimedDiscardWithDisposition, Privacy: capability.PreDispatchPrivacyExactPartition, Coalescing: capability.PreDispatchCoalescingForbidden, MaxResultBytes: 4 << 10, CostUnits: 1},
	}
	if err := registry.Register(spec, grant, capability.HandlerFunc(func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"body":""}`), nil
	})); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 1})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func baseSource() string {
	return "import numpy as np\ndataset = np.load('/workspace/input.npy', allow_pickle=False)\n"
}

func digest(character byte) string { return "sha256:" + strings.Repeat(string(character), 64) }

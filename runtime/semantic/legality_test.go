package semantic

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

func TestCanPreissueRequiresVerifiedExactNecessarilyReachedCallAndFrozenContext(t *testing.T) {
	plan := legalityTestPlan(t, true)
	verified, site := legalityVerifiedAnalysis(t, plan, true)
	ctx := legalityContext()

	decision := CanPreissue(verified, plan, site.ID, ctx)
	if !decision.Allowed() || len(decision.Rejections()) != 0 {
		t.Fatalf("decision=%+v", decision)
	}
	call, ok := decision.QualifiedCall()
	if !ok || call.CallSiteID() != site.ID || call.Capability() != site.Capability ||
		call.ArgumentsSHA256() == "" || call.ResourceSHA256() == "" {
		t.Fatalf("qualified call=%+v ok=%v", call, ok)
	}

	for name, run := range map[string]func() Decision{
		"unverified":    func() Decision { return CanPreissue(VerifiedAnalysis{}, plan, site.ID, ctx) },
		"missing site":  func() Decision { return CanPreissue(verified, plan, legalityDigest("missing"), ctx) },
		"plan mismatch": func() Decision { return CanPreissue(verified, legalityTestPlan(t, false), site.ID, ctx) },
		"budget": func() Decision {
			candidate := ctx
			candidate.RemainingPhysicalReads = 0
			return CanPreissue(verified, plan, site.ID, candidate)
		},
		"reservation": func() Decision {
			candidate := ctx
			candidate.BudgetReservationSHA256 = ""
			return CanPreissue(verified, plan, site.ID, candidate)
		},
		"freshness": func() Decision {
			candidate := ctx
			candidate.FreshnessEpoch = ""
			return CanPreissue(verified, plan, site.ID, candidate)
		},
	} {
		t.Run(name, func(t *testing.T) {
			decision := run()
			if decision.Allowed() || len(decision.Rejections()) == 0 {
				t.Fatalf("decision=%+v", decision)
			}
		})
	}

	notReached, notReachedSite := legalityVerifiedAnalysis(t, plan, false)
	decision = CanPreissue(notReached, plan, notReachedSite.ID, ctx)
	if decision.Allowed() || !hasRejection(decision, RejectCallNotNecessarilyReached) {
		t.Fatalf("not-reached decision=%+v", decision)
	}
}

func TestPrepareQualifiedPLMUsesFinalSemanticSourceIdentity(t *testing.T) {
	adapter := &legalityPLMAdapter{}
	plan := legalityPLMPlan(t, adapter)
	verified, site := legalityVerifiedAnalysis(t, plan, true)
	call, ok := CanPreissue(verified, plan, site.ID, legalityContext()).QualifiedCall()
	if !ok {
		t.Fatal("PLM qualified call unavailable")
	}
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "semantic-plm", Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	table, err := capability.NewSplitPhaseTable(broker, capability.SplitPhaseLimits{MaxCalls: 1, MaxCostUnits: 1, MaxResultBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if err := PrepareQualifiedPLM(context.Background(), table, call); err != nil {
		t.Fatal(err)
	}
	if snapshot := table.Snapshot(); snapshot.CandidatesPrepared != 1 || snapshot.Submitted != 1 || broker.Calls() != 0 {
		t.Fatalf("snapshot=%+v calls=%d", snapshot, broker.Calls())
	}
	if err := broker.Finalize(false); err != nil {
		t.Fatal(err)
	}
}

func TestCanClaimStagedObservationRequiresExactIdentityAndReadyState(t *testing.T) {
	plan := legalityTestPlan(t, true)
	verified, site := legalityVerifiedAnalysis(t, plan, true)
	decision := CanPreissue(verified, plan, site.ID, legalityContext())
	call, ok := decision.QualifiedCall()
	if !ok {
		t.Fatalf("preissue decision=%+v", decision)
	}
	claim := call.ExpectedObservationClaim()
	identity := call.ClaimIdentitySHA256()
	if identity == "" {
		t.Fatal("missing claim identity")
	}
	otherContext := legalityContext()
	otherContext.BudgetReservationSHA256 = legalityDigest("other-budget-reservation")
	otherDecision := CanPreissue(verified, plan, site.ID, otherContext)
	otherCall, otherOK := otherDecision.QualifiedCall()
	if !otherOK || otherCall.ClaimIdentitySHA256() == identity {
		t.Fatal("budget reservation did not change exact claim identity")
	}
	if got := CanClaimStagedObservation(call, claim); !got.Allowed() {
		t.Fatalf("claim rejected: %+v", got)
	}
	claim.ArgumentsSHA256 = legalityDigest("wrong")
	if got := CanClaimStagedObservation(call, claim); got.Allowed() || !hasRejection(got, RejectObservationIdentityMismatch) {
		t.Fatalf("mismatch accepted: %+v", got)
	}
	claim = call.ExpectedObservationClaim()
	claim.BudgetReservationSHA256 = legalityDigest("wrong-reservation")
	if got := CanClaimStagedObservation(call, claim); got.Allowed() || !hasRejection(got, RejectObservationIdentityMismatch) {
		t.Fatalf("budget reservation mismatch accepted: %+v", got)
	}
	claim = call.ExpectedObservationClaim()
	claim.Ready = false
	if got := CanClaimStagedObservation(call, claim); got.Allowed() || !hasRejection(got, RejectObservationNotReady) {
		t.Fatalf("terminal observation accepted: %+v", got)
	}
}

func TestUnsupportedSharedLegalityQuestionsFailClosed(t *testing.T) {
	plan := legalityTestPlan(t, true)
	verified, site := legalityVerifiedAnalysis(t, plan, true)
	preissue := CanPreissue(verified, plan, site.ID, legalityContext())
	call, _ := preissue.QualifiedCall()
	if got := CanCoalesce(call); got.Allowed() || !hasRejection(got, RejectCoalescingContractMissing) {
		t.Fatalf("coalesce=%+v", got)
	}
	if got := CanCache(call); got.Allowed() || !hasRejection(got, RejectCacheContractMissing) {
		t.Fatalf("cache=%+v", got)
	}
	backend := RequiredBackend(verified)
	if backend.Backend != BackendUnknown || !hasRejection(backend.Decision, RejectBackendContractMissing) {
		t.Fatalf("backend=%+v", backend)
	}
}

func TestCanReuseWholeRunMintsOnlyExactEffectFreeCanonicalPlan(t *testing.T) {
	capabilityPlan := legalityTestPlan(t, true)
	verifiedAnalysis, _ := legalityVerifiedAnalysis(t, capabilityPlan, true)
	analysis, err := verifiedAnalysis.Analysis()
	if err != nil {
		t.Fatal(err)
	}
	analysis.ModuleEffects = EffectSummary{}
	analysis.CallSites = []CallSite{}
	analysis.CandidateRegions[0].Effects = EffectSummary{}
	analysis.CandidateRegions[0].CapabilityOccurrences = []string{}
	_, encoded, err := analysis.Identity()
	if err != nil {
		t.Fatal(err)
	}
	verifiedAnalysis = VerifiedAnalysis{analysisJSON: encoded}
	plan, _, err := BuildWholeRunPlan(analysis, WholeRunConfig{
		Dependencies:    []Dependency{{Kind: DependencyCanonicalInputs, IdentitySHA256: legalityDigest("inputs")}},
		InputsCanonical: true, OutputsCanonical: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	verifiedPlan, err := BindVerifiedWholeRunPlan(verifiedAnalysis, plan)
	if err != nil {
		t.Fatal(err)
	}
	decision := CanReuseWholeRun(verifiedPlan)
	qualified, ok := decision.QualifiedWholeRun()
	if !decision.Allowed() || !ok || qualified.RegionID() != plan.Regions[0].ID {
		t.Fatalf("decision=%+v qualified=%+v ok=%v", decision, qualified, ok)
	}
	boundAnalysis, boundPlan, _, boundRegion, err := qualified.Bound()
	if err != nil || boundAnalysis.ASTSHA256 != analysis.ASTSHA256 || boundPlan.Regions[0].ID != boundRegion.ID {
		t.Fatalf("analysis=%+v plan=%+v region=%+v err=%v", boundAnalysis, boundPlan, boundRegion, err)
	}

	if got := CanReuseWholeRun(VerifiedWholeRunPlan{}); got.Allowed() || !hasRejection(got, RejectUnverifiedAnalysis) {
		t.Fatalf("forged plan accepted: %+v", got)
	}
	unsafe := analysis
	unsafe.ModuleEffects.MayObserveLive = true
	unsafe.CandidateRegions[0].Effects.MayObserveLive = true
	_, unsafeEncoded, err := unsafe.Identity()
	if err != nil {
		t.Fatal(err)
	}
	unsafeVerified := VerifiedAnalysis{analysisJSON: unsafeEncoded}
	unsafePlan, _, err := BuildWholeRunPlan(unsafe, WholeRunConfig{InputsCanonical: true, OutputsCanonical: true})
	if err != nil {
		t.Fatal(err)
	}
	unsafeBound, err := BindVerifiedWholeRunPlan(unsafeVerified, unsafePlan)
	if err != nil {
		t.Fatal(err)
	}
	if got := CanReuseWholeRun(unsafeBound); got.Allowed() || !hasRejection(got, RejectWholeRunNotReusable) {
		t.Fatalf("effectful plan accepted: %+v", got)
	}
}

func TestSemanticPreDispatchConsumerRequiresExclusiveDynamicCallSurface(t *testing.T) {
	plan := legalityTestPlan(t, true)
	verified, _ := legalityVerifiedAnalysis(t, plan, true)
	analysis, err := verified.Analysis()
	if err != nil || !exclusiveDynamicCallAnalysis(analysis) {
		t.Fatalf("exclusive baseline analysis=%+v err=%v", analysis, err)
	}
	analysis.CallSites = append(analysis.CallSites, analysis.CallSites[0])
	if exclusiveDynamicCallAnalysis(analysis) {
		t.Fatal("multiple positive call sites accepted")
	}
	analysis.CallSites = analysis.CallSites[:1]
	analysis.Barriers = append(analysis.Barriers, Barrier{})
	if exclusiveDynamicCallAnalysis(analysis) {
		t.Fatal("opaque/dynamic barrier accepted")
	}
	analysis.Barriers = nil
	analysis.ModuleEffects.MayBeUnknown = true
	if exclusiveDynamicCallAnalysis(analysis) {
		t.Fatal("unknown module effect accepted")
	}
}

func legalityContext() PreissueContext {
	return PreissueContext{
		StreamEpoch: "stream-1", WorkflowEpoch: "workflow-1",
		FreshnessEpoch: "plan-epoch-1", ExpiryEpoch: "expiry-1",
		PrivacyPartition: "private-1", ParentLineageSHA256: legalityDigest("parent"),
		BudgetReservationSHA256: legalityDigest("budget-reservation"), RemainingPhysicalReads: 1,
	}
}

func legalityVerifiedAnalysis(t *testing.T, plan *capability.Plan, necessarilyReached bool) (VerifiedAnalysis, CallSite) {
	t.Helper()
	source := "result = sources.read(\"profile\")\n"
	sourceSHA := legalityDigest(source)
	span := SourceSpan{StartLine: 1, StartColumn: 9, EndLine: 1, EndColumn: 32}
	site := CallSite{
		ID: legalityDigest("site:" + fmt.Sprint(necessarilyReached)), Span: span,
		Capability: "sources.read", ControlRegionID: legalityDigest("control"),
		NecessarilyReached: necessarilyReached, ArgumentsCanonical: true,
		CanonicalArguments: json.RawMessage(`{"key":"profile"}`), DynamicOccurrence: 1,
	}
	analysis := Analysis{
		SchemaVersion: AnalysisSchemaVersion, SourceSHA256: sourceSHA,
		ASTSHA256: legalityDigest("ast"), AnalyzerSHA256: legalityDigest("analyzer"),
		ArtifactSHA256: legalityDigest("artifact"), ExecutionProfileSHA256: legalityDigest("profile"),
		ImportClosureSHA256: legalityDigest("imports"), CapabilityPlanSHA256: plan.Identity(),
		ModuleSpan:    SourceSpan{StartLine: 1, StartColumn: 0, EndLine: 1, EndColumn: 32},
		ModuleEffects: EffectSummary{MayObserveLive: true, MaySuspend: true},
		Functions:     []FunctionSummary{}, Barriers: []Barrier{}, CallSiteCoverage: "positive_only", CandidateRegionCoverage: "module_top_level_complete",
		CallSites: []CallSite{site}, CandidateRegionCount: 1,
		CandidateRegions: []CandidateRegion{{
			ID: legalityDigest("region"), Kind: CandidateRegionStraightLine,
			Span: SourceSpan{StartLine: 1, StartColumn: 0, EndLine: 1, EndColumn: 32}, ControlRegionID: site.ControlRegionID,
			ControlPredecessors: []string{}, DataDependencies: []RegionDataDependency{}, LiveIns: []string{}, LiveOuts: []string{},
			LiveInsCanonical: true, LiveOutsCanonical: true, Effects: EffectSummary{MayObserveLive: true, MaySuspend: true},
			CapabilityOccurrences: []string{site.ID}, Barriers: []BarrierCode{}, RejectionReasons: []CandidateRejection{},
		}},
	}
	_, encoded, err := analysis.Identity()
	if err != nil {
		t.Fatal(err)
	}
	return VerifiedAnalysis{analysisJSON: encoded}, site
}

type legalityPLMAdapter struct{}

func (*legalityPLMAdapter) Call(context.Context, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{"value":"ok"}`), nil
}

func (*legalityPLMAdapter) PLMValidatorIdentities() capability.PLMValidatorIdentities {
	return capability.PLMValidatorIdentities{
		Temporal: "semantic-plm-temporal.v1", ProviderNonInterference: "semantic-plm-provider.v1",
	}
}

func (*legalityPLMAdapter) ValidatePLM(_ context.Context, request capability.PLMValidationRequest) (capability.PLMValidationResult, error) {
	return capability.PLMValidationResult{
		Temporal: request.Certificate.Temporal, TemporalValid: true, ProviderNonInterferenceValid: true,
	}, nil
}

func (*legalityPLMAdapter) PLMProviderSessionIdentity(context.Context) string {
	return "semantic-provider-session.v1"
}

func legalityPLMPlan(t *testing.T, adapter *legalityPLMAdapter) *capability.Plan {
	t.Helper()
	registry := capability.NewRegistry()
	grant, err := capability.NewGrant(json.RawMessage(`{"principal":"test-plm"}`))
	if err != nil {
		t.Fatal(err)
	}
	spec := capability.Spec{
		Name: "sources.read", Version: "sources.read.plm.v1", Description: "Read one immutable PLM source.",
		EffectClass: capability.EffectExternalRead, Playback: capability.PlaybackLiveOnly, HandlerIdentity: "sources-read-plm-v1",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"key":{"type":"string"}},"required":["key"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"],"additionalProperties":false}`),
		Python:       &capability.PythonProjection{Module: "sources", Method: "read", Arguments: []string{"key"}},
		ReadOnly:     true, Idempotent: true,
		PLM: &capability.PLMContract{
			Version: capability.PLMContractVersionV1, Temporal: capability.TemporalImmutable, PrepareEffect: capability.PrepareSilentRead,
			Speculation: capability.SpeculationBudgeted, Failure: capability.FailureRetryAtLinearize, Authority: capability.AuthorityRecheckAtLinearize,
			Resource:          capability.ResourceReference{Namespace: "source", Argument: "key"},
			TemporalValidator: "semantic-plm-temporal.v1", ProviderNonInterferenceValidator: "semantic-plm-provider.v1",
			MaxResultBytes: 1 << 20, CostUnits: 1,
		},
	}
	if err := registry.Register(spec, grant, adapter); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 2})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func legalityTestPlan(t *testing.T, qualified bool) *capability.Plan {
	return legalityTestPlanWithHandler(t, qualified, capability.HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"value":"ok"}`), nil
	}))
}

func legalityTestPlanWithHandler(t *testing.T, qualified bool, handler capability.Handler) *capability.Plan {
	t.Helper()
	registry := capability.NewRegistry()
	grant, err := capability.NewGrant(json.RawMessage(`{"principal":"test"}`))
	if err != nil {
		t.Fatal(err)
	}
	spec := capability.Spec{
		Name: "sources.read", Version: "sources.read.v1", Description: "Read one source.",
		EffectClass: capability.EffectExternalRead, Playback: capability.PlaybackLiveOnly,
		HandlerIdentity: "sources-read-v1",
		InputSchema:     json.RawMessage(`{"type":"object","properties":{"key":{"type":"string"}},"required":["key"],"additionalProperties":false}`),
		OutputSchema:    json.RawMessage(`{"type":"object"}`),
		Python:          &capability.PythonProjection{Module: "sources", Method: "read", Arguments: []string{"key"}},
	}
	if qualified {
		spec.ReadOnly, spec.Idempotent = true, true
		spec.PreDispatch = &capability.PreDispatchContract{
			Resource:  capability.ResourceReference{Namespace: "source", Argument: "key"},
			Freshness: capability.FreshnessPlanEpoch, Unclaimed: capability.UnclaimedDiscardWithDisposition,
			Privacy: capability.PreDispatchPrivacyExactPartition, Coalescing: capability.PreDispatchCoalescingForbidden,
			MaxResultBytes: 1 << 20, CostUnits: 1,
		}
	}
	if err := registry.Register(spec, grant, handler); err != nil {
		t.Fatal(err)
	}
	plan, err := registry.Seal(capability.PlanConfig{MaxCalls: 2})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func legalityDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return fmt.Sprintf("sha256:%x", digest[:])
}

func hasRejection(decision Decision, reason RejectionReason) bool {
	for _, rejection := range decision.Rejections() {
		if rejection == reason {
			return true
		}
	}
	return false
}

package semantic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/streaming"
)

func TestStreamingSemanticPreDispatchStartsVisibleCallsConcurrentlyAndClaimsExactlyOnce(t *testing.T) {
	var active atomic.Int32
	var peak atomic.Int32
	release := make(chan struct{})
	plan := legalityTestPlanWithHandler(t, true, capability.HandlerFunc(func(_ context.Context, arguments json.RawMessage) (json.RawMessage, error) {
		current := active.Add(1)
		for observed := peak.Load(); current > observed && !peak.CompareAndSwap(observed, current); observed = peak.Load() {
		}
		<-release
		active.Add(-1)
		return json.RawMessage(`{"value":"ok"}`), nil
	}))
	verified, site := legalityVerifiedAnalysis(t, plan, true)
	decision := CanPreissue(verified, plan, site.ID, legalityContext())
	base, ok := decision.QualifiedCall()
	if !ok {
		t.Fatal("base call not qualified")
	}
	base.exclusiveDynamicCall = false
	budget, err := NewPreDispatchBudget(3)
	if err != nil {
		t.Fatal(err)
	}
	controller, err := NewStreamingSemanticPreDispatch(plan, budget, goroutineTestLauncher{})
	if err != nil {
		t.Fatal(err)
	}
	for index, key := range []string{"weather", "rail", "attractions"} {
		call := streamingTestCall(t, base, plan, index+1, key)
		added, err := controller.Add(context.Background(), call)
		if err != nil || !added {
			t.Fatalf("add %s: added=%t err=%v", key, added, err)
		}
	}
	for attempts := 0; peak.Load() != 3 && attempts < 10000; attempts++ {
		runtime.Gosched()
	}
	if peak.Load() != 3 {
		t.Fatalf("peak physical reads=%d", peak.Load())
	}
	close(release)
	for _, key := range []string{"weather", "rail", "attractions"} {
		arguments := json.RawMessage(`{"key":"` + key + `"}`)
		outcome, err := controller.Claim(context.Background(), "sources.read", arguments)
		if err != nil || outcome.Validate() != nil {
			t.Fatalf("claim %s: outcome=%+v err=%v", key, outcome, err)
		}
	}
	if _, err := controller.Claim(context.Background(), "sources.read", json.RawMessage(`{"key":"weather"}`)); !errors.Is(err, capability.ErrStagedObservationNotTargeted) {
		t.Fatalf("second weather claim error=%v", err)
	}
	if err := controller.Finalize(true); err != nil {
		t.Fatal(err)
	}
	snapshot := controller.Snapshot()
	if snapshot.PhysicalIssues != 3 || snapshot.PhysicalStarts != 3 || snapshot.PhysicalFinishes != 3 || snapshot.LogicalClaims != 3 || snapshot.Consumed != 3 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

func TestVerifiedSourceGenerationRejectsAggregateOversizeBeforeAnalysis(t *testing.T) {
	plan := legalityTestPlan(t, true)
	budget, _ := NewPreDispatchBudget(1)
	controller, _ := NewStreamingSemanticPreDispatch(plan, budget, &queuedLauncher{})
	admission, _ := NewStreamingPrefixAdmission(plan, controller, legalityContext())
	chunks := make(chan string, 2)
	chunks <- strings.Repeat("x", 1<<20)
	chunks <- "x"
	close(chunks)
	var analyses atomic.Uint32
	_, err := GenerateVerifiedSourceWithPreDispatch(context.Background(), VerifiedSourceGenerationConfig{
		Analyze: func(context.Context, string, Bindings, *capability.Plan) (VerifiedAnalysis, error) {
			analyses.Add(1)
			return VerifiedAnalysis{}, ErrUnverifiedAnalysis
		},
		Plan: plan, Admission: admission, SourceChunks: chunks,
		Bindings:            Bindings{ArtifactSHA256: legalityDigest("artifact"), ExecutionProfileSHA256: legalityDigest("profile"), ImportClosureSHA256: legalityDigest("imports"), CapabilityPlanSHA256: plan.Identity()},
		ShouldAnalyzePrefix: func(uint32, string) bool { return false },
	})
	if !errors.Is(err, streaming.ErrSourceTooLarge) || analyses.Load() != 0 {
		t.Fatalf("error=%v analyses=%d", err, analyses.Load())
	}
	if snapshot := admission.Snapshot(); snapshot.Complete || snapshot.PrefixCount != 0 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

func TestStreamingPrefixSealPromotesReadyRecordToFinalSource(t *testing.T) {
	plan := legalityTestPlan(t, true)
	prefixSource := "result = sources.read(\"profile\")\n"
	prefixVerified, prefixSiteID := streamingPrefixAnalysis(t, plan, prefixSource)
	budget, _ := NewPreDispatchBudget(1)
	launcher := &queuedLauncher{}
	controller, err := NewStreamingSemanticPreDispatch(plan, budget, launcher)
	if err != nil {
		t.Fatal(err)
	}
	admission, err := NewStreamingPrefixAdmission(plan, controller, legalityContext())
	if err != nil {
		t.Fatal(err)
	}
	if added, err := admission.AdmitVerifiedPrefix(context.Background(), prefixSource, prefixVerified); err != nil || added != 1 {
		t.Fatalf("admit added=%d err=%v", added, err)
	}
	launcher.RunAll()
	finalSource := prefixSource + "answer = result\n"
	if err := admission.SealFinalSource(finalSource); err != nil {
		t.Fatal(err)
	}
	entry := controller.entries[0]
	if entry.call.SourceSHA256() != digestText(finalSource) || entry.call.CallSiteID() != prefixSiteID ||
		entry.controller.claim.SourceSHA256 != digestText(finalSource) ||
		entry.controller.identity.SourceSHA256 != digestText(finalSource) ||
		entry.controller.identity.ClaimIdentitySHA256 != entry.call.ClaimIdentitySHA256() {
		t.Fatalf("call=%+v claim=%+v identity=%+v", entry.call, entry.controller.claim, entry.controller.identity)
	}
	outcome, err := controller.Claim(context.Background(), "sources.read", json.RawMessage(`{"key":"profile"}`))
	if err != nil || outcome.Validate() != nil {
		t.Fatalf("claim outcome=%+v err=%v", outcome, err)
	}
}

func TestStreamingPrefixAdmissionIssuesIntoUnifiedSplitPhaseTable(t *testing.T) {
	plan := legalityTestPlan(t, true)
	prefixSource := "result = sources.read(\"profile\")\n"
	prefixVerified, _ := streamingPrefixAnalysis(t, plan, prefixSource)
	broker, err := capability.NewBroker(capability.Config{RunIdentity: "unified-prefix", Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	table, err := capability.NewSplitPhaseTable(broker, capability.SplitPhaseLimits{MaxCalls: 1, MaxCostUnits: 1, MaxResultBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	admission, err := NewSplitPhasePrefixAdmission(plan, table, legalityContext())
	if err != nil {
		t.Fatal(err)
	}
	if added, err := admission.AdmitVerifiedPrefix(context.Background(), prefixSource, prefixVerified); err != nil || added != 1 {
		t.Fatalf("admit added=%d err=%v", added, err)
	}
	if err := admission.SealFinalSource(prefixSource + "answer = result\n"); err != nil {
		t.Fatal(err)
	}
	response, err := table.Materialize(context.Background(), "slot-s1c9-e1c32-1")
	if err != nil || !json.Valid(response) {
		t.Fatalf("response=%s err=%v", response, err)
	}
	if err := broker.Finalize(true); err != nil {
		t.Fatal(err)
	}
	if snapshot := table.Snapshot(); snapshot.Submitted != 1 || snapshot.Consumed != 1 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

func TestStreamingPrefixSealRejectsNonExtensionBeforePromotion(t *testing.T) {
	plan := legalityTestPlan(t, true)
	prefixSource := "result = sources.read(\"profile\")\n"
	prefixVerified, _ := streamingPrefixAnalysis(t, plan, prefixSource)
	budget, _ := NewPreDispatchBudget(1)
	launcher := &queuedLauncher{}
	controller, _ := NewStreamingSemanticPreDispatch(plan, budget, launcher)
	admission, _ := NewStreamingPrefixAdmission(plan, controller, legalityContext())
	if added, err := admission.AdmitVerifiedPrefix(context.Background(), prefixSource, prefixVerified); err != nil || added != 1 {
		t.Fatalf("admit added=%d err=%v", added, err)
	}
	launcher.RunAll()
	if err := admission.SealFinalSource("different = 1\n"); !errors.Is(err, ErrAnalysisBinding) {
		t.Fatalf("seal error=%v", err)
	}
	if snapshot := controller.Snapshot(); snapshot.SourceSealed || snapshot.LogicalClaims != 0 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

func streamingPrefixAnalysis(t *testing.T, plan *capability.Plan, prefixSource string) (VerifiedAnalysis, string) {
	t.Helper()
	verified, _ := legalityVerifiedAnalysis(t, plan, true)
	analysis, err := verified.Analysis()
	if err != nil {
		t.Fatal(err)
	}
	prefixSiteID := analysis.CallSites[0].ID
	analysis.SourceSHA256 = digestText(prefixSource)
	_, prefixJSON, err := analysis.Identity()
	if err != nil {
		t.Fatal(err)
	}
	return VerifiedAnalysis{analysisJSON: prefixJSON}, prefixSiteID
}

func TestStreamingSemanticPreDispatchClaimsIdenticalArgumentsInVerifiedSourceOrder(t *testing.T) {
	plan := legalityTestPlan(t, true)
	verified, site := legalityVerifiedAnalysis(t, plan, true)
	base, _ := CanPreissue(verified, plan, site.ID, legalityContext()).QualifiedCall()
	base.exclusiveDynamicCall = false
	first := base
	first.callSiteID = legalityDigest("identical-first")
	first.startLine, first.endLine = 1, 1
	first.budgetReservationSHA256 = legalityDigest("identical-first-reservation")
	second := base
	second.callSiteID = legalityDigest("identical-second")
	second.startLine, second.endLine = 2, 2
	second.budgetReservationSHA256 = legalityDigest("identical-second-reservation")
	budget, _ := NewPreDispatchBudget(2)
	launcher := &queuedLauncher{}
	controller, err := NewStreamingSemanticPreDispatch(plan, budget, launcher)
	if err != nil {
		t.Fatal(err)
	}
	for index, call := range []QualifiedCall{first, second} {
		if added, err := controller.Add(context.Background(), call); err != nil || !added {
			t.Fatalf("add %d=%t err=%v", index, added, err)
		}
	}
	launcher.RunAll()
	for index := 0; index < 2; index++ {
		outcome, err := controller.Claim(context.Background(), base.Capability(), base.CanonicalArguments())
		if err != nil || outcome.Validate() != nil {
			t.Fatalf("claim %d outcome=%+v err=%v", index, outcome, err)
		}
		if !controller.entries[index].claimed || controller.entries[index].controller.Snapshot().LogicalClaims != 1 {
			t.Fatalf("entry %d=%+v child=%+v", index, controller.entries[index], controller.entries[index].controller.Snapshot())
		}
	}
	if _, err := controller.Claim(context.Background(), base.Capability(), base.CanonicalArguments()); !errors.Is(err, capability.ErrStagedObservationNotTargeted) {
		t.Fatalf("third claim error=%v", err)
	}
	if snapshot := controller.Snapshot(); snapshot.LogicalClaims != 2 || snapshot.Consumed != 2 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

func TestStreamingSemanticPreDispatchDeduplicatesSameVisiblePrefixOccurrence(t *testing.T) {
	plan := legalityTestPlan(t, true)
	verified, site := legalityVerifiedAnalysis(t, plan, true)
	decision := CanPreissue(verified, plan, site.ID, legalityContext())
	call, _ := decision.QualifiedCall()
	call.exclusiveDynamicCall = false
	budget, _ := NewPreDispatchBudget(2)
	launcher := &queuedLauncher{}
	controller, err := NewStreamingSemanticPreDispatch(plan, budget, launcher)
	if err != nil {
		t.Fatal(err)
	}
	firstAdded, err := controller.Add(context.Background(), call)
	if err != nil || !firstAdded {
		t.Fatalf("first add=%t err=%v", firstAdded, err)
	}
	extended := call
	extended.sourceSHA256 = legalityDigest("same prefix plus later source")
	extended.callSiteID = legalityDigest("same occurrence in extended source")
	secondAdded, err := controller.Add(context.Background(), extended)
	if err != nil || secondAdded || launcher.Count() != 1 {
		t.Fatalf("second add=%t launches=%d err=%v", secondAdded, launcher.Count(), err)
	}
}

func TestStreamingSemanticPreDispatchSameCapabilityMismatchFailsClosed(t *testing.T) {
	plan := legalityTestPlan(t, true)
	verified, site := legalityVerifiedAnalysis(t, plan, true)
	decision := CanPreissue(verified, plan, site.ID, legalityContext())
	call, _ := decision.QualifiedCall()
	call.exclusiveDynamicCall = false
	budget, _ := NewPreDispatchBudget(1)
	launcher := &queuedLauncher{}
	controller, err := NewStreamingSemanticPreDispatch(plan, budget, launcher)
	if err != nil {
		t.Fatal(err)
	}
	if added, err := controller.Add(context.Background(), call); err != nil || !added {
		t.Fatalf("added=%t err=%v", added, err)
	}
	launcher.RunAll()
	if _, err := controller.Claim(context.Background(), call.Capability(), json.RawMessage(`{"key":"different"}`)); !errors.Is(err, ErrPreDispatchClaimMismatch) {
		t.Fatalf("claim mismatch error=%v", err)
	}
	if snapshot := controller.Snapshot(); snapshot.RejectedClaims != 1 || snapshot.LogicalClaims != 0 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

func TestStreamingSemanticPreDispatchFinalizesUnclaimedReadsWithTypedDisposition(t *testing.T) {
	plan := legalityTestPlan(t, true)
	verified, site := legalityVerifiedAnalysis(t, plan, true)
	base, _ := CanPreissue(verified, plan, site.ID, legalityContext()).QualifiedCall()
	base.exclusiveDynamicCall = false
	budget, _ := NewPreDispatchBudget(3)
	launcher := &queuedLauncher{}
	controller, err := NewStreamingSemanticPreDispatch(plan, budget, launcher)
	if err != nil {
		t.Fatal(err)
	}
	for index, key := range []string{"weather", "rail", "attractions"} {
		if added, err := controller.Add(context.Background(), streamingTestCall(t, base, plan, index+1, key)); err != nil || !added {
			t.Fatalf("add %s: added=%t err=%v", key, added, err)
		}
	}
	launcher.RunAll()
	if _, err := controller.Claim(context.Background(), "sources.read", json.RawMessage(`{"key":"weather"}`)); err != nil {
		t.Fatal(err)
	}
	if err := controller.Finalize(true); err != nil {
		t.Fatal(err)
	}
	if snapshot := controller.Snapshot(); snapshot.Consumed != 1 || snapshot.Orphaned != 2 || snapshot.LogicalClaims != 1 || snapshot.PhysicalFinishes != 3 {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

func TestCanPreissueStreamingPrefixAllowsOnlyStraightLineIndependentReadLookahead(t *testing.T) {
	plan := legalityTestPlan(t, true)
	verified, secondSite := streamingLookaheadAnalysis(t, plan, false)
	if _, ok := CanPreissue(verified, plan, secondSite.ID, legalityContext()).QualifiedCall(); ok {
		t.Fatal("ordinary pre-dispatch accepted a non-necessarily-reached call")
	}
	if _, ok := CanPreissueStreamingPrefix(verified, plan, secondSite.ID, legalityContext()).QualifiedCall(); !ok {
		t.Fatal("streaming prefix rejected straight-line independent read look-ahead")
	}
	unsafe, unsafeSite := streamingLookaheadAnalysis(t, plan, true)
	if _, ok := CanPreissueStreamingPrefix(unsafe, plan, unsafeSite.ID, legalityContext()).QualifiedCall(); ok {
		t.Fatal("streaming prefix crossed an opaque-control rejection")
	}
}

func TestCanPreissueStreamingPrefixCrossesOnlySafeFunctionDeclarations(t *testing.T) {
	plan := legalityTestPlan(t, true)
	verified, site := streamingLookaheadWithDeclaration(t, plan)
	if _, ok := CanPreissueStreamingPrefix(verified, plan, site.ID, legalityContext()).QualifiedCall(); !ok {
		t.Fatal("streaming prefix rejected a safe non-executing function declaration")
	}
	analysis, err := verified.Analysis()
	if err != nil {
		t.Fatal(err)
	}
	analysis.CandidateRegions[0].Barriers = []BarrierCode{BarrierUnsupportedControl}
	analysis.CandidateRegions[0].Effects.MayBeUnknown = true
	analysis.CandidateRegions[0].RejectionReasons = append(analysis.CandidateRegions[0].RejectionReasons, CandidateRejectUnknownEffect)
	sort.Slice(analysis.CandidateRegions[0].RejectionReasons, func(i, j int) bool {
		return analysis.CandidateRegions[0].RejectionReasons[i] < analysis.CandidateRegions[0].RejectionReasons[j]
	})
	_, encoded, err := analysis.Identity()
	if err != nil {
		t.Fatal(err)
	}
	unsafe := VerifiedAnalysis{analysisJSON: encoded}
	if _, ok := CanPreissueStreamingPrefix(unsafe, plan, site.ID, legalityContext()).QualifiedCall(); ok {
		t.Fatal("streaming prefix crossed an unsafe function declaration")
	}
	analysis, err = verified.Analysis()
	if err != nil {
		t.Fatal(err)
	}
	analysis.CandidateRegions[0].RejectionReasons = []CandidateRejection{CandidateRejectDeclaration}
	_, encoded, err = analysis.Identity()
	if err != nil {
		t.Fatal(err)
	}
	genericDeclaration := VerifiedAnalysis{analysisJSON: encoded}
	if _, ok := CanPreissueStreamingPrefix(genericDeclaration, plan, site.ID, legalityContext()).QualifiedCall(); ok {
		t.Fatal("streaming prefix crossed a generic declaration such as an import")
	}
}

func streamingLookaheadWithDeclaration(t *testing.T, plan *capability.Plan) (VerifiedAnalysis, CallSite) {
	t.Helper()
	verified, site := streamingLookaheadAnalysis(t, plan, false)
	analysis, err := verified.Analysis()
	if err != nil {
		t.Fatal(err)
	}
	control := analysis.CandidateRegions[0].ControlRegionID
	declarationID := legalityDigest("safe-declaration")
	declaration := CandidateRegion{
		ID: declarationID, Kind: CandidateRegionDeclaration,
		Span: SourceSpan{StartLine: 1, StartColumn: 0, EndLine: 1, EndColumn: 24}, ControlRegionID: control,
		ControlPredecessors: []string{}, DataDependencies: []RegionDataDependency{}, LiveIns: []string{}, LiveOuts: []string{},
		LiveInsCanonical: true, LiveOutsCanonical: true, Effects: EffectSummary{}, CapabilityOccurrences: []string{},
		Barriers: []BarrierCode{}, RejectionReasons: []CandidateRejection{CandidateRejectFunctionDeclaration},
	}
	for index := range analysis.CallSites {
		analysis.CallSites[index].Span.StartLine++
		analysis.CallSites[index].Span.EndLine++
	}
	for index := range analysis.CandidateRegions {
		analysis.CandidateRegions[index].Span.StartLine++
		analysis.CandidateRegions[index].Span.EndLine++
	}
	analysis.CandidateRegions[0].ControlPredecessors = []string{declarationID}
	analysis.CandidateRegions = append([]CandidateRegion{declaration}, analysis.CandidateRegions...)
	analysis.CandidateRegionCount = len(analysis.CandidateRegions)
	analysis.ModuleSpan = SourceSpan{StartLine: 1, StartColumn: 0, EndLine: 3, EndColumn: 30}
	site.Span.StartLine++
	site.Span.EndLine++
	_, encoded, err := analysis.Identity()
	if err != nil {
		t.Fatal(err)
	}
	return VerifiedAnalysis{analysisJSON: encoded}, site
}

func streamingLookaheadAnalysis(t *testing.T, plan *capability.Plan, opaqueControl bool) (VerifiedAnalysis, CallSite) {
	t.Helper()
	verified, first := legalityVerifiedAnalysis(t, plan, true)
	analysis, err := verified.Analysis()
	if err != nil {
		t.Fatal(err)
	}
	analysis.SourceSHA256 = legalityDigest("first and second source")
	analysis.ModuleSpan = SourceSpan{StartLine: 1, StartColumn: 0, EndLine: 2, EndColumn: 30}
	control := legalityDigest("streaming-control")
	first.ID = legalityDigest("streaming-first")
	first.ControlRegionID = control
	first.Span = SourceSpan{StartLine: 1, StartColumn: 8, EndLine: 1, EndColumn: 31}
	second := first
	second.ID = legalityDigest("streaming-second")
	second.Span = SourceSpan{StartLine: 2, StartColumn: 9, EndLine: 2, EndColumn: 29}
	second.NecessarilyReached = false
	second.CanonicalArguments = json.RawMessage(`{"key":"rail"}`)
	regionOne := CandidateRegion{
		ID: legalityDigest("streaming-region-one"), Kind: CandidateRegionStraightLine,
		Span: SourceSpan{StartLine: 1, StartColumn: 0, EndLine: 1, EndColumn: 31}, ControlRegionID: control,
		ControlPredecessors: []string{}, DataDependencies: []RegionDataDependency{}, LiveIns: []string{}, LiveOuts: []string{},
		LiveInsCanonical: true, LiveOutsCanonical: true, Effects: EffectSummary{MayObserveLive: true, MaySuspend: true},
		CapabilityOccurrences: []string{first.ID}, Barriers: []BarrierCode{}, RejectionReasons: []CandidateRejection{CandidateRejectMayRaise},
	}
	regionTwo := CandidateRegion{
		ID: legalityDigest("streaming-region-two"), Kind: CandidateRegionStraightLine,
		Span: SourceSpan{StartLine: 2, StartColumn: 0, EndLine: 2, EndColumn: 29}, ControlRegionID: control,
		ControlPredecessors: []string{regionOne.ID}, DataDependencies: []RegionDataDependency{}, LiveIns: []string{}, LiveOuts: []string{},
		LiveInsCanonical: true, LiveOutsCanonical: true,
		Effects: EffectSummary{MayObserveLive: true, MaySuspend: true}, CapabilityOccurrences: []string{second.ID},
		Barriers: []BarrierCode{}, RejectionReasons: []CandidateRejection{CandidateRejectMayRaise},
	}
	if opaqueControl {
		regionTwo.RejectionReasons = append(regionTwo.RejectionReasons, CandidateRejectOpaqueControl)
		sort.Slice(regionTwo.RejectionReasons, func(i, j int) bool { return regionTwo.RejectionReasons[i] < regionTwo.RejectionReasons[j] })
	}
	analysis.CallSites = []CallSite{first, second}
	sort.Slice(analysis.CallSites, func(i, j int) bool { return analysis.CallSites[i].ID < analysis.CallSites[j].ID })
	analysis.CandidateRegions = []CandidateRegion{regionOne, regionTwo}
	analysis.CandidateRegionCount = 2
	_, encoded, err := analysis.Identity()
	if err != nil {
		t.Fatal(err)
	}
	return VerifiedAnalysis{analysisJSON: encoded}, second
}

func streamingTestCall(t *testing.T, base QualifiedCall, plan *capability.Plan, line int, key string) QualifiedCall {
	t.Helper()
	arguments := json.RawMessage(`{"key":"` + key + `"}`)
	argumentsDigest := sha256.Sum256(arguments)
	qualification, ok := plan.PreDispatch(base.Capability())
	if !ok {
		t.Fatal("missing pre-dispatch qualification")
	}
	resourceSHA, err := resourceIdentity(qualification.Contract().Resource, arguments)
	if err != nil {
		t.Fatal(err)
	}
	call := base
	call.callSiteID = legalityDigest("site:" + key)
	call.sourceSHA256 = legalityDigest("prefix:" + key)
	call.canonicalArguments = arguments
	call.argumentsSHA256 = "sha256:" + hex.EncodeToString(argumentsDigest[:])
	call.resourceSHA256 = resourceSHA
	call.budgetReservationSHA256 = legalityDigest("reservation:" + key)
	call.startLine, call.startColumn = uint32(line), 0
	call.endLine, call.endColumn = uint32(line), uint32(20+len(key))
	call.exclusiveDynamicCall = false
	return call
}

func TestVerifiedSourceGenerationDoesNotBlockSourceCompletionOnPrefixAnalysis(t *testing.T) {
	plan := legalityTestPlan(t, true)
	budget, err := NewPreDispatchBudget(1)
	if err != nil {
		t.Fatal(err)
	}
	controller, err := NewStreamingSemanticPreDispatch(plan, budget, &queuedLauncher{})
	if err != nil {
		t.Fatal(err)
	}
	admission, err := NewStreamingPrefixAdmission(plan, controller, PreissueContext{
		StreamEpoch: "stream-test", WorkflowEpoch: "workflow-test", FreshnessEpoch: "fresh-test",
		ExpiryEpoch: "run-end", PrivacyPartition: "test", ParentLineageSHA256: legalityDigest("lineage"),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	chunks := make(chan string, 2)
	chunks <- "value = 1\n"
	chunks <- "result = value\n"
	close(chunks)
	sourceComplete := make(chan struct{}, 1)
	returned := make(chan error, 1)
	go func() {
		_, generationErr := GenerateVerifiedSourceWithPreDispatch(ctx, VerifiedSourceGenerationConfig{
			Plan: plan, Admission: admission, SourceChunks: chunks,
			Bindings: Bindings{ArtifactSHA256: legalityDigest("artifact"), ExecutionProfileSHA256: legalityDigest("profile"), ImportClosureSHA256: legalityDigest("imports"), CapabilityPlanSHA256: plan.Identity()},
			Analyze: func(callContext context.Context, _ string, _ Bindings, _ *capability.Plan) (VerifiedAnalysis, error) {
				<-callContext.Done()
				return VerifiedAnalysis{}, callContext.Err()
			},
			Observe: func(event VerifiedSourceGenerationEvent) {
				if event.Phase == "source_complete" {
					sourceComplete <- struct{}{}
				}
			},
		})
		returned <- generationErr
	}()
	select {
	case <-sourceComplete:
	case <-time.After(time.Second):
		t.Fatal("source generation waited for prefix analysis")
	}
	cancel()
	if err := <-returned; !errors.Is(err, context.Canceled) {
		t.Fatalf("generation error=%v", err)
	}
}

var _ capability.StagedObservationClaimer = (*StreamingSemanticPreDispatch)(nil)

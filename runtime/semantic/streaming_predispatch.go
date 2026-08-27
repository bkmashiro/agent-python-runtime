package semantic

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/streaming"
)

// StreamingPreDispatchSnapshot is body-free causal evidence for one Run-private
// dynamic controller. Counts are derived from its child physical operations.
type StreamingPreDispatchSnapshot struct {
	PhysicalIssues      uint32
	PhysicalStarts      uint32
	PhysicalFinishes    uint32
	LogicalClaims       uint32
	RejectedClaims      uint32
	Consumed            uint32
	Orphaned            uint32
	Cancelled           uint32
	Failed              uint32
	ReservedCostUnits   uint64
	ProviderCostUnits   uint64
	ReservedResultBytes uint64
	PhysicalResultBytes uint64
	SourceSealed        bool
	FinalSourceSHA256   string
}

type StreamingPrefixAdmissionSnapshot struct {
	PrefixCount        uint32
	SkippedPrefixCount uint32
	QualifiedCallCount uint32
	RejectedCallCount  uint32
	LastSourceSHA256   string
	Complete           bool
}

type StreamingPreDispatchEvent struct {
	Kind             string
	CallSiteID       string
	Capability       string
	ArgumentsSHA256  string
	OccurrenceSHA256 string
}

type StreamingPreDispatchObserver func(StreamingPreDispatchEvent)

type streamingPreDispatchEntry struct {
	occurrence string
	call       QualifiedCall
	controller *SemanticPreDispatch
	claimed    bool
}

// StreamingSemanticPreDispatch is installed before a streaming Guest starts.
// The Host may add only already-qualified calls as source prefixes become
// visible. It never receives or predicts not-yet-generated source.
type StreamingSemanticPreDispatch struct {
	mu                sync.Mutex
	plan              *capability.Plan
	budget            *PreDispatchBudget
	launcher          PreDispatchLauncher
	observe           StreamingPreDispatchObserver
	entries           []*streamingPreDispatchEntry
	seen              map[string]struct{}
	finalized         bool
	requireSourceSeal bool
	sourceSealed      bool
	finalSourceSHA256 string
	rejected          uint32
}

func NewStreamingSemanticPreDispatch(plan *capability.Plan, budget *PreDispatchBudget, launcher PreDispatchLauncher) (*StreamingSemanticPreDispatch, error) {
	return NewObservedStreamingSemanticPreDispatch(plan, budget, launcher, nil)
}

func NewObservedStreamingSemanticPreDispatch(plan *capability.Plan, budget *PreDispatchBudget, launcher PreDispatchLauncher, observe StreamingPreDispatchObserver) (*StreamingSemanticPreDispatch, error) {
	if plan == nil || plan.Identity() == "" || budget == nil || launcher == nil {
		return nil, ErrPreDispatchInvalid
	}
	return &StreamingSemanticPreDispatch{plan: plan, budget: budget, launcher: launcher, observe: observe, seen: make(map[string]struct{})}, nil
}

// Add starts one physical read before the corresponding source prefix is sent
// to the execution Guest. Re-analysis of an extended prefix is idempotent.
func (controller *StreamingSemanticPreDispatch) Add(ctx context.Context, call QualifiedCall) (bool, error) {
	if controller == nil || ctx == nil || !call.valid() || call.binding.PlanSHA256 != controller.plan.Identity() {
		return false, ErrPreDispatchInvalid
	}
	occurrence := call.prefixOccurrenceIdentity()
	if occurrence == "" {
		return false, ErrPreDispatchInvalid
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.finalized || controller.sourceSealed {
		return false, ErrPreDispatchInvalid
	}
	if _, exists := controller.seen[occurrence]; exists {
		return false, nil
	}
	child, err := newSemanticPreDispatch(call, controller.plan, controller.budget, false)
	if err != nil {
		return false, err
	}
	if controller.observe != nil {
		controller.observe(streamingControllerEvent("qualified", call, occurrence))
	}
	launcher := streamingObservedLauncher{delegate: controller.launcher, before: func() {
		if controller.observe != nil {
			controller.observe(streamingControllerEvent("issue", call, occurrence))
		}
	}}
	if err := child.Start(ctx, launcher); err != nil {
		return false, err
	}
	controller.seen[occurrence] = struct{}{}
	controller.entries = append(controller.entries, &streamingPreDispatchEntry{occurrence: occurrence, call: call.clone(), controller: child})
	return true, nil
}

func (controller *StreamingSemanticPreDispatch) Claim(ctx context.Context, capabilityName string, arguments json.RawMessage) (capability.StagedCapabilityOutcome, error) {
	if controller == nil {
		return capability.StagedCapabilityOutcome{}, ErrPreDispatchInvalid
	}
	controller.mu.Lock()
	if controller.requireSourceSeal && !controller.sourceSealed {
		controller.rejected++
		controller.mu.Unlock()
		return capability.StagedCapabilityOutcome{}, ErrPreDispatchInvalid
	}
	var selected *streamingPreDispatchEntry
	sameCapabilityPending := false
	for _, entry := range controller.entries {
		if entry.claimed {
			continue
		}
		if entry.call.capability == capabilityName {
			sameCapabilityPending = true
			if bytes.Equal(entry.call.canonicalArguments, arguments) {
				entry.claimed = true
				selected = entry
				break
			}
		}
	}
	if selected == nil && sameCapabilityPending {
		controller.rejected++
	}
	controller.mu.Unlock()
	if selected != nil {
		outcome, err := selected.controller.Claim(ctx, capabilityName, arguments)
		if err == nil && controller.observe != nil {
			controller.observe(streamingControllerEvent("claim", selected.call, selected.occurrence))
		}
		return outcome, err
	}
	if sameCapabilityPending {
		return capability.StagedCapabilityOutcome{}, ErrPreDispatchClaimMismatch
	}
	return capability.StagedCapabilityOutcome{}, capability.ErrStagedObservationNotTargeted
}

func (controller *StreamingSemanticPreDispatch) Finalize(success bool) error {
	if controller == nil {
		return ErrPreDispatchInvalid
	}
	controller.mu.Lock()
	if controller.finalized {
		controller.mu.Unlock()
		return nil
	}
	controller.finalized = true
	entries := append([]*streamingPreDispatchEntry(nil), controller.entries...)
	controller.mu.Unlock()
	errs := make([]error, 0, len(entries))
	for _, entry := range entries {
		errs = append(errs, entry.controller.Finalize(success))
	}
	return errors.Join(errs...)
}

func (controller *StreamingSemanticPreDispatch) Snapshot() StreamingPreDispatchSnapshot {
	if controller == nil {
		return StreamingPreDispatchSnapshot{}
	}
	controller.mu.Lock()
	entries := append([]*streamingPreDispatchEntry(nil), controller.entries...)
	snapshot := StreamingPreDispatchSnapshot{
		RejectedClaims: controller.rejected, SourceSealed: controller.sourceSealed,
		FinalSourceSHA256: controller.finalSourceSHA256,
	}
	controller.mu.Unlock()
	for _, entry := range entries {
		child := entry.controller.Snapshot()
		snapshot.PhysicalIssues += child.PhysicalIssues
		snapshot.PhysicalStarts += child.PhysicalStarts
		snapshot.PhysicalFinishes += child.PhysicalFinishes
		snapshot.LogicalClaims += child.LogicalClaims
		snapshot.RejectedClaims += child.RejectedClaims
		snapshot.ReservedCostUnits += child.ReservedCostUnits
		snapshot.ProviderCostUnits += child.ProviderCostUnits
		snapshot.ReservedResultBytes += child.ReservedResultBytes
		snapshot.PhysicalResultBytes += child.PhysicalResultBytes
		switch child.Disposition {
		case streaming.ObservationConsumed:
			snapshot.Consumed++
		case streaming.ObservationOrphaned:
			snapshot.Orphaned++
		case streaming.ObservationCancelled:
			snapshot.Cancelled++
		case streaming.ObservationFailed:
			snapshot.Failed++
		}
	}
	return snapshot
}

func (controller *StreamingSemanticPreDispatch) PlanIdentity() string {
	if controller == nil || controller.plan == nil {
		return ""
	}
	return controller.plan.Identity()
}

func (controller *StreamingSemanticPreDispatch) SealFinalSource(finalSHA string) error {
	if controller == nil || !digestPattern.MatchString(finalSHA) {
		return ErrPreDispatchInvalid
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.finalized || controller.sourceSealed {
		return ErrPreDispatchInvalid
	}
	promoted := make([]QualifiedCall, len(controller.entries))
	for index, entry := range controller.entries {
		call, err := promoteStreamingCall(entry.call, finalSHA)
		if err != nil {
			return err
		}
		promoted[index] = call
	}
	for index, entry := range controller.entries {
		if err := entry.controller.promoteFinalCall(promoted[index]); err != nil {
			return err
		}
		entry.call = promoted[index]
	}
	controller.sourceSealed = true
	controller.finalSourceSHA256 = finalSHA
	return nil
}

func (call QualifiedCall) prefixOccurrenceIdentity() string {
	if !call.valid() {
		return ""
	}
	value := fmt.Sprintf("pysolate.streaming-prefix-occurrence.v1\x00%s\x00%d:%d:%d:%d\x00%s",
		call.capability, call.startLine, call.startColumn, call.endLine, call.endColumn, call.argumentsSHA256)
	digest := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(digest[:])
}

var _ capability.StagedObservationClaimer = (*StreamingSemanticPreDispatch)(nil)

type streamingObservedLauncher struct {
	delegate PreDispatchLauncher
	before   func()
}

func (launcher streamingObservedLauncher) Launch(task func()) {
	launcher.before()
	launcher.delegate.Launch(task)
}

func streamingControllerEvent(kind string, call QualifiedCall, occurrence string) StreamingPreDispatchEvent {
	return StreamingPreDispatchEvent{
		Kind: kind, CallSiteID: call.callSiteID, Capability: call.capability,
		ArgumentsSHA256: call.argumentsSHA256, OccurrenceSHA256: occurrence,
	}
}

type streamingIssueSink interface {
	PlanIdentity() string
	Add(context.Context, QualifiedCall) (bool, error)
	SealFinalSource(string) error
	Finalize(bool) error
	Snapshot() StreamingPreDispatchSnapshot
}

// StreamingPrefixAdmission joins analyses of monotonically growing,
// actually-visible source prefixes to one Run-private issue sink.
type StreamingPrefixAdmission struct {
	mu                sync.Mutex
	plan              *capability.Plan
	controller        streamingIssueSink
	context           PreissueContext
	lastSource        string
	lastVisibleSource string
	seen              map[string]struct{}
	snapshot          StreamingPrefixAdmissionSnapshot
}

func NewStreamingPrefixAdmission(plan *capability.Plan, controller *StreamingSemanticPreDispatch, baseContext PreissueContext) (*StreamingPrefixAdmission, error) {
	probe := baseContext
	probe.BudgetReservationSHA256 = digestText("streaming-prefix-admission-probe")
	probe.RemainingPhysicalReads = 1
	if plan == nil || controller == nil || controller.PlanIdentity() != plan.Identity() || !probe.valid() {
		return nil, ErrPreDispatchInvalid
	}
	controller.mu.Lock()
	if controller.finalized || controller.sourceSealed || len(controller.entries) != 0 {
		controller.mu.Unlock()
		return nil, ErrPreDispatchInvalid
	}
	controller.requireSourceSeal = true
	controller.mu.Unlock()
	return newStreamingPrefixAdmission(plan, controller, baseContext)
}

func (admission *StreamingPrefixAdmission) AdmitVerifiedPrefix(ctx context.Context, source string, verified VerifiedAnalysis) (uint32, error) {
	if admission == nil || ctx == nil || source == "" {
		return 0, ErrPreDispatchInvalid
	}
	analysis, err := verified.Analysis()
	if err != nil {
		return 0, err
	}
	sourceDigest := sha256.Sum256([]byte(source))
	sourceSHA := "sha256:" + hex.EncodeToString(sourceDigest[:])
	admission.mu.Lock()
	defer admission.mu.Unlock()
	if analysis.SourceSHA256 != sourceSHA || admission.lastSource != "" && !bytes.HasPrefix([]byte(source), []byte(admission.lastSource)) ||
		admission.lastSource == "" && admission.lastVisibleSource != "" && !bytes.HasPrefix([]byte(source), []byte(admission.lastVisibleSource)) && !bytes.HasPrefix([]byte(admission.lastVisibleSource), []byte(source)) {
		return 0, ErrAnalysisBinding
	}
	sites := append([]CallSite(nil), analysis.CallSites...)
	sort.Slice(sites, func(left, right int) bool {
		if sites[left].Span.StartLine != sites[right].Span.StartLine {
			return sites[left].Span.StartLine < sites[right].Span.StartLine
		}
		return sites[left].Span.StartColumn < sites[right].Span.StartColumn
	})
	var added uint32
	var rejected uint32
	for _, site := range sites {
		occurrence := sitePrefixOccurrenceIdentity(site)
		if _, exists := admission.seen[occurrence]; exists {
			continue
		}
		preissueContext := admission.context
		preissueContext.BudgetReservationSHA256 = prefixBudgetReservation(preissueContext.StreamEpoch, site)
		preissueContext.RemainingPhysicalReads = 1
		decision := CanPreissueStreamingPrefix(verified, admission.plan, site.ID, preissueContext)
		call, ok := decision.QualifiedCall()
		if !ok {
			rejected++
			continue
		}
		wasAdded, err := admission.controller.Add(ctx, call)
		if err != nil {
			return added, err
		}
		if wasAdded {
			added++
			admission.seen[occurrence] = struct{}{}
		}
	}
	admission.lastSource = source
	admission.snapshot.PrefixCount++
	admission.snapshot.QualifiedCallCount += added
	admission.snapshot.RejectedCallCount += rejected
	if admission.lastVisibleSource == "" || len(source) >= len(admission.lastVisibleSource) {
		admission.snapshot.LastSourceSHA256 = sourceSHA
	}
	return added, nil
}

func (admission *StreamingPrefixAdmission) Snapshot() StreamingPrefixAdmissionSnapshot {
	if admission == nil {
		return StreamingPrefixAdmissionSnapshot{}
	}
	admission.mu.Lock()
	defer admission.mu.Unlock()
	return admission.snapshot
}

// RecordSkippedPrefix advances only the visible-source binding. It cannot add
// a call or spend authority; later exact analysis must still extend this source.
func (admission *StreamingPrefixAdmission) RecordSkippedPrefix(source string) error {
	if admission == nil || source == "" {
		return ErrPreDispatchInvalid
	}
	admission.mu.Lock()
	defer admission.mu.Unlock()
	base := admission.lastVisibleSource
	if base == "" {
		base = admission.lastSource
	}
	if admission.snapshot.Complete || base != "" && !strings.HasPrefix(source, base) {
		return ErrAnalysisBinding
	}
	digest := sha256.Sum256([]byte(source))
	admission.lastVisibleSource = source
	admission.snapshot.SkippedPrefixCount++
	admission.snapshot.LastSourceSHA256 = "sha256:" + hex.EncodeToString(digest[:])
	return nil
}

func (admission *StreamingPrefixAdmission) SealFinalSource(source string) error {
	if admission == nil || source == "" {
		return ErrPreDispatchInvalid
	}
	admission.mu.Lock()
	defer admission.mu.Unlock()
	base := admission.lastVisibleSource
	if base == "" {
		base = admission.lastSource
	}
	if admission.snapshot.Complete || base == "" || !strings.HasSuffix(base, "\n") || !strings.HasPrefix(source, base) {
		return ErrAnalysisBinding
	}
	finalSHA := digestText(source)
	if err := admission.controller.SealFinalSource(finalSHA); err != nil {
		return err
	}
	admission.snapshot.LastSourceSHA256 = finalSHA
	admission.snapshot.Complete = true
	return nil
}

func promoteStreamingCall(prefix QualifiedCall, finalSourceSHA256 string) (QualifiedCall, error) {
	promoted := prefix.clone()
	promoted.sourceSHA256 = finalSourceSHA256
	if !promoted.valid() {
		return QualifiedCall{}, ErrAnalysisBinding
	}
	return promoted, nil
}

func prefixBudgetReservation(streamEpoch string, site CallSite) string {
	argumentsDigest := sha256.Sum256(site.CanonicalArguments)
	value := fmt.Sprintf("pysolate.streaming-prefix-budget.v1\x00%s\x00%s\x00%d:%d:%d:%d\x00%x",
		streamEpoch, site.Capability, site.Span.StartLine, site.Span.StartColumn, site.Span.EndLine, site.Span.EndColumn, argumentsDigest[:])
	return digestText(value)
}

func sitePrefixOccurrenceIdentity(site CallSite) string {
	argumentsDigest := sha256.Sum256(site.CanonicalArguments)
	value := fmt.Sprintf("pysolate.streaming-prefix-occurrence.v1\x00%s\x00%d:%d:%d:%d\x00sha256:%x",
		site.Capability, site.Span.StartLine, site.Span.StartColumn, site.Span.EndLine, site.Span.EndColumn, argumentsDigest[:])
	return digestText(value)
}

func digestText(value string) string {
	digest := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(digest[:])
}

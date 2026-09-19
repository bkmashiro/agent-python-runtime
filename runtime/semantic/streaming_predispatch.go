package semantic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

var ErrPreDispatchInvalid = errors.New("invalid streaming PLM admission")

type StreamingPreDispatchSnapshot struct {
	PhysicalIssues, PhysicalStarts, PhysicalFinishes                               uint32
	LogicalClaims, RejectedClaims, Consumed, Orphaned, Cancelled, Failed           uint32
	ReservedCostUnits, ProviderCostUnits, ReservedResultBytes, PhysicalResultBytes uint64
	SourceSealed                                                                   bool
	FinalSourceSHA256                                                              string
}

type StreamingPrefixAdmissionSnapshot struct {
	PrefixCount, SkippedPrefixCount, QualifiedCallCount, RejectedCallCount uint32
	LastSourceSHA256                                                       string
	Complete                                                               bool
}

type streamingIssueSink interface {
	PlanIdentity() string
	Add(context.Context, QualifiedCall) (bool, error)
	SealFinalSource(string) error
	Finalize(bool) error
	Snapshot() StreamingPreDispatchSnapshot
}

type StreamingPrefixAdmission struct {
	mu                            sync.Mutex
	plan                          *capability.Plan
	controller                    streamingIssueSink
	context                       PreissueContext
	lastSource, lastVisibleSource string
	seen                          map[string]struct{}
	snapshot                      StreamingPrefixAdmissionSnapshot
}

func (admission *StreamingPrefixAdmission) AdmitVerifiedPrefix(ctx context.Context, source string, verified VerifiedAnalysis) (uint32, error) {
	if admission == nil || ctx == nil || source == "" {
		return 0, ErrPreDispatchInvalid
	}
	analysis, err := verified.Analysis()
	if err != nil {
		return 0, err
	}
	sourceSHA := digestText(source)
	admission.mu.Lock()
	defer admission.mu.Unlock()
	if analysis.SourceSHA256 != sourceSHA || admission.lastSource != "" && !strings.HasPrefix(source, admission.lastSource) || admission.lastSource == "" && admission.lastVisibleSource != "" && !strings.HasPrefix(source, admission.lastVisibleSource) && !strings.HasPrefix(admission.lastVisibleSource, source) {
		return 0, ErrAnalysisBinding
	}
	var added, rejected uint32
	for _, site := range analysis.CallSites {
		occurrence := sitePrefixOccurrenceIdentity(site)
		if _, ok := admission.seen[occurrence]; ok {
			continue
		}
		ctxn := admission.context
		ctxn.BudgetReservationSHA256 = prefixBudgetReservation(ctxn.StreamEpoch, site)
		ctxn.RemainingPhysicalReads = 1
		decision := CanPreissueStreamingPrefix(verified, admission.plan, site.ID, ctxn)
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
	admission.snapshot.LastSourceSHA256 = sourceSHA
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
	admission.lastVisibleSource = source
	admission.snapshot.SkippedPrefixCount++
	admission.snapshot.LastSourceSHA256 = digestText(source)
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

func digestText(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}
func prefixBudgetReservation(streamEpoch string, site CallSite) string {
	return digestText(fmt.Sprintf("pysolate.streaming-prefix-budget.v1\x00%s\x00%s\x00%d:%d:%d:%d\x00%s", streamEpoch, site.Capability, site.Span.StartLine, site.Span.StartColumn, site.Span.EndLine, site.Span.EndColumn, digestText(string(site.CanonicalArguments))))
}
func sitePrefixOccurrenceIdentity(site CallSite) string {
	return digestText(fmt.Sprintf("pysolate.streaming-prefix-occurrence.v1\x00%s\x00%d:%d:%d:%d\x00%s", site.Capability, site.Span.StartLine, site.Span.StartColumn, site.Span.EndLine, site.Span.EndColumn, digestText(string(site.CanonicalArguments))))
}

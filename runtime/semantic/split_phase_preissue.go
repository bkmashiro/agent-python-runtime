package semantic

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

type streamingPLMEntry struct {
	call    QualifiedCall
	slotID  string
	request []byte
}

// streamingPLMIssueSink lets prefix analysis prepare directly into the same
// SplitPhaseTable later used by whole-program PLM lowering.
type streamingPLMIssueSink struct {
	mu                sync.Mutex
	table             *capability.SplitPhaseTable
	entries           []streamingPLMEntry
	entriesBySlot     map[string]int
	sourceSealed      bool
	finalSourceSHA256 string
	finalized         bool
}

func newStreamingPrefixAdmission(plan *capability.Plan, sink streamingIssueSink, baseContext PreissueContext) (*StreamingPrefixAdmission, error) {
	probe := baseContext
	probe.BudgetReservationSHA256 = digestText("streaming-prefix-admission-probe")
	probe.RemainingPhysicalReads = 1
	if plan == nil || sink == nil || sink.PlanIdentity() != plan.Identity() || !probe.valid() {
		return nil, ErrPreDispatchInvalid
	}
	baseContext.BudgetReservationSHA256 = ""
	baseContext.RemainingPhysicalReads = 0
	return &StreamingPrefixAdmission{plan: plan, controller: sink, context: baseContext, seen: make(map[string]struct{})}, nil
}

// NewStreamingPLMPrefixAdmission creates the shared-owner path. Prefix analysis
// may prepare a qualified straight-line call, while the full-source PLM pass
// reuses the same occurrence slot and remains the only linearization owner.
func NewStreamingPLMPrefixAdmission(plan *capability.Plan, table *capability.SplitPhaseTable, baseContext PreissueContext) (*StreamingPrefixAdmission, error) {
	if plan == nil || table == nil || table.PlanIdentity() != plan.Identity() {
		return nil, ErrPreDispatchInvalid
	}
	sink := &streamingPLMIssueSink{table: table, entriesBySlot: make(map[string]int)}
	return newStreamingPrefixAdmission(plan, sink, baseContext)
}

func (sink *streamingPLMIssueSink) PlanIdentity() string {
	if sink == nil || sink.table == nil {
		return ""
	}
	return sink.table.PlanIdentity()
}

func (sink *streamingPLMIssueSink) Add(ctx context.Context, call QualifiedCall) (bool, error) {
	if sink == nil || ctx == nil || !call.valid() || call.binding.PlanSHA256 != sink.PlanIdentity() {
		return false, ErrPreDispatchInvalid
	}
	slotID, request, err := qualifiedPLMRequest(call)
	if err != nil {
		return false, err
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.finalized || sink.sourceSealed {
		return false, ErrPreDispatchInvalid
	}
	if index, exists := sink.entriesBySlot[slotID]; exists {
		existing := sink.entries[index]
		if existing.call.Capability() == call.Capability() && bytes.Equal(existing.call.CanonicalArguments(), call.CanonicalArguments()) {
			return false, nil
		}
		return false, ErrAnalysisBinding
	}
	if err := sink.table.PrepareRuntimePLM(ctx, slotID, request, call.SourceSHA256()); err != nil {
		return false, err
	}
	sink.entriesBySlot[slotID] = len(sink.entries)
	sink.entries = append(sink.entries, streamingPLMEntry{call: call.clone(), slotID: slotID, request: request})
	return true, nil
}

func (sink *streamingPLMIssueSink) SealFinalSource(finalSourceSHA256 string) error {
	if sink == nil || !digestPattern.MatchString(finalSourceSHA256) {
		return ErrPreDispatchInvalid
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.finalized || sink.sourceSealed {
		return ErrPreDispatchInvalid
	}
	if len(sink.entries) != 0 {
		promotions := make([]capability.RuntimePLMSourcePromotion, 0, len(sink.entries))
		for _, entry := range sink.entries {
			promotions = append(promotions, capability.RuntimePLMSourcePromotion{
				SlotID: entry.slotID, Request: append([]byte(nil), entry.request...), PrefixSourceIdentity: entry.call.SourceSHA256(),
			})
		}
		if err := sink.table.PromoteRuntimePLMSources(promotions, finalSourceSHA256); err != nil {
			return err
		}
	}
	sink.sourceSealed = true
	sink.finalSourceSHA256 = finalSourceSHA256
	return nil
}

func (sink *streamingPLMIssueSink) Finalize(success bool) error {
	if sink == nil || sink.table == nil {
		return ErrPreDispatchInvalid
	}
	sink.mu.Lock()
	if sink.finalized {
		sink.mu.Unlock()
		return nil
	}
	sink.finalized = true
	sink.mu.Unlock()
	return sink.table.Finalize(success)
}

func (sink *streamingPLMIssueSink) Snapshot() StreamingPreDispatchSnapshot {
	if sink == nil || sink.table == nil {
		return StreamingPreDispatchSnapshot{}
	}
	split := sink.table.Snapshot()
	sink.mu.Lock()
	sealed, finalSource := sink.sourceSealed, sink.finalSourceSHA256
	sink.mu.Unlock()
	return StreamingPreDispatchSnapshot{
		PhysicalIssues: split.Submitted, PhysicalStarts: split.PhysicalStarts, PhysicalFinishes: split.PhysicalFinishes,
		LogicalClaims: split.LogicalClaims, RejectedClaims: split.CandidatesRejected, Consumed: split.Consumed,
		Orphaned: split.Discarded, Cancelled: split.Cancelled, Failed: split.Failed,
		PhysicalResultBytes: split.PhysicalResultBytes, SourceSealed: sealed, FinalSourceSHA256: finalSource,
	}
}

// PrepareQualifiedPLM converts a semantic proof into the versioned Host PLM
// runtime identity. Prefix callers use the currently visible source identity and
// promote it atomically after the full source is known.
func PrepareQualifiedPLM(ctx context.Context, table *capability.SplitPhaseTable, call QualifiedCall) error {
	if ctx == nil || table == nil || !call.valid() || call.binding.PlanSHA256 != table.PlanIdentity() {
		return ErrPreDispatchInvalid
	}
	slotID, request, err := qualifiedPLMRequest(call)
	if err != nil {
		return err
	}
	if err := table.PrepareRuntimePLM(ctx, slotID, request, call.SourceSHA256()); err != nil {
		return err
	}
	return nil
}

func qualifiedPLMRequest(call QualifiedCall) (string, []byte, error) {
	baseSlot, _, ok := call.SplitPhaseSiteIDs()
	if !ok {
		return "", nil, ErrPreDispatchInvalid
	}
	siteID := strings.TrimPrefix(baseSlot, "slot-")
	dynamicSlot := baseSlot + "-1"
	request, err := json.Marshal(struct {
		CallID     string          `json:"call_id"`
		Capability string          `json:"capability"`
		Arguments  json.RawMessage `json:"arguments"`
	}{
		CallID: "plm-" + siteID + "-1", Capability: call.Capability(), Arguments: call.CanonicalArguments(),
	})
	if err != nil {
		return "", nil, ErrPreDispatchInvalid
	}
	return dynamicSlot, request, nil
}

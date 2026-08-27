package semantic

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

type splitPhaseIssueSink struct {
	mu       sync.Mutex
	plan     *capability.Plan
	table    *capability.SplitPhaseTable
	seen     map[string]struct{}
	sealed   bool
	finalSHA string
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

// NewSplitPhasePrefixAdmission keeps incremental source analysis while routing
// every admitted physical read into the final execution's split-phase table.
func NewSplitPhasePrefixAdmission(plan *capability.Plan, table *capability.SplitPhaseTable, baseContext PreissueContext) (*StreamingPrefixAdmission, error) {
	if plan == nil || table == nil || table.PlanIdentity() != plan.Identity() {
		return nil, ErrPreDispatchInvalid
	}
	sink := &splitPhaseIssueSink{plan: plan, table: table, seen: make(map[string]struct{})}
	return newStreamingPrefixAdmission(plan, sink, baseContext)
}

func (sink *splitPhaseIssueSink) PlanIdentity() string {
	if sink == nil || sink.plan == nil {
		return ""
	}
	return sink.plan.Identity()
}

func (sink *splitPhaseIssueSink) Add(ctx context.Context, call QualifiedCall) (bool, error) {
	if sink == nil || ctx == nil || !call.valid() || call.binding.PlanSHA256 != sink.PlanIdentity() {
		return false, ErrPreDispatchInvalid
	}
	occurrence := call.prefixOccurrenceIdentity()
	sink.mu.Lock()
	if sink.sealed {
		sink.mu.Unlock()
		return false, ErrPreDispatchInvalid
	}
	if _, exists := sink.seen[occurrence]; exists {
		sink.mu.Unlock()
		return false, nil
	}
	sink.mu.Unlock()
	if err := IssueQualifiedSplitPhase(ctx, sink.table, call); err != nil {
		return false, err
	}
	sink.mu.Lock()
	sink.seen[occurrence] = struct{}{}
	sink.mu.Unlock()
	return true, nil
}

func (sink *splitPhaseIssueSink) SealFinalSource(finalSHA string) error {
	if sink == nil || !digestPattern.MatchString(finalSHA) {
		return ErrPreDispatchInvalid
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.sealed {
		return ErrPreDispatchInvalid
	}
	sink.sealed = true
	sink.finalSHA = finalSHA
	return nil
}

func (sink *splitPhaseIssueSink) Finalize(success bool) error {
	if sink == nil || sink.table == nil {
		return ErrPreDispatchInvalid
	}
	return sink.table.Finalize(success)
}

func (sink *splitPhaseIssueSink) Snapshot() StreamingPreDispatchSnapshot {
	if sink == nil || sink.table == nil {
		return StreamingPreDispatchSnapshot{}
	}
	sink.mu.Lock()
	sealed, finalSHA := sink.sealed, sink.finalSHA
	sink.mu.Unlock()
	physical := sink.table.Snapshot()
	return StreamingPreDispatchSnapshot{
		PhysicalIssues: physical.Submitted, PhysicalStarts: physical.PhysicalStarts,
		PhysicalFinishes: physical.PhysicalFinishes, LogicalClaims: physical.LogicalClaims,
		Consumed: physical.Consumed, Orphaned: physical.Discarded,
		SourceSealed: sealed, FinalSourceSHA256: finalSHA,
	}
}

// IssueQualifiedSplitPhase moves one already-qualified source-time read into
// the same Run-private table later used by compiler-emitted runtime issue.
func IssueQualifiedSplitPhase(ctx context.Context, table *capability.SplitPhaseTable, call QualifiedCall) error {
	if ctx == nil || table == nil || !call.valid() || call.binding.PlanSHA256 != table.PlanIdentity() {
		return ErrPreDispatchInvalid
	}
	slot, callID, ok := call.SplitPhaseSiteIDs()
	if !ok {
		return ErrPreDispatchInvalid
	}
	request, err := json.Marshal(struct {
		CallID     string          `json:"call_id"`
		Capability string          `json:"capability"`
		Arguments  json.RawMessage `json:"arguments"`
	}{
		CallID: callID + "-1", Capability: call.Capability(), Arguments: call.CanonicalArguments(),
	})
	if err != nil {
		return ErrPreDispatchInvalid
	}
	if err := table.IssueOrReuse(ctx, slot+"-1", request); err != nil {
		return err
	}
	return nil
}

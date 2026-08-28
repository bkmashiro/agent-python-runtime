package semantic

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

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

// PrepareQualifiedPLM converts one complete-source semantic proof into the
// versioned Host PLM runtime identity. It is used only after SourceSHA256 is the
// final sealed source; pre-seal streaming candidates remain disabled.
func PrepareQualifiedPLM(ctx context.Context, table *capability.SplitPhaseTable, call QualifiedCall) error {
	if ctx == nil || table == nil || !call.valid() || call.binding.PlanSHA256 != table.PlanIdentity() {
		return ErrPreDispatchInvalid
	}
	baseSlot, _, ok := call.SplitPhaseSiteIDs()
	if !ok {
		return ErrPreDispatchInvalid
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
		return ErrPreDispatchInvalid
	}
	if err := table.PrepareRuntimePLM(ctx, dynamicSlot, request, call.SourceSHA256()); err != nil {
		return err
	}
	return nil
}

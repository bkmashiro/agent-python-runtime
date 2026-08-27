package semantic

import (
	"context"
	"encoding/json"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

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

package capability

import (
	"context"
	"errors"
	"fmt"

	"github.com/bkmashiro/agent-python-runtime/runtime/transaction"
)

const maxAbortSteps = 4096

type AbortCall struct {
	TransactionID string
	Operation     transaction.Operation
	Attempt       transaction.Attempt
}

// AbortHandler is Host-only. Generated or untrusted Guest code cannot obtain
// this interface or supply rollback/compensation arguments.
type AbortHandler interface {
	Rollback(context.Context, AbortCall) error
	Compensate(context.Context, AbortCall) error
}

func (binder *CoordinatorBinder) CurrentTransactionID() string {
	if binder == nil {
		return ""
	}
	binder.mu.Lock()
	defer binder.mu.Unlock()
	return binder.transactionID
}

func (binder *CoordinatorBinder) Inspect(autoCompensateTools map[string]bool) (transaction.Inspection, error) {
	if binder == nil {
		return transaction.Inspection{}, errors.New("transaction binder is nil")
	}
	binder.mu.Lock()
	defer binder.mu.Unlock()
	return binder.coordinator.Inspect(binder.transactionID, autoCompensateTools)
}

func (binder *CoordinatorBinder) FinalizeWorkflow() (transaction.Transaction, error) {
	if binder == nil {
		return transaction.Transaction{}, errors.New("transaction binder is nil")
	}
	binder.mu.Lock()
	defer binder.mu.Unlock()
	return binder.coordinator.FinalizeWorkflow(binder.transactionID)
}

func (binder *CoordinatorBinder) Abort(ctx context.Context, registry *Registry, autoCompensateTools map[string]bool, reason string) (transaction.Inspection, error) {
	if binder == nil || registry == nil || !validIdentifier(reason) || len(autoCompensateTools) > 1024 {
		return transaction.Inspection{}, errors.New("invalid transaction abort request")
	}
	binder.mu.Lock()
	defer binder.mu.Unlock()
	for stepIndex := 0; stepIndex < maxAbortSteps; stepIndex++ {
		if err := ctx.Err(); err != nil {
			return transaction.Inspection{}, err
		}
		binder.ordinal++
		requestDigest := digestBytes([]byte(fmt.Sprintf("host-abort\x00%s\x00%s\x00%d", binder.transactionID, reason, binder.ordinal)))
		step, err := binder.coordinator.BeginAbortStep(transaction.AbortStepRequest{
			TransactionID: binder.transactionID, AutoCompensateTools: autoCompensateTools,
			Ordinal: binder.ordinal, LeaseDuration: binder.leaseDuration, ProviderRequestDigest: requestDigest,
		})
		if err != nil {
			return transaction.Inspection{}, err
		}
		if step.Done {
			return binder.coordinator.Inspect(binder.transactionID, autoCompensateTools)
		}
		if step.Dispatch == nil {
			return transaction.Inspection{}, errors.New("transaction abort produced no dispatch")
		}
		registered, exists := registry.lookup(step.Dispatch.Operation.ToolID)
		handler, supportsAbort := registered.spec.Handler.(AbortHandler)
		dispatchErr := errors.New("Host handler does not implement required abort operation")
		if exists && supportsAbort {
			call := AbortCall{TransactionID: binder.transactionID, Operation: step.Dispatch.Operation, Attempt: step.Dispatch.Attempt}
			switch step.Dispatch.Attempt.Kind {
			case transaction.AttemptRollback:
				dispatchErr = handler.Rollback(ctx, call)
			case transaction.AttemptCompensate:
				dispatchErr = handler.Compensate(ctx, call)
			default:
				dispatchErr = errors.New("unsupported abort attempt kind")
			}
		}
		outcome := transaction.DispatchSucceeded
		if dispatchErr != nil {
			outcome = transaction.DispatchFailed
		}
		if _, completeErr := binder.coordinator.CompleteDispatch(transaction.CompleteDispatchRequest{
			OperationID: step.Dispatch.Operation.ID, AttemptID: step.Dispatch.Attempt.ID, Outcome: outcome,
		}); completeErr != nil {
			return transaction.Inspection{}, completeErr
		}
		if dispatchErr != nil {
			inspection, inspectErr := binder.coordinator.Inspect(binder.transactionID, autoCompensateTools)
			return inspection, errors.Join(dispatchErr, inspectErr)
		}
	}
	return transaction.Inspection{}, errors.New("transaction abort exceeded bounded step count")
}

func cloneAutoCompensate(source map[string]bool) map[string]bool {
	cloned := make(map[string]bool, len(source))
	for toolID, allowed := range source {
		cloned[toolID] = allowed
	}
	return cloned
}

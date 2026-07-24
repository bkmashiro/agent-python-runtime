package capability

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/transaction"
)

var ErrReplayRequiresReconciliation = errors.New("typed call replay requires transaction reconciliation")

type CoordinatorBinder struct {
	mu            sync.Mutex
	coordinator   *transaction.Coordinator
	transactionID string
	leaseDuration time.Duration
	ordinal       uint32
}

func NewCoordinatorBinder(coordinator *transaction.Coordinator, transactionID string, leaseDuration time.Duration) (*CoordinatorBinder, error) {
	if coordinator == nil || !validIdentifier(transactionID) || leaseDuration <= 0 || leaseDuration > 5*time.Minute {
		return nil, errors.New("invalid coordinator binder configuration")
	}
	_, err := coordinator.InspectTransaction(transactionID)
	if err != nil {
		return nil, errors.New("coordinator binder transaction does not exist")
	}
	return &CoordinatorBinder{coordinator: coordinator, transactionID: transactionID, leaseDuration: leaseDuration}, nil
}

func (binder *CoordinatorBinder) Begin(_ context.Context, call BoundCall) (BoundOperation, error) {
	binder.mu.Lock()
	defer binder.mu.Unlock()
	effectClass, effectAllowed := boundEffectClass(call.EffectClass)
	value, err := binder.coordinator.InspectTransaction(binder.transactionID)
	if err != nil || value.RunID != call.RunIdentity || value.CatalogDigest != call.CatalogDigest ||
		!effectAllowed || call.Policy != string(transaction.PolicyAutoCommit) ||
		!validIdentifier(call.ToolID) || !validIdentifier(call.HandlerVersion) || !validIdentifier(call.PolicyVersion) ||
		!catalogDigestPattern.MatchString(call.ArgumentsDigest) || !catalogDigestPattern.MatchString(call.RequestDigest) {
		return BoundOperation{}, errors.New("typed call does not match the Host transaction binding")
	}
	existing, findErr := binder.coordinator.FindDispatch(value.ID, call.RequestDigest)
	if findErr == nil {
		if existing.Attempt.State == transaction.AttemptDispatching {
			if _, markErr := binder.coordinator.CompleteDispatch(transaction.CompleteDispatchRequest{
				OperationID: existing.Operation.ID, AttemptID: existing.Attempt.ID, Outcome: transaction.DispatchAmbiguous,
			}); markErr != nil {
				return BoundOperation{}, errors.Join(ErrReplayRequiresReconciliation, markErr)
			}
		}
		return BoundOperation{}, ErrReplayRequiresReconciliation
	}
	if !errors.Is(findErr, transaction.ErrNotFound) {
		return BoundOperation{}, findErr
	}
	if value.State != transaction.TransactionOpen {
		return BoundOperation{}, errors.New("typed call transaction is not open")
	}
	operation, err := binder.coordinator.Propose(transaction.ProposeRequest{
		TransactionID: value.ID, ToolID: call.ToolID, HandlerVersion: call.HandlerVersion,
		EffectClass: effectClass, Policy: transaction.PolicyAutoCommit,
		PolicyVersion: call.PolicyVersion, ArgumentDigest: call.ArgumentsDigest,
	})
	if err != nil {
		return BoundOperation{}, err
	}
	binder.ordinal++
	dispatch, err := binder.coordinator.BeginDispatch(transaction.DispatchRequest{
		OperationID: operation.ID, Kind: transaction.AttemptApply, Ordinal: binder.ordinal,
		LeaseDuration: binder.leaseDuration, ProviderRequestDigest: call.RequestDigest,
	})
	if err != nil {
		return BoundOperation{}, err
	}
	return BoundOperation{
		TransactionID: value.ID, OperationID: dispatch.Operation.ID, AttemptID: dispatch.Attempt.ID,
		OperationIndex: dispatch.Operation.Index, ManifestDigest: dispatch.Operation.ManifestDigest,
	}, nil
}

func boundEffectClass(value string) (transaction.EffectClass, bool) {
	switch transaction.EffectClass(value) {
	case transaction.EffectReadOnly, transaction.EffectReversible, transaction.EffectCompensatable:
		return transaction.EffectClass(value), true
	default:
		return "", false
	}
}

func (binder *CoordinatorBinder) Complete(_ context.Context, bound BoundOperation, outcome BoundOutcome) error {
	binder.mu.Lock()
	defer binder.mu.Unlock()
	if bound.TransactionID != binder.transactionID || !validIdentifier(bound.OperationID) || !validIdentifier(bound.AttemptID) {
		return errors.New("bound completion identity mismatch")
	}
	dispatchOutcome := transaction.DispatchFailed
	if outcome.Status == StatusOK && outcome.ErrorCode == "" {
		dispatchOutcome = transaction.DispatchSucceeded
	}
	_, err := binder.coordinator.CompleteDispatch(transaction.CompleteDispatchRequest{
		OperationID: bound.OperationID, AttemptID: bound.AttemptID, Outcome: dispatchOutcome,
	})
	return err
}

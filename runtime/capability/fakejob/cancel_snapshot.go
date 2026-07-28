package fakejob

import (
	"fmt"
	"sort"

	"github.com/bkmashiro/agent-python-runtime/runtime/transaction"
)

const cancelControllerSnapshotVersion = 1

type CancelControllerSnapshot struct {
	SchemaVersion uint32                `json:"schema_version"`
	TransactionID string                `json:"transaction_id"`
	Ordinal       uint32                `json:"ordinal"`
	Stages        []CancelStageSnapshot `json:"stages"`
}

type CancelStageSnapshot struct {
	OperationID     string        `json:"operation_id"`
	ManifestDigest  string        `json:"manifest_digest"`
	JobID           string        `json:"job_id"`
	ExpectedVersion uint64        `json:"expected_version"`
	ApprovalDigest  string        `json:"approval_digest,omitempty"`
	AttemptID       string        `json:"attempt_id,omitempty"`
	Status          string        `json:"status"`
	Receipt         CancelReceipt `json:"receipt"`
}

func (controller *CancelController) Snapshot() (CancelControllerSnapshot, error) {
	if controller == nil {
		return CancelControllerSnapshot{}, ErrCancelReconciliation
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	return controller.snapshotLocked()
}

func (controller *CancelController) snapshotLocked() (CancelControllerSnapshot, error) {
	result := CancelControllerSnapshot{SchemaVersion: cancelControllerSnapshotVersion, TransactionID: controller.transactionID, Ordinal: controller.ordinal, Stages: make([]CancelStageSnapshot, 0, len(controller.staged))}
	for _, stage := range controller.staged {
		status := stage.status
		if status == "completing" {
			status = "ambiguous"
		}
		item := CancelStageSnapshot{OperationID: stage.public.OperationID, ManifestDigest: stage.public.ManifestDigest, JobID: stage.public.JobID, ExpectedVersion: stage.public.ExpectedVersion, ApprovalDigest: stage.approvalDigest, AttemptID: stage.attemptID, Status: status, Receipt: stage.receipt}
		if err := validateStageSnapshot(item); err != nil {
			return CancelControllerSnapshot{}, err
		}
		result.Stages = append(result.Stages, item)
	}
	sort.Slice(result.Stages, func(i, j int) bool { return result.Stages[i].OperationID < result.Stages[j].OperationID })
	return result, nil
}

func NewCancelControllerFromSnapshot(coordinator *transaction.Coordinator, adapter *Adapter, snapshot CancelControllerSnapshot) (*CancelController, error) {
	if coordinator == nil || adapter == nil || snapshot.SchemaVersion != cancelControllerSnapshotVersion || !validIdentity(snapshot.TransactionID) || len(snapshot.Stages) > maxJobs {
		return nil, ErrCancelReconciliation
	}
	inspectedTransaction, err := coordinator.InspectTransaction(snapshot.TransactionID)
	if err != nil || inspectedTransaction.ID != snapshot.TransactionID {
		return nil, ErrCancelReconciliation
	}
	controller := &CancelController{coordinator: coordinator, transactionID: snapshot.TransactionID, adapter: adapter, ordinal: snapshot.Ordinal, staged: make(map[string]*cancelStage, len(snapshot.Stages))}
	for _, item := range snapshot.Stages {
		if err := validateStageSnapshot(item); err != nil {
			return nil, err
		}
		if _, duplicate := controller.staged[item.OperationID]; duplicate {
			return nil, ErrCancelReconciliation
		}
		operation, inspectErr := coordinator.InspectOperation(item.OperationID)
		if inspectErr != nil || operation.TransactionID != snapshot.TransactionID || operation.ToolID != CancelToolID || operation.HandlerVersion != HandlerVersion || operation.EffectClass != transaction.EffectIrreversible || operation.Policy != transaction.PolicyUserApprovalRequired || operation.ManifestDigest != item.ManifestDigest {
			return nil, ErrCancelReconciliation
		}
		controller.staged[item.OperationID] = &cancelStage{public: StagedCancel{OperationID: item.OperationID, ManifestDigest: item.ManifestDigest, JobID: item.JobID, ExpectedVersion: item.ExpectedVersion}, approvalDigest: item.ApprovalDigest, attemptID: item.AttemptID, status: item.Status, receipt: item.Receipt}
	}
	return controller, nil
}

func validateStageSnapshot(item CancelStageSnapshot) error {
	if !validIdentity(item.OperationID) || !validDigest(item.ManifestDigest) || !validIdentity(item.JobID) || item.ExpectedVersion == 0 {
		return ErrCancelReconciliation
	}
	switch item.Status {
	case "awaiting_approval":
		if item.ApprovalDigest != "" || item.AttemptID != "" || item.Receipt != (CancelReceipt{}) {
			return ErrCancelReconciliation
		}
	case "ambiguous", "committed":
		if !validDigest(item.ApprovalDigest) || !validIdentity(item.AttemptID) || !validIdentity(item.Receipt.JobID) || item.Receipt.JobID != item.JobID || item.Receipt.JobVersion <= item.ExpectedVersion || !validDigest(item.Receipt.ReceiptDigest) {
			return ErrCancelReconciliation
		}
		expected := cancelDigest(fmt.Sprintf("cancel-receipt\x00%s\x00%d\x00%s", item.JobID, item.Receipt.JobVersion, item.ManifestDigest))
		if item.Receipt.ReceiptDigest != expected {
			return ErrCancelReconciliation
		}
	default:
		return ErrCancelReconciliation
	}
	return nil
}

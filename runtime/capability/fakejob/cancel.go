package fakejob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/transaction"
)

const CancelToolID = "job.cancel"

var (
	ErrCancelApprovalRequired = errors.New("fake job cancel approval required")
	ErrCancelReconciliation   = errors.New("fake job cancel requires reconciliation")
)

type StagedCancel struct {
	OperationID     string `json:"operation_id"`
	ManifestDigest  string `json:"manifest_digest"`
	JobID           string `json:"job_id"`
	ExpectedVersion uint64 `json:"expected_version"`
}

type CancelReceipt struct {
	JobID         string `json:"job_id"`
	JobVersion    uint64 `json:"job_version"`
	ReceiptDigest string `json:"receipt_digest"`
}

type cancelStage struct {
	public         StagedCancel
	approvalDigest string
	attemptID      string
	status         string
	receipt        CancelReceipt
}

type CancelController struct {
	mu            sync.Mutex
	coordinator   *transaction.Coordinator
	transactionID string
	adapter       *Adapter
	ordinal       uint32
	staged        map[string]*cancelStage
}

func NewCancelController(coordinator *transaction.Coordinator, transactionID string, adapter *Adapter) (*CancelController, error) {
	if coordinator == nil || adapter == nil || !validIdentity(transactionID) {
		return nil, ErrCancelApprovalRequired
	}
	if _, err := coordinator.InspectTransaction(transactionID); err != nil {
		return nil, ErrCancelApprovalRequired
	}
	return &CancelController{coordinator: coordinator, transactionID: transactionID, adapter: adapter, staged: map[string]*cancelStage{}}, nil
}

func (controller *CancelController) Prepare(ctx context.Context, jobID string, expectedVersion uint64) (StagedCancel, error) {
	if !validIdentity(jobID) || expectedVersion == 0 {
		return StagedCancel{}, ErrJobDenied
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if len(controller.staged) >= maxJobs {
		return StagedCancel{}, ErrJobDenied
	}
	var planned Job
	err := controller.adapter.withSecret(ctx, controller.adapter.config.ReadSecretRef, CancelToolID, "prepare", func(secret []byte) error {
		var e error
		planned, e = controller.adapter.config.Provider.planCancel(secret, jobID, expectedVersion)
		return e
	})
	if err != nil {
		return StagedCancel{}, err
	}
	argumentDigest := cancelDigest(fmt.Sprintf("cancel\x00%s\x00%d\x00%s", jobID, expectedVersion, planned.RecipeDigest))
	operation, err := controller.coordinator.Propose(transaction.ProposeRequest{TransactionID: controller.transactionID, ToolID: CancelToolID, HandlerVersion: HandlerVersion, EffectClass: transaction.EffectIrreversible, Policy: transaction.PolicyUserApprovalRequired, PolicyVersion: controller.adapter.config.PolicyVersion, ArgumentDigest: argumentDigest})
	if err != nil {
		return StagedCancel{}, err
	}
	stage := &cancelStage{public: StagedCancel{OperationID: operation.ID, ManifestDigest: operation.ManifestDigest, JobID: jobID, ExpectedVersion: expectedVersion}, status: "awaiting_approval"}
	controller.staged[operation.ID] = stage
	return stage.public, nil
}

func (controller *CancelController) RegisterApproval(credential transaction.CommitCredential, operationID, authorityID, actorID string, expiresAt time.Time) error {
	controller.mu.Lock()
	stage, exists := controller.staged[operationID]
	controller.mu.Unlock()
	if !exists {
		return ErrCancelApprovalRequired
	}
	_, err := controller.coordinator.RegisterApproval(credential, transaction.AuthorityClaims{AuthorityID: authorityID, TransactionID: controller.transactionID, OperationID: operationID, ManifestDigest: stage.public.ManifestDigest, Source: transaction.CommitSourceUser, ActorID: actorID, ExpiresAt: expiresAt})
	return err
}

func (controller *CancelController) Commit(ctx context.Context, credential transaction.CommitCredential, operationID string) (CancelReceipt, error) {
	controller.mu.Lock()
	stage, exists := controller.staged[operationID]
	if !exists {
		controller.mu.Unlock()
		return CancelReceipt{}, ErrCancelApprovalRequired
	}
	credentialDigest := cancelDigest("approval\x00" + credential.Token)
	if stage.status == "committed" {
		if stage.approvalDigest != credentialDigest {
			controller.mu.Unlock()
			return CancelReceipt{}, ErrCancelApprovalRequired
		}
		receipt := stage.receipt
		controller.mu.Unlock()
		return receipt, nil
	}
	if stage.status == "ambiguous" {
		controller.mu.Unlock()
		return CancelReceipt{}, ErrCancelReconciliation
	}
	controller.mu.Unlock()
	operation, err := controller.coordinator.Authorize(credential)
	if err != nil || operation.ID != operationID || operation.ManifestDigest != stage.public.ManifestDigest {
		return CancelReceipt{}, ErrCancelApprovalRequired
	}
	controller.mu.Lock()
	controller.ordinal++
	ordinal := controller.ordinal
	controller.mu.Unlock()
	dispatch, err := controller.coordinator.BeginDispatch(transaction.DispatchRequest{OperationID: operationID, Kind: transaction.AttemptApply, Ordinal: ordinal, LeaseDuration: controller.adapter.config.LeaseDuration, ProviderRequestDigest: stage.public.ManifestDigest})
	if err != nil {
		return CancelReceipt{}, err
	}
	var receipt CancelReceipt
	var ambiguous bool
	err = controller.adapter.withSecret(ctx, controller.adapter.config.ControlSecretRef, CancelToolID, "commit", func(secret []byte) error {
		var e error
		receipt, ambiguous, e = controller.adapter.config.Provider.cancelExact(secret, stage.public.JobID, stage.public.ExpectedVersion, stage.public.ManifestDigest)
		return e
	})
	if err != nil {
		_, _ = controller.coordinator.CompleteDispatch(transaction.CompleteDispatchRequest{OperationID: operationID, AttemptID: dispatch.Attempt.ID, Outcome: transaction.DispatchFailed})
		return CancelReceipt{}, err
	}
	controller.mu.Lock()
	stage.attemptID = dispatch.Attempt.ID
	stage.receipt = receipt
	stage.approvalDigest = credentialDigest
	if ambiguous {
		stage.status = "ambiguous"
	} else {
		stage.status = "completing"
	}
	controller.mu.Unlock()
	if ambiguous {
		_, completeErr := controller.coordinator.CompleteDispatch(transaction.CompleteDispatchRequest{OperationID: operationID, AttemptID: dispatch.Attempt.ID, Outcome: transaction.DispatchAmbiguous})
		return CancelReceipt{}, errors.Join(ErrCancelReconciliation, completeErr)
	}
	_, err = controller.coordinator.CompleteAuthorizedDispatch(credential, transaction.CompleteDispatchRequest{OperationID: operationID, AttemptID: dispatch.Attempt.ID, Outcome: transaction.DispatchSucceeded, ProviderReceiptDigest: receipt.ReceiptDigest})
	if err != nil {
		controller.mu.Lock()
		stage.status = "ambiguous"
		controller.mu.Unlock()
		return CancelReceipt{}, errors.Join(ErrCancelReconciliation, err)
	}
	controller.mu.Lock()
	stage.status = "committed"
	controller.mu.Unlock()
	return receipt, nil
}

func (controller *CancelController) Reconcile(ctx context.Context, credential transaction.CommitCredential, operationID string) (CancelReceipt, error) {
	controller.mu.Lock()
	stage, exists := controller.staged[operationID]
	if !exists || stage.status != "ambiguous" || stage.approvalDigest != cancelDigest("approval\x00"+credential.Token) {
		controller.mu.Unlock()
		return CancelReceipt{}, ErrCancelReconciliation
	}
	attemptID := stage.attemptID
	receipt := stage.receipt
	controller.mu.Unlock()
	err := controller.adapter.withSecret(ctx, controller.adapter.config.ReadSecretRef, CancelToolID, "reconcile", func(secret []byte) error { return controller.adapter.config.Provider.observeCanceled(secret, receipt) })
	if err != nil {
		return CancelReceipt{}, ErrCancelReconciliation
	}
	_, err = controller.coordinator.ReconcileAuthorizedDispatch(credential, transaction.ReconcileDispatchRequest{
		OperationID: operationID, AttemptID: attemptID, Outcome: transaction.DispatchSucceeded,
		ProviderReceiptDigest: receipt.ReceiptDigest,
		ObservationDigest:     cancelDigest(fmt.Sprintf("fakejob.readback/v1\x00%s\x00%d\x00%s\x00%s", receipt.JobID, receipt.JobVersion, stage.public.ManifestDigest, receipt.ReceiptDigest)),
	})
	if err != nil {
		return CancelReceipt{}, err
	}
	controller.mu.Lock()
	stage.status = "committed"
	controller.mu.Unlock()
	return receipt, nil
}

func (provider *Provider) SetAmbiguousNextCancel() {
	provider.mu.Lock()
	provider.ambiguousNextCancel = true
	provider.mu.Unlock()
}
func (provider *Provider) planCancel(credential []byte, id string, expectedVersion uint64) (Job, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if !sameSecret(credential, provider.readCredential) {
		return Job{}, ErrCredentialDenied
	}
	state := provider.jobs[id]
	if state == nil || state.job.Version != expectedVersion {
		return Job{}, ErrJobDrift
	}
	if state.job.Status != "queued" && state.job.Status != "running" {
		return Job{}, ErrJobTerminal
	}
	return state.job, nil
}
func (provider *Provider) cancelExact(credential []byte, id string, expectedVersion uint64, manifest string) (CancelReceipt, bool, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if !sameSecret(credential, provider.controlCredential) {
		return CancelReceipt{}, false, ErrCredentialDenied
	}
	state := provider.jobs[id]
	if state == nil || state.job.Version != expectedVersion {
		return CancelReceipt{}, false, ErrJobDrift
	}
	if state.job.Status != "queued" && state.job.Status != "running" {
		return CancelReceipt{}, false, ErrJobTerminal
	}
	state.job.Status = "canceled"
	state.job.Version = provider.allocateVersion()
	receipt := CancelReceipt{JobID: id, JobVersion: state.job.Version}
	receipt.ReceiptDigest = cancelDigest(fmt.Sprintf("cancel-receipt\x00%s\x00%d\x00%s", id, state.job.Version, manifest))
	ambiguous := provider.ambiguousNextCancel
	provider.ambiguousNextCancel = false
	return receipt, ambiguous, nil
}
func (provider *Provider) observeCanceled(credential []byte, receipt CancelReceipt) error {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if !sameSecret(credential, provider.readCredential) {
		return ErrCredentialDenied
	}
	state := provider.jobs[receipt.JobID]
	if state == nil || state.job.Status != "canceled" || state.job.Version != receipt.JobVersion || !validDigest(receipt.ReceiptDigest) {
		return ErrJobDrift
	}
	return nil
}
func cancelDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

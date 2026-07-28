package fakemail

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strings"

	"github.com/bkmashiro/agent-python-runtime/runtime/devsnapshot"
	"github.com/bkmashiro/agent-python-runtime/runtime/transaction"
)

const mailSnapshotVersion = 1

type ProviderSnapshot struct {
	SchemaVersion uint32        `json:"schema_version"`
	Digest        string        `json:"digest"`
	NextDraft     uint64        `json:"next_draft"`
	NextVersion   uint64        `json:"next_version"`
	NextMessage   uint64        `json:"next_message"`
	Messages      []Message     `json:"messages"`
	Drafts        []Draft       `json:"drafts"`
	Sent          []SendReceipt `json:"sent"`
}
type SendControllerSnapshot struct {
	SchemaVersion uint32              `json:"schema_version"`
	Digest        string              `json:"digest"`
	TransactionID string              `json:"transaction_id"`
	Ordinal       uint32              `json:"ordinal"`
	Stages        []SendStageSnapshot `json:"stages"`
}
type SendStageSnapshot struct {
	Public         StagedSend  `json:"public"`
	Request        SendRequest `json:"request"`
	ApprovalDigest string      `json:"approval_digest,omitempty"`
	AttemptID      string      `json:"attempt_id,omitempty"`
	Status         string      `json:"status"`
	Receipt        SendReceipt `json:"receipt"`
}

func (provider *Provider) ExportSnapshot() (ProviderSnapshot, error) {
	if provider == nil {
		return ProviderSnapshot{}, ErrMailboxDenied
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.snapshotLocked()
}
func (provider *Provider) snapshotLocked() (ProviderSnapshot, error) {
	result := ProviderSnapshot{SchemaVersion: mailSnapshotVersion, NextDraft: provider.nextDraft, NextVersion: provider.nextVersion, NextMessage: provider.nextMessage, Messages: make([]Message, 0, len(provider.messages)), Drafts: make([]Draft, 0, len(provider.drafts)), Sent: make([]SendReceipt, 0, len(provider.sent))}
	for _, message := range provider.messages {
		result.Messages = append(result.Messages, cloneMessage(message))
	}
	for _, draft := range provider.drafts {
		result.Drafts = append(result.Drafts, cloneDraft(draft))
	}
	for _, receipt := range provider.sent {
		result.Sent = append(result.Sent, receipt)
	}
	sort.Slice(result.Messages, func(i, j int) bool { return result.Messages[i].ID < result.Messages[j].ID })
	sort.Slice(result.Drafts, func(i, j int) bool { return result.Drafts[i].ID < result.Drafts[j].ID })
	sort.Slice(result.Sent, func(i, j int) bool { return result.Sent[i].ManifestDigest < result.Sent[j].ManifestDigest })
	if validateProviderSnapshot(result, false) != nil {
		return ProviderSnapshot{}, ErrMailboxDenied
	}
	result.Digest = mailDigest(result)
	return result, nil
}
func NewProviderFromSnapshot(snapshot ProviderSnapshot, readCredential, draftCredential, sendCredential []byte) (*Provider, error) {
	if validateProviderSnapshot(snapshot, true) != nil {
		return nil, ErrMailboxDenied
	}
	provider, err := NewProvider(snapshot.Messages, readCredential, draftCredential, sendCredential)
	if err != nil {
		return nil, err
	}
	provider.nextDraft = snapshot.NextDraft
	provider.nextVersion = snapshot.NextVersion
	provider.nextMessage = snapshot.NextMessage
	for _, draft := range snapshot.Drafts {
		provider.drafts[draft.ID] = cloneDraft(draft)
	}
	for _, receipt := range snapshot.Sent {
		provider.sent[receipt.ManifestDigest] = receipt
	}
	return provider, nil
}
func validateProviderSnapshot(snapshot ProviderSnapshot, requireDigest bool) error {
	if snapshot.SchemaVersion != mailSnapshotVersion || len(snapshot.Messages) > 10000 || len(snapshot.Drafts) > maxTrackedState || len(snapshot.Sent) > maxTrackedState || snapshot.NextVersion == 0 {
		return ErrMailboxDenied
	}
	if requireDigest && snapshot.Digest != mailDigest(snapshot) {
		return ErrMailboxDenied
	}
	last := ""
	var maxVersion uint64
	for _, message := range snapshot.Messages {
		if !validMessage(message) || message.ID <= last {
			return ErrMailboxDenied
		}
		last = message.ID
		if message.Version > maxVersion {
			maxVersion = message.Version
		}
	}
	last = ""
	for _, draft := range snapshot.Drafts {
		if !validIdentity(draft.ID) || !validMailPayload(draft.To, draft.Subject, draft.Body) || draft.Version == 0 || draft.ID <= last {
			return ErrMailboxDenied
		}
		last = draft.ID
		if draft.Version > maxVersion {
			maxVersion = draft.Version
		}
	}
	last = ""
	for _, receipt := range snapshot.Sent {
		if !validSendReceipt(receipt) || receipt.ManifestDigest <= last {
			return ErrMailboxDenied
		}
		last = receipt.ManifestDigest
	}
	if snapshot.NextVersion <= maxVersion {
		return ErrMailboxDenied
	}
	return nil
}

func (controller *SendController) Snapshot() (SendControllerSnapshot, error) {
	if controller == nil {
		return SendControllerSnapshot{}, ErrSendReconciliation
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	return controller.snapshotLocked()
}
func (controller *SendController) snapshotLocked() (SendControllerSnapshot, error) {
	result := SendControllerSnapshot{SchemaVersion: mailSnapshotVersion, TransactionID: controller.transactionID, Ordinal: controller.ordinal, Stages: make([]SendStageSnapshot, 0, len(controller.staged))}
	for _, stage := range controller.staged {
		status := stage.status
		if status == "completing" {
			status = "ambiguous"
		}
		item := SendStageSnapshot{Public: stage.public, Request: SendRequest{To: append([]string(nil), stage.request.To...), Subject: stage.request.Subject, Body: stage.request.Body}, ApprovalDigest: stage.approvalDigest, AttemptID: stage.attemptID, Status: status, Receipt: stage.receipt}
		if validateSendStage(item) != nil {
			return SendControllerSnapshot{}, ErrSendReconciliation
		}
		result.Stages = append(result.Stages, item)
	}
	sort.Slice(result.Stages, func(i, j int) bool { return result.Stages[i].Public.OperationID < result.Stages[j].Public.OperationID })
	result.Digest = mailDigest(result)
	return result, nil
}
func NewSendControllerFromSnapshot(coordinator *transaction.Coordinator, adapter *Adapter, snapshot SendControllerSnapshot) (*SendController, error) {
	if coordinator == nil || adapter == nil || snapshot.SchemaVersion != mailSnapshotVersion || snapshot.Digest != mailDigest(snapshot) || !validIdentity(snapshot.TransactionID) || len(snapshot.Stages) > maxTrackedState {
		return nil, ErrSendReconciliation
	}
	tx, err := coordinator.InspectTransaction(snapshot.TransactionID)
	if err != nil || tx.ID != snapshot.TransactionID {
		return nil, ErrSendReconciliation
	}
	controller := &SendController{coordinator: coordinator, transactionID: snapshot.TransactionID, adapter: adapter, ordinal: snapshot.Ordinal, staged: map[string]*stagedSend{}}
	for _, item := range snapshot.Stages {
		if validateSendStage(item) != nil {
			return nil, ErrSendReconciliation
		}
		operation, err := coordinator.InspectOperation(item.Public.OperationID)
		if err != nil || operation.TransactionID != snapshot.TransactionID || operation.ToolID != SendToolID || operation.HandlerVersion != HandlerVersion || operation.ManifestDigest != item.Public.ManifestDigest || operation.EffectClass != transaction.EffectIrreversible || operation.Policy != transaction.PolicyUserApprovalRequired {
			return nil, ErrSendReconciliation
		}
		controller.staged[item.Public.OperationID] = &stagedSend{public: item.Public, request: SendRequest{To: append([]string(nil), item.Request.To...), Subject: item.Request.Subject, Body: item.Request.Body}, approvalDigest: item.ApprovalDigest, attemptID: item.AttemptID, status: item.Status, receipt: item.Receipt}
	}
	return controller, nil
}
func validateSendStage(item SendStageSnapshot) error {
	if !validIdentity(item.Public.OperationID) || !validDigest(item.Public.ManifestDigest) || !validMailPayload(item.Request.To, item.Request.Subject, item.Request.Body) || item.Public.RecipientDigest != digest([]byte(strings.Join(item.Request.To, "\x00"))) || item.Public.PayloadDigest != digest([]byte(item.Request.Subject+"\x00"+item.Request.Body)) {
		return ErrSendReconciliation
	}
	switch item.Status {
	case "awaiting_approval":
		if item.ApprovalDigest != "" || item.AttemptID != "" || item.Receipt != (SendReceipt{}) {
			return ErrSendReconciliation
		}
	case "ambiguous", "committed":
		if !validDigest(item.ApprovalDigest) || !validIdentity(item.AttemptID) || !validSendReceipt(item.Receipt) || item.Receipt.ManifestDigest != item.Public.ManifestDigest {
			return ErrSendReconciliation
		}
	default:
		return ErrSendReconciliation
	}
	return nil
}
func validSendReceipt(receipt SendReceipt) bool {
	return validIdentity(receipt.ProviderMessageID) && validDigest(receipt.ManifestDigest) && validDigest(receipt.ReceiptDigest) && receipt.ReceiptDigest == digest([]byte(receipt.ProviderMessageID+"\x00"+receipt.ManifestDigest))
}

func SaveDevelopmentCheckpoint(ctx context.Context, store *devsnapshot.Store, id string, controller *SendController) (devsnapshot.Snapshot, error) {
	if store == nil || controller == nil || controller.adapter == nil || controller.adapter.config.Provider == nil {
		return devsnapshot.Snapshot{}, ErrMailboxDenied
	}
	provider := controller.adapter.config.Provider
	controller.mu.Lock()
	provider.mu.Lock()
	providerSnapshot, providerErr := provider.snapshotLocked()
	controllerSnapshot, controllerErr := controller.snapshotLocked()
	provider.mu.Unlock()
	controller.mu.Unlock()
	if providerErr != nil || controllerErr != nil {
		return devsnapshot.Snapshot{}, errors.Join(providerErr, controllerErr)
	}
	providerJSON, _ := json.Marshal(providerSnapshot)
	controllerJSON, _ := json.Marshal(controllerSnapshot)
	return store.Put(ctx, id, map[string]json.RawMessage{"mail_provider": providerJSON, "send_controller": controllerJSON})
}
func LoadDevelopmentCheckpoint(ctx context.Context, store *devsnapshot.Store, id string) (ProviderSnapshot, SendControllerSnapshot, error) {
	if store == nil {
		return ProviderSnapshot{}, SendControllerSnapshot{}, ErrMailboxDenied
	}
	saved, err := store.Get(ctx, id)
	if err != nil {
		return ProviderSnapshot{}, SendControllerSnapshot{}, err
	}
	if len(saved.Components) != 2 {
		return ProviderSnapshot{}, SendControllerSnapshot{}, ErrMailboxDenied
	}
	var provider ProviderSnapshot
	var controller SendControllerSnapshot
	if decodeMail(saved.Components["mail_provider"], &provider) != nil || decodeMail(saved.Components["send_controller"], &controller) != nil || validateProviderSnapshot(provider, true) != nil || controller.SchemaVersion != mailSnapshotVersion || controller.Digest != mailDigest(controller) {
		return ProviderSnapshot{}, SendControllerSnapshot{}, ErrMailboxDenied
	}
	for _, stage := range controller.Stages {
		if validateSendStage(stage) != nil {
			return ProviderSnapshot{}, SendControllerSnapshot{}, ErrMailboxDenied
		}
	}
	return provider, controller, nil
}
func decodeMail(value []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrMailboxDenied
	}
	return nil
}
func mailDigest(value any) string {
	switch typed := value.(type) {
	case ProviderSnapshot:
		typed.Digest = ""
		value = typed
	case SendControllerSnapshot:
		typed.Digest = ""
		value = typed
	}
	encoded, _ := json.Marshal(value)
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func validDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != 71 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

package effect

import (
	"strings"
	"sync"

	"github.com/bkmashiro/agent-python-runtime/runtime/transaction"
)

type StagedOutbox struct {
	PrepareID       string
	ManifestDigest  string
	RecipientDigest string
	PayloadDigest   string
}

type OutboxCommitReceipt struct {
	CommitID       string
	ManifestDigest string
	EventID        string
}

type outboxIntent struct {
	Staged    StagedOutbox
	Recipient string
	Body      string
	Committed *OutboxCommitReceipt
}

type Outbox struct {
	mu             sync.Mutex
	byPrepare      map[string]string
	intents        map[string]*outboxIntent
	commits        map[string]OutboxCommitReceipt
	commitKeys     map[string]string
	committedCount int
}

func NewOutbox() *Outbox {
	return &Outbox{byPrepare: map[string]string{}, intents: map[string]*outboxIntent{}, commits: map[string]OutboxCommitReceipt{}, commitKeys: map[string]string{}}
}

func (outbox *Outbox) Prepare(prepareID, recipient, body string) (StagedOutbox, error) {
	outbox.mu.Lock()
	defer outbox.mu.Unlock()
	at := strings.LastIndex(recipient, "@")
	if at <= 0 || !strings.HasSuffix(strings.ToLower(recipient[at+1:]), ".invalid") || strings.ContainsAny(recipient, "\r\n") {
		return StagedOutbox{}, ErrRecipientDenied
	}
	manifest := Digest(prepareID + "\x00" + recipient + "\x00" + body)
	if prior, ok := outbox.byPrepare[prepareID]; ok {
		if prior != manifest {
			return StagedOutbox{}, ErrConflict
		}
		return outbox.intents[manifest].Staged, nil
	}
	staged := StagedOutbox{PrepareID: prepareID, ManifestDigest: manifest, RecipientDigest: Digest(recipient), PayloadDigest: Digest(body)}
	outbox.byPrepare[prepareID] = manifest
	outbox.intents[manifest] = &outboxIntent{Staged: staged, Recipient: recipient, Body: body}
	return staged, nil
}

func (outbox *Outbox) PrepareIrreversible(coordinator *transaction.Coordinator, operationID, recipient, body string) (StagedOutbox, error) {
	if coordinator == nil {
		return StagedOutbox{}, ErrAuthorityDenied
	}
	operation, err := coordinator.InspectOperation(operationID)
	if err != nil || operation.EffectClass != transaction.EffectIrreversible ||
		(operation.State != transaction.OperationAwaitingUserApproval && operation.State != transaction.OperationAwaitingAgentCommit) {
		return StagedOutbox{}, ErrAuthorityDenied
	}
	outbox.mu.Lock()
	defer outbox.mu.Unlock()
	at := strings.LastIndex(recipient, "@")
	if at <= 0 || !strings.HasSuffix(strings.ToLower(recipient[at+1:]), ".invalid") || strings.ContainsAny(recipient, "\r\n") {
		return StagedOutbox{}, ErrRecipientDenied
	}
	prepareID := "prepare_" + operation.ID
	manifest := operation.ManifestDigest
	key := Digest(manifest + "\x00" + recipient + "\x00" + body)
	if prior, ok := outbox.byPrepare[prepareID]; ok {
		if prior != key {
			return StagedOutbox{}, ErrConflict
		}
		return outbox.intents[manifest].Staged, nil
	}
	if _, exists := outbox.intents[manifest]; exists {
		return StagedOutbox{}, ErrConflict
	}
	staged := StagedOutbox{PrepareID: prepareID, ManifestDigest: manifest, RecipientDigest: Digest(recipient), PayloadDigest: Digest(body)}
	outbox.byPrepare[prepareID] = key
	outbox.intents[manifest] = &outboxIntent{Staged: staged, Recipient: recipient, Body: body}
	return staged, nil
}

func (outbox *Outbox) commit(commitID, manifestDigest string) (OutboxCommitReceipt, error) {
	outbox.mu.Lock()
	defer outbox.mu.Unlock()
	if prior, ok := outbox.commits[commitID]; ok {
		if outbox.commitKeys[commitID] != manifestDigest {
			return OutboxCommitReceipt{}, ErrConflict
		}
		return prior, nil
	}
	intent, ok := outbox.intents[manifestDigest]
	if !ok {
		return OutboxCommitReceipt{}, ErrManifestMismatch
	}
	if claimedManifest, claimed := outbox.commitKeys[commitID]; claimed {
		if claimedManifest != manifestDigest {
			return OutboxCommitReceipt{}, ErrConflict
		}
	} else {
		outbox.commitKeys[commitID] = manifestDigest
	}
	if intent.Committed != nil {
		if intent.Committed.CommitID != commitID {
			return OutboxCommitReceipt{}, ErrConflict
		}
		return *intent.Committed, nil
	}
	receipt := OutboxCommitReceipt{CommitID: commitID, ManifestDigest: manifestDigest, EventID: "outbox_" + Digest(manifestDigest)[7:]}
	intent.Committed = &receipt
	outbox.commits[commitID], outbox.commitKeys[commitID] = receipt, manifestDigest
	outbox.committedCount++
	return receipt, nil
}

func (outbox *Outbox) CommittedCount() int {
	outbox.mu.Lock()
	defer outbox.mu.Unlock()
	return outbox.committedCount
}

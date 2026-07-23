package transaction

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sync"
	"time"
)

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

func validIdentifier(value string) bool {
	return identifierPattern.MatchString(value)
}

type IDSource interface {
	New(prefix string) (string, error)
}

type coordinatorLedger interface {
	createTransaction(Transaction) error
	GetTransaction(string) (Transaction, error)
	createOperation(Operation) error
	GetOperation(string) (Operation, error)
	ListOperations(string) ([]Operation, error)
	transitionOperation(string, uint64, OperationState, OperationState, time.Time) (Operation, error)
}

type Coordinator struct {
	mu        sync.Mutex
	ledger    coordinatorLedger
	ids       IDSource
	now       func() time.Time
	authority AuthorityVerifier
}

type BeginRequest struct {
	RunID         string
	CatalogDigest string
	Mode          TransactionMode
}

type ProposeRequest struct {
	TransactionID  string
	ToolID         string
	HandlerVersion string
	EffectClass    EffectClass
	Policy         PolicyOutcome
	PolicyVersion  string
	ArgumentDigest string
}

type CommitSource string

const (
	CommitSourceAgent CommitSource = "agent"
	CommitSourceUser  CommitSource = "user"
)

type AuthorityClaims struct {
	AuthorityID    string
	TransactionID  string
	OperationID    string
	ManifestDigest string
	Source         CommitSource
	SourceRunID    string
	ActorID        string
	PhaseGrantID   string
	ExpiresAt      time.Time
	Consumed       bool
}

type AuthorityVerifier interface {
	Verify(token string) (AuthorityClaims, error)
	Consume(token string) error
}

type CommitCredential struct {
	Token string
}

func NewCoordinator(ledger coordinatorLedger, ids IDSource, now func() time.Time, authority AuthorityVerifier) *Coordinator {
	return &Coordinator{ledger: ledger, ids: ids, now: now, authority: authority}
}

func (coordinator *Coordinator) Begin(request BeginRequest) (Transaction, error) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if request.RunID == "" || !digestPattern.MatchString(request.CatalogDigest) ||
		(request.Mode != TransactionModeDirect && request.Mode != TransactionModeWorkflow) {
		return Transaction{}, ErrInvalidInput
	}
	id, err := coordinator.ids.New("tx")
	if err != nil {
		return Transaction{}, fmt.Errorf("generate transaction id: %w", err)
	}
	now := coordinator.now().UTC()
	value := Transaction{
		ID: id, RunID: request.RunID, CatalogDigest: request.CatalogDigest,
		Mode: request.Mode, State: TransactionOpen, CreatedAt: now,
	}
	if err := coordinator.ledger.createTransaction(value); err != nil {
		return Transaction{}, err
	}
	return coordinator.ledger.GetTransaction(id)
}

func (coordinator *Coordinator) Propose(request ProposeRequest) (Operation, error) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	transaction, err := coordinator.ledger.GetTransaction(request.TransactionID)
	if err != nil {
		return Operation{}, err
	}
	if transaction.State != TransactionOpen || !validIdentifier(request.ToolID) ||
		!validIdentifier(request.HandlerVersion) || !validIdentifier(request.PolicyVersion) ||
		!validEffectClass(request.EffectClass) || !validPolicy(request.Policy) ||
		!digestPattern.MatchString(request.ArgumentDigest) {
		return Operation{}, ErrInvalidInput
	}
	operations, err := coordinator.ledger.ListOperations(request.TransactionID)
	if err != nil {
		return Operation{}, err
	}
	if transaction.Mode == TransactionModeDirect && len(operations) != 0 {
		return Operation{}, ErrDirectTransactionLimit
	}
	id, err := coordinator.ids.New("op")
	if err != nil {
		return Operation{}, fmt.Errorf("generate operation id: %w", err)
	}
	state := stateForPolicy(request.Policy)
	now := coordinator.now().UTC()
	index := uint32(len(operations) + 1)
	value := Operation{
		ID: id, TransactionID: transaction.ID, Index: index,
		ToolID: request.ToolID, HandlerVersion: request.HandlerVersion,
		EffectClass: request.EffectClass, Policy: request.Policy, PolicyVersion: request.PolicyVersion,
		State: state, ArgumentDigest: request.ArgumentDigest,
		ManifestDigest: manifestDigest(transaction, request, index), CreatedAt: now, UpdatedAt: now,
	}
	if err := coordinator.ledger.createOperation(value); err != nil {
		return Operation{}, err
	}
	return coordinator.ledger.GetOperation(id)
}

func (coordinator *Coordinator) Authorize(credential CommitCredential) (Operation, error) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if coordinator.authority == nil || credential.Token == "" {
		return Operation{}, ErrAuthorityDenied
	}
	claims, err := coordinator.authority.Verify(credential.Token)
	if err != nil || !validIdentifier(claims.AuthorityID) || !validIdentifier(claims.ActorID) ||
		!validIdentifier(claims.TransactionID) || !validIdentifier(claims.OperationID) {
		return Operation{}, ErrAuthorityDenied
	}
	transaction, err := coordinator.ledger.GetTransaction(claims.TransactionID)
	if err != nil {
		return Operation{}, ErrAuthorityDenied
	}
	operation, err := coordinator.ledger.GetOperation(claims.OperationID)
	if err != nil || operation.TransactionID != transaction.ID {
		return Operation{}, ErrAuthorityDenied
	}
	if claims.Consumed {
		if operation.State == OperationReady {
			return operation, nil
		}
		return Operation{}, ErrAuthorityDenied
	}
	if operation.State == OperationReady {
		return Operation{}, ErrAlreadyAuthorized
	}
	if claims.ManifestDigest != operation.ManifestDigest || !digestPattern.MatchString(claims.ManifestDigest) {
		return Operation{}, ErrDigestMismatch
	}
	now := coordinator.now().UTC()
	if !claims.ExpiresAt.After(now) {
		return Operation{}, ErrExpired
	}

	switch operation.State {
	case OperationAwaitingAgentCommit:
		if claims.Source != CommitSourceAgent || !validIdentifier(claims.SourceRunID) ||
			!validIdentifier(claims.PhaseGrantID) {
			return Operation{}, ErrAuthorityDenied
		}
		if claims.SourceRunID == transaction.RunID {
			return Operation{}, ErrSameRunCommit
		}
	case OperationAwaitingUserApproval:
		if claims.Source != CommitSourceUser || claims.SourceRunID != "" || claims.PhaseGrantID != "" {
			return Operation{}, ErrAuthorityDenied
		}
	default:
		return Operation{}, ErrAuthorityDenied
	}

	if err := coordinator.authority.Consume(credential.Token); err != nil {
		return Operation{}, ErrAuthorityDenied
	}
	return coordinator.ledger.transitionOperation(
		operation.ID, operation.Version, operation.State, OperationReady, now,
	)
}

func stateForPolicy(policy PolicyOutcome) OperationState {
	switch policy {
	case PolicyDeny:
		return OperationDenied
	case PolicyAgentCommitRequired:
		return OperationAwaitingAgentCommit
	case PolicyUserApprovalRequired:
		return OperationAwaitingUserApproval
	default:
		return OperationReady
	}
}

func validEffectClass(value EffectClass) bool {
	switch value {
	case EffectReadOnly, EffectReversible, EffectCompensatable, EffectIrreversible:
		return true
	default:
		return false
	}
}

func validPolicy(value PolicyOutcome) bool {
	switch value {
	case PolicyDeny, PolicyAutoCommit, PolicyAgentCommitRequired, PolicyUserApprovalRequired:
		return true
	default:
		return false
	}
}

func manifestDigest(transaction Transaction, request ProposeRequest, index uint32) string {
	hash := sha256.New()
	for _, value := range []string{
		transaction.ID, transaction.RunID, transaction.CatalogDigest, string(transaction.Mode),
		fmt.Sprintf("%d", index), request.ToolID, request.HandlerVersion,
		string(request.EffectClass), string(request.Policy), request.PolicyVersion, request.ArgumentDigest,
	} {
		hash.Write([]byte{0})
		hash.Write([]byte(value))
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

package transaction

import (
	"errors"
	"time"
)

var (
	ErrAlreadyExists          = errors.New("transaction record already exists")
	ErrNotFound               = errors.New("transaction record not found")
	ErrConflict               = errors.New("transaction record version or state conflict")
	ErrInvalidInput           = errors.New("invalid transaction input")
	ErrDirectTransactionLimit = errors.New("direct transaction permits exactly one operation")
	ErrAuthorityDenied        = errors.New("commit authority denied")
	ErrSameRunCommit          = errors.New("same-run agent commit denied")
	ErrDigestMismatch         = errors.New("manifest digest mismatch")
	ErrExpired                = errors.New("authority expired")
	ErrAlreadyAuthorized      = errors.New("operation already authorized")
)

type TransactionMode string

const (
	TransactionModeDirect   TransactionMode = "direct"
	TransactionModeWorkflow TransactionMode = "workflow"
)

type EffectClass string

const (
	EffectReadOnly      EffectClass = "read_only"
	EffectReversible    EffectClass = "reversible"
	EffectCompensatable EffectClass = "compensatable"
	EffectIrreversible  EffectClass = "irreversible"
)

type PolicyOutcome string

const (
	PolicyDeny                 PolicyOutcome = "DENY"
	PolicyAutoCommit           PolicyOutcome = "AUTO_COMMIT"
	PolicyAgentCommitRequired  PolicyOutcome = "AGENT_COMMIT_REQUIRED"
	PolicyUserApprovalRequired PolicyOutcome = "USER_APPROVAL_REQUIRED"
)

type AttemptKind string

const (
	AttemptApply      AttemptKind = "apply"
	AttemptRollback   AttemptKind = "rollback"
	AttemptCompensate AttemptKind = "compensate"
)

type Transaction struct {
	ID            string
	RunID         string
	CatalogDigest string
	Mode          TransactionMode
	State         TransactionState
	Version       uint64
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type Operation struct {
	ID             string
	TransactionID  string
	Index          uint32
	ToolID         string
	HandlerVersion string
	EffectClass    EffectClass
	Policy         PolicyOutcome
	PolicyVersion  string
	State          OperationState
	ArgumentDigest string
	ManifestDigest string
	Version        uint64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type Attempt struct {
	ID                     string
	TransactionID          string
	OperationID            string
	Kind                   AttemptKind
	Ordinal                uint32
	State                  AttemptState
	ExpectedOperationState OperationState
	LeaseID                string
	LeaseExpiresAt         time.Time
	ProviderRequestDigest  string
	ReconciliationDigest   string
	Version                uint64
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

type Transition struct {
	Sequence      uint64
	TransactionID string
	EntityType    string
	EntityID      string
	From          string
	To            string
	ObservedAt    time.Time
}

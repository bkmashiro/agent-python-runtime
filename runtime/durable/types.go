package durable

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrBusy            = errors.New("durable run is busy")
	ErrNotFound        = errors.New("durable object not found")
	ErrConflict        = errors.New("durable state conflict")
	ErrHistoryMismatch = errors.New("durable history mismatch")
	ErrLimit           = errors.New("durable logical payload limit exceeded")
)

const (
	StatusActive    = "active"
	StatusWaiting   = "waiting"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
	StatusBlocked   = "blocked"

	CallPending   = "pending"
	CallWaiting   = "waiting"
	CallCompleted = "completed"

	// MaxRunPayloadBytes bounds logical payload retained for one run. It is not
	// a bound on the SQLite database or WAL file size.
	MaxRunPayloadBytes uint64 = 64 << 20
)

type Definition struct {
	ID                 string
	Code               string
	Seed               string
	ArtifactSHA256     string
	EnvironmentVersion string
	Inputs             json.RawMessage
	Tools              json.RawMessage
}

type Run struct {
	Definition Definition
	Status     string
	Outcome    json.RawMessage
	Reason     string
}

type Call struct {
	RunID        string
	Sequence     uint32
	CallID       string
	Tool         string
	OperationKey string
	State        string
	Arguments    json.RawMessage
	Outcome      json.RawMessage
}

type WaitSpec struct {
	Kind     string
	Request  json.RawMessage
	Deadline *time.Time
}

type Decision struct {
	Result json.RawMessage
	Error  string
}

type Wait struct {
	ID       string
	RunID    string
	Sequence uint32
	Spec     WaitSpec
	Decision *Decision
}

type Store struct {
	db      *sql.DB
	lockDir string
}

// Package approval owns bounded human-approval leases independently of Guest
// execution lifetime. It stores digest-only audit records and never dispatches
// capabilities itself.
package approval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"
)

var (
	ErrInvalidProposal = errors.New("invalid approval proposal")
	ErrDecisionFinal   = errors.New("approval decision is final")
	ErrRejected        = errors.New("approval rejected")
	ErrExpired         = errors.New("approval expired")
	ErrNotApproved     = errors.New("approval was not granted")
)

type Status string

const (
	StatusWaiting   Status = "waiting"
	StatusApproved  Status = "approved"
	StatusRejected  Status = "rejected"
	StatusExpired   Status = "expired"
	StatusCancelled Status = "cancelled"
)

type Proposal struct {
	RunID        string
	PlanSHA256   string
	CallID       string
	ParentCallID string
	Capability   string
	Arguments    []byte
	Lease        time.Duration
}

type Request struct {
	RequestID       string    `json:"request_id"`
	RunID           string    `json:"run_id"`
	PlanSHA256      string    `json:"plan_sha256"`
	CallID          string    `json:"call_id"`
	ParentCallID    string    `json:"parent_call_id,omitempty"`
	Capability      string    `json:"capability"`
	ArgumentsSHA256 string    `json:"arguments_sha256"`
	CreatedAt       time.Time `json:"created_at"`
	ExpiresAt       time.Time `json:"expires_at"`
}

type Permit struct {
	RequestID string
}

type Record struct {
	Request
	Status          Status     `json:"status"`
	DecisionAt      *time.Time `json:"decision_at,omitempty"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	Executed        bool       `json:"executed"`
	DispatchOutcome string     `json:"dispatch_outcome,omitempty"`
}

type pending struct {
	record Record
	wake   chan Status
}

type Controller struct {
	mu       sync.Mutex
	sequence uint64
	order    []string
	pending  map[string]*pending
}

func NewController() *Controller {
	return &Controller{pending: make(map[string]*pending)}
}

func (controller *Controller) Authorize(ctx context.Context, proposal Proposal) (Permit, error) {
	if controller == nil || ctx == nil || !validProposal(proposal) {
		return Permit{}, ErrInvalidProposal
	}
	now := time.Now().UTC()
	argumentsDigest := sha256.Sum256(proposal.Arguments)

	controller.mu.Lock()
	controller.sequence++
	requestID := requestIdentity(proposal, now, controller.sequence, argumentsDigest)
	request := Request{
		RequestID: requestID, RunID: proposal.RunID, PlanSHA256: proposal.PlanSHA256,
		CallID: proposal.CallID, ParentCallID: proposal.ParentCallID, Capability: proposal.Capability,
		ArgumentsSHA256: "sha256:" + hex.EncodeToString(argumentsDigest[:]), CreatedAt: now, ExpiresAt: now.Add(proposal.Lease),
	}
	state := &pending{record: Record{Request: request, Status: StatusWaiting}, wake: make(chan Status, 1)}
	controller.pending[requestID] = state
	controller.order = append(controller.order, requestID)
	controller.mu.Unlock()

	timer := time.NewTimer(proposal.Lease)
	defer timer.Stop()
	var status Status
	select {
	case status = <-state.wake:
	case <-timer.C:
		status = controller.finishWaiting(requestID, StatusExpired)
	case <-ctx.Done():
		status = controller.finishWaiting(requestID, StatusCancelled)
	}
	switch status {
	case StatusApproved:
		return Permit{RequestID: requestID}, nil
	case StatusRejected:
		return Permit{RequestID: requestID}, ErrRejected
	case StatusExpired:
		return Permit{RequestID: requestID}, ErrExpired
	case StatusCancelled:
		return Permit{RequestID: requestID}, ctx.Err()
	default:
		return Permit{RequestID: requestID}, ErrDecisionFinal
	}
}

func (controller *Controller) Approve(requestID string) error {
	return controller.resolve(requestID, StatusApproved)
}

func (controller *Controller) Reject(requestID string) error {
	return controller.resolve(requestID, StatusRejected)
}

func (controller *Controller) resolve(requestID string, status Status) error {
	if controller == nil || (status != StatusApproved && status != StatusRejected) {
		return ErrDecisionFinal
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	state, ok := controller.pending[requestID]
	if !ok || state.record.Status != StatusWaiting {
		return ErrDecisionFinal
	}
	now := time.Now().UTC()
	if !now.Before(state.record.ExpiresAt) {
		state.record.Status = StatusExpired
		state.record.DecisionAt = &now
		state.wake <- StatusExpired
		return ErrDecisionFinal
	}
	state.record.Status = status
	state.record.DecisionAt = &now
	state.wake <- status
	return nil
}

func (controller *Controller) finishWaiting(requestID string, status Status) Status {
	controller.mu.Lock()
	defer controller.mu.Unlock()
	state, ok := controller.pending[requestID]
	if !ok {
		return ""
	}
	if state.record.Status != StatusWaiting {
		return state.record.Status
	}
	now := time.Now().UTC()
	state.record.Status = status
	state.record.DecisionAt = &now
	return status
}

func (controller *Controller) Complete(requestID, outcome string) error {
	if controller == nil || !validOutcome(outcome) {
		return ErrNotApproved
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	state, ok := controller.pending[requestID]
	if !ok || state.record.Status != StatusApproved || state.record.Executed || state.record.CompletedAt != nil {
		return ErrNotApproved
	}
	now := time.Now().UTC()
	state.record.Executed = true
	state.record.DispatchOutcome = outcome
	state.record.CompletedAt = &now
	return nil
}

// AbortApproved records that a permit became unusable before handler dispatch.
// It never marks the operation executed and prevents later completion/revival.
func (controller *Controller) AbortApproved(requestID, outcome string) error {
	if controller == nil || outcome != "cancelled_before_dispatch" {
		return ErrNotApproved
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	state, ok := controller.pending[requestID]
	if !ok || state.record.Status != StatusApproved || state.record.Executed || state.record.CompletedAt != nil {
		return ErrNotApproved
	}
	now := time.Now().UTC()
	state.record.DispatchOutcome = outcome
	state.record.CompletedAt = &now
	return nil
}

func (controller *Controller) Snapshot() []Record {
	if controller == nil {
		return nil
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	records := make([]Record, 0, len(controller.order))
	for _, id := range controller.order {
		records = append(records, controller.pending[id].record)
	}
	return records
}

func requestIdentity(proposal Proposal, created time.Time, sequence uint64, arguments [sha256.Size]byte) string {
	hash := sha256.New()
	for _, value := range []string{"pysolate-approval-request-v1", proposal.RunID, proposal.PlanSHA256, proposal.CallID, proposal.ParentCallID, proposal.Capability, hex.EncodeToString(arguments[:]), strconv.FormatInt(created.UnixNano(), 10), strconv.FormatUint(sequence, 10)} {
		hash.Write([]byte(value))
		hash.Write([]byte{0})
	}
	return "apr_" + hex.EncodeToString(hash.Sum(nil))
}

func validProposal(proposal Proposal) bool {
	return validIdentity(proposal.RunID, 128) && validSHA256(proposal.PlanSHA256) && validIdentity(proposal.CallID, 128) &&
		(proposal.ParentCallID == "" || validIdentity(proposal.ParentCallID, 96)) && validIdentity(proposal.Capability, 128) &&
		len(proposal.Arguments) > 0 && len(proposal.Arguments) <= 1<<20 && proposal.Lease > 0 && proposal.Lease <= 24*time.Hour
}

func validIdentity(value string, limit int) bool {
	if len(value) == 0 || len(value) > limit || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') && (character < '0' || character > '9') && character != '-' && character != '_' && character != '.' && character != ':' {
			return false
		}
	}
	return true
}

func validSHA256(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || value[:len("sha256:")] != "sha256:" {
		return false
	}
	_, err := hex.DecodeString(value[len("sha256:"):])
	return err == nil
}

func validOutcome(value string) bool {
	return value == "ok" || value == "error" || value == "ambiguous"
}

package capability

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/bkmashiro/agent-python-runtime/runtime/approval"
	sourcebindingtrusted "github.com/bkmashiro/agent-python-runtime/runtime/internal/sourcebinding"
	"github.com/bkmashiro/agent-python-runtime/runtime/receipt"
)

const maxCallBytes = 1 << 20

var (
	ErrInvalidBroker                = errors.New("invalid Host tool broker")
	ErrStagedObservationNotTargeted = errors.New("dynamic call was not targeted by staged observation")
)

type CallLifecyclePhase string

const (
	CallLifecycleIntent  CallLifecyclePhase = "intent"
	CallLifecycleStarted CallLifecyclePhase = "started"
)

type CallLifecycleObservation struct {
	ArgumentsSHA256      string
	CallID               string
	Capability           string
	CapabilityPlanSHA256 string
	OperationIndex       uint32
	Phase                CallLifecyclePhase
}

// CallLifecycleObserver receives body-free, non-authoritative live-call markers.
// Observation failure must not decide whether a capability executes.
type CallLifecycleObserver interface {
	ObserveCallLifecycle(context.Context, CallLifecycleObservation)
}

type Config struct {
	RunIdentity         string
	Plan                *Plan
	Playback            *PlaybackConfig
	Branch              *BranchConfig
	StagedClaimer       StagedObservationClaimer
	SemanticPreDispatch bool
	// ProgrammaticParentCallID binds every admitted call to one program
	// execution. Empty selects the ordinary direct-call path.
	ProgrammaticParentCallID string
	// AllowDirectCalls selects the `both` presentation. The `:program:`
	// namespace remains reserved for exact parent-bound children.
	AllowDirectCalls   bool
	ApprovalSuspension bool
	ApprovalController *approval.Controller
	SourceResolver     *SourceBindingResolver
}

type StagedObservationClaimer interface {
	Claim(context.Context, string, json.RawMessage) (StagedCapabilityOutcome, error)
	Finalize(bool) error
}

type CallIDStagedObservationClaimer interface {
	ClaimCall(context.Context, string, string, json.RawMessage) (StagedCapabilityOutcome, error)
}

type SourceBindingRequest = sourcebindingtrusted.Request

// SourceBindingResolver is an opaque Host-TCB resolver. Its constructor accepts
// an internal authority token, so external plugins can consume but cannot mint
// source-bound receipt evidence.
type SourceBindingResolver struct {
	authority sourcebindingtrusted.Authority
}

func NewSourceBindingResolver(authority sourcebindingtrusted.Authority) (*SourceBindingResolver, error) {
	if !authority.Valid() {
		return nil, ErrInvalidBroker
	}
	return &SourceBindingResolver{authority: authority}, nil
}

func (resolver *SourceBindingResolver) ResolveSource(request SourceBindingRequest) (receipt.SourceBinding, bool) {
	if resolver == nil {
		return receipt.SourceBinding{}, false
	}
	return resolver.authority.Resolve(request)
}

type Broker struct {
	config            Config
	mu                sync.Mutex
	calls             uint32
	programmaticCalls uint32
	seen              map[string]struct{}
	receipts          []receipt.Receipt
	transcript        []TranscriptEntry
	playbackEntries   map[uint32]TranscriptEntry
	playbackConsumed  map[uint32]bool
	playbackFailed    bool
	branch            *branchState
	lifecycleObserver CallLifecycleObserver
}

type request struct {
	CallID            string                 `json:"call_id"`
	Capability        string                 `json:"capability"`
	Arguments         json.RawMessage        `json:"arguments"`
	ApprovalRequestID string                 `json:"-"`
	ParentCallID      string                 `json:"-"`
	Source            *receipt.SourceBinding `json:"-"`
}

type response struct {
	CallID string          `json:"call_id"`
	Status string          `json:"status"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *callError      `json:"error,omitempty"`
}

type callError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func NewBroker(config Config) (*Broker, error) {
	if !validIdentity(config.RunIdentity) || config.Plan == nil || config.Plan.Identity() == "" || config.Plan.MaxCalls() == 0 ||
		(config.Playback != nil && config.Branch != nil) || (config.StagedClaimer != nil && (config.Playback != nil || config.Branch != nil)) ||
		(config.StagedClaimer != nil) != config.SemanticPreDispatch ||
		(config.ProgrammaticParentCallID != "" && !validProgrammaticParentCallID(config.ProgrammaticParentCallID)) ||
		(config.AllowDirectCalls && config.ProgrammaticParentCallID == "") ||
		(config.SourceResolver != nil && config.ProgrammaticParentCallID == "") ||
		(config.ApprovalController != nil) != config.ApprovalSuspension || (config.Plan.RequiresApproval() && !config.ApprovalSuspension) {
		return nil, ErrInvalidBroker
	}
	broker := &Broker{config: config, seen: make(map[string]struct{})}
	if config.Playback != nil {
		entries, err := normalizePlaybackEntries(config.Playback.Entries)
		if err != nil {
			return nil, ErrInvalidBroker
		}
		broker.playbackEntries = make(map[uint32]TranscriptEntry, len(entries))
		broker.playbackConsumed = make(map[uint32]bool, len(entries))
		for _, entry := range entries {
			registered, ok := config.Plan.lookup(entry.Capability)
			if !ok || registered.spec.Playback != PlaybackCaptured || entry.OperationIndex >= config.Plan.MaxCalls() {
				return nil, ErrInvalidBroker
			}
			broker.playbackEntries[entry.OperationIndex] = entry
		}
	}
	if config.Branch != nil {
		branch, err := newBranchState(*config.Branch, config.Plan)
		if err != nil {
			return nil, ErrInvalidBroker
		}
		broker.branch = branch
	}
	return broker, nil
}

// CapabilityPlan returns the immutable sealed Plan bound at Broker construction.
func (broker *Broker) CapabilityPlan() *Plan {
	if broker == nil {
		return nil
	}
	return broker.config.Plan
}

func (broker *Broker) AttachCallLifecycleObserver(observer CallLifecycleObserver) error {
	if broker == nil || observer == nil {
		return ErrInvalidBroker
	}
	broker.mu.Lock()
	defer broker.mu.Unlock()
	if broker.calls != 0 || broker.lifecycleObserver != nil {
		return ErrInvalidBroker
	}
	broker.lifecycleObserver = observer
	return nil
}

// AttachStagedClaimer installs one Run-private physical/logical join before the
// first Broker call. Semantic pre-dispatch still binds its claimer at Broker
// construction; split-phase derived execution uses this late Host-only join.
func (broker *Broker) AttachStagedClaimer(claimer StagedObservationClaimer) error {
	if broker == nil || claimer == nil {
		return ErrInvalidBroker
	}
	broker.mu.Lock()
	defer broker.mu.Unlock()
	if table, ok := claimer.(*SplitPhaseTable); ok &&
		(table.owner != broker || table.RunIdentity() != broker.config.RunIdentity || table.PlanIdentity() != broker.config.Plan.Identity()) {
		return ErrInvalidBroker
	}
	if broker.calls != 0 || broker.config.StagedClaimer != nil || broker.config.Playback != nil || broker.config.Branch != nil || broker.config.SemanticPreDispatch {
		return ErrInvalidBroker
	}
	broker.config.StagedClaimer = claimer
	return nil
}

// AttachedSplitPhaseTable returns the Host-owned table supplied before the
// first logical call, if source-time analysis already created one.
func (broker *Broker) AttachedSplitPhaseTable() *SplitPhaseTable {
	if broker == nil {
		return nil
	}
	broker.mu.Lock()
	defer broker.mu.Unlock()
	table, _ := broker.config.StagedClaimer.(*SplitPhaseTable)
	return table
}

func (broker *Broker) observeCallLifecycle(ctx context.Context, call request, operation uint32, phase CallLifecyclePhase) {
	broker.mu.Lock()
	observer := broker.lifecycleObserver
	broker.mu.Unlock()
	if observer == nil {
		return
	}
	digest := sha256.Sum256(call.Arguments)
	observer.ObserveCallLifecycle(ctx, CallLifecycleObservation{
		ArgumentsSHA256: fmt.Sprintf("sha256:%x", digest[:]), CallID: call.CallID, Capability: call.Capability,
		CapabilityPlanSHA256: broker.config.Plan.Identity(), OperationIndex: operation, Phase: phase,
	})
}

func (broker *Broker) Call(ctx context.Context, raw []byte) ([]byte, error) {
	return broker.call(ctx, raw, false)
}

// CallStreaming applies the pre-seal authority ceiling at the Broker itself;
// Python projection names are not a security boundary.
func (broker *Broker) CallStreaming(ctx context.Context, raw []byte) ([]byte, error) {
	return broker.call(ctx, raw, true)
}

func (broker *Broker) call(ctx context.Context, raw []byte, streaming bool) ([]byte, error) {
	if broker == nil {
		return nil, ErrInvalidBroker
	}
	if len(raw) == 0 || len(raw) > maxCallBytes {
		broker.failPlayback()
		return nil, ErrInvalidBroker
	}
	var call request
	if !utf8.Valid(raw) || rejectDuplicateJSON(raw) != nil {
		broker.failPlayback()
		return encodeResponse(response{Status: "error", Error: &callError{Code: "invalid_arguments", Message: "invalid Host tool call"}})
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&call); err != nil || !validIdentity(call.CallID) || !validName(call.Capability) || len(call.Arguments) == 0 || !json.Valid(call.Arguments) {
		broker.failPlayback()
		return encodeResponse(response{CallID: call.CallID, Status: "error", Error: &callError{Code: "invalid_arguments", Message: "invalid Host tool call"}})
	}

	broker.mu.Lock()
	programmaticCall := false
	if broker.config.ProgrammaticParentCallID != "" {
		expected := fmt.Sprintf("%s:program:%d", broker.config.ProgrammaticParentCallID, broker.programmaticCalls+1)
		reservedProgrammaticID := strings.Contains(call.CallID, ":program:")
		switch {
		case call.CallID == expected:
			programmaticCall = true
		case !broker.config.AllowDirectCalls || reservedProgrammaticID:
			broker.mu.Unlock()
			return encodeResponse(response{CallID: call.CallID, Status: "denied", Error: &callError{Code: "programmatic_call_identity_mismatch", Message: "programmatic child call identity does not match its parent and sequence"}})
		}
	}
	if broker.calls >= broker.config.Plan.MaxCalls() {
		broker.mu.Unlock()
		broker.failPlayback()
		return encodeResponse(response{CallID: call.CallID, Status: "error", Error: &callError{Code: "call_budget_exceeded", Message: "Host tool call budget exhausted"}})
	}
	if _, duplicate := broker.seen[call.CallID]; duplicate {
		broker.mu.Unlock()
		broker.failPlayback()
		return encodeResponse(response{CallID: call.CallID, Status: "error", Error: &callError{Code: "duplicate_call_id", Message: "call_id must be unique"}})
	}
	broker.seen[call.CallID] = struct{}{}
	if programmaticCall {
		broker.programmaticCalls++
		call.ParentCallID = broker.config.ProgrammaticParentCallID
	}
	operation := broker.calls
	broker.calls++
	broker.mu.Unlock()

	registered, ok := broker.config.Plan.lookup(call.Capability)
	if !ok {
		broker.failPlayback()
		broker.record(call, operation, "denied", nil)
		return encodeResponse(response{CallID: call.CallID, Status: "denied", Error: &callError{Code: "capability_denied", Message: "Host tool is not granted"}})
	}
	if streaming && registered.spec.EffectClass == EffectWorkspaceWrite {
		broker.failPlayback()
		broker.record(call, operation, "denied", nil)
		return encodeResponse(response{CallID: call.CallID, Status: "denied", Error: &callError{Code: "streaming_write_denied", Message: "write authority is unavailable before final seal"}})
	}
	arguments, err := canonicalForSchema(registered.inputSchema, call.Arguments)
	if err != nil {
		broker.failPlayback()
		broker.record(call, operation, "denied", nil)
		return encodeResponse(response{CallID: call.CallID, Status: "denied", Error: &callError{Code: "invalid_arguments", Message: "Host tool arguments do not match the capability schema"}})
	}
	call.Arguments = arguments
	if programmaticCall && broker.config.SourceResolver != nil {
		bound, found := broker.config.SourceResolver.ResolveSource(SourceBindingRequest{
			CallID: call.CallID, ParentCallID: call.ParentCallID, Capability: call.Capability,
			OperationIndex: operation, Arguments: append(json.RawMessage(nil), arguments...), Programmatic: true,
		})
		if found {
			if !receipt.ValidSourceBinding(bound) || bound.Capability != call.Capability {
				broker.record(call, operation, "denied", nil)
				return encodeResponse(response{CallID: call.CallID, Status: "denied", Error: &callError{Code: "source_binding_invalid", Message: "Host source binding is invalid"}})
			}
			copy := bound
			call.Source = &copy
		}
	}
	broker.observeCallLifecycle(ctx, call, operation, CallLifecycleIntent)
	if broker.config.StagedClaimer != nil {
		qualifiedStaged := registered.spec.Playback == PlaybackLiveOnly
		if broker.config.SemanticPreDispatch {
			qualification, qualified := broker.config.Plan.PreDispatch(call.Capability)
			qualifiedStaged = qualifiedStaged && qualified && qualification.Eligible() &&
				(registered.spec.EffectClass == EffectPure || registered.spec.EffectClass == EffectWorkspaceRead || registered.spec.EffectClass == EffectExternalRead)
		}
		if !qualifiedStaged {
			broker.record(call, operation, "denied", nil)
			return encodeResponse(response{CallID: call.CallID, Status: "denied", Error: &callError{Code: "staged_observation_unqualified", Message: "capability is not eligible for staged observation"}})
		}
		var staged StagedCapabilityOutcome
		var claimErr error
		if targeted, ok := broker.config.StagedClaimer.(CallIDStagedObservationClaimer); ok {
			staged, claimErr = targeted.ClaimCall(ctx, call.CallID, call.Capability, append(json.RawMessage(nil), arguments...))
		} else {
			staged, claimErr = broker.config.StagedClaimer.Claim(ctx, call.Capability, append(json.RawMessage(nil), arguments...))
		}
		if claimErr != nil && !errors.Is(claimErr, ErrStagedObservationNotTargeted) {
			broker.record(call, operation, "error", nil)
			if errors.Is(claimErr, context.Canceled) || errors.Is(claimErr, context.DeadlineExceeded) {
				return encodeResponse(response{CallID: call.CallID, Status: "error", Error: &callError{Code: "handler_error", Message: "Host tool failed"}})
			}
			return encodeResponse(response{CallID: call.CallID, Status: "error", Error: &callError{Code: "staged_observation_mismatch", Message: "staged observation did not match the dynamic Host call"}})
		}
		if claimErr == nil && staged.Validate() != nil {
			broker.record(call, operation, "error", nil)
			return encodeResponse(response{CallID: call.CallID, Status: "error", Error: &callError{Code: "invalid_staged_result", Message: "staged observation result is outside the capability schema"}})
		}
		if claimErr == nil && staged.ErrorCode != "" {
			broker.record(call, operation, "error", nil)
			if staged.ErrorCode == PLMProviderOutcomeUncertainCode {
				return encodeResponse(response{CallID: call.CallID, Status: "error", Error: &callError{Code: PLMProviderOutcomeUncertainCode, Message: "Provider outcome is uncertain; the operation was not replayed"}})
			}
			if staged.ErrorCode == "handler_error" {
				return encodeResponse(response{CallID: call.CallID, Status: "error", Error: &callError{Code: "handler_error", Message: "Host tool failed"}})
			}
			return encodeResponse(response{CallID: call.CallID, Status: "error", Error: &callError{Code: "invalid_result", Message: "Host tool returned a result outside its capability schema"}})
		}
		if claimErr == nil {
			canonicalResult, resultErr := canonicalForSchema(registered.outputSchema, staged.Result)
			if resultErr == nil {
				resultErr = validateSpecResultSemantics(registered.spec, canonicalResult)
			}
			if resultErr != nil || len(canonicalResult) > maxCallBytes {
				broker.record(call, operation, "error", nil)
				return encodeResponse(response{CallID: call.CallID, Status: "error", Error: &callError{Code: "invalid_staged_result", Message: "staged observation result is outside the capability schema"}})
			}
			broker.record(call, operation, "ok", canonicalResult)
			return encodeResponse(response{CallID: call.CallID, Status: "ok", Result: canonicalResult})
		}
	}
	if broker.branch != nil {
		entry, live, matchErr := broker.matchBranch(operation, call.Capability, arguments)
		if matchErr != nil {
			broker.failBranch()
			broker.record(call, operation, "error", nil)
			return encodeResponse(response{CallID: call.CallID, Status: "error", Error: &callError{Code: "branch_mismatch", Message: "Host branch transcript does not match this call"}})
		}
		if !live {
			canonicalResult, resultErr := canonicalForSchema(registered.outputSchema, entry.Result)
			if resultErr == nil {
				resultErr = validateSpecResultSemantics(registered.spec, canonicalResult)
			}
			if resultErr != nil || len(canonicalResult) > maxCallBytes || playbackDigest(canonicalResult) != entry.ResultSHA256 {
				broker.failBranch()
				broker.record(call, operation, "error", nil)
				return encodeResponse(response{CallID: call.CallID, Status: "error", Error: &callError{Code: "invalid_result", Message: "Host branch result does not match the capability schema"}})
			}
			if err := broker.consumeBranch(entry); err != nil {
				broker.failBranch()
				broker.record(call, operation, "error", nil)
				return encodeResponse(response{CallID: call.CallID, Status: "error", Error: &callError{Code: "branch_mismatch", Message: "Host branch transcript does not match this call"}})
			}
			broker.record(call, operation, "ok", canonicalResult)
			return encodeResponse(response{CallID: call.CallID, Status: "ok", Result: canonicalResult})
		}
		if registered.spec.EffectClass != EffectExternalRead || registered.spec.Playback != PlaybackCaptured {
			broker.failBranch()
			broker.record(call, operation, "denied", nil)
			return encodeResponse(response{CallID: call.CallID, Status: "denied", Error: &callError{Code: "branch_authority_denied", Message: "Host branch live suffix permits only captured external reads"}})
		}
	} else if broker.playbackEntries != nil {
		entry, matchErr := broker.matchPlayback(operation, call.Capability, arguments)
		if matchErr != nil {
			broker.failPlayback()
			broker.record(call, operation, "error", nil)
			return encodeResponse(response{CallID: call.CallID, Status: "error", Error: &callError{Code: "playback_mismatch", Message: "Host playback transcript does not match this call"}})
		}
		canonicalResult, resultErr := canonicalForSchema(registered.outputSchema, entry.Result)
		if resultErr == nil {
			resultErr = validateSpecResultSemantics(registered.spec, canonicalResult)
		}
		if resultErr != nil || len(canonicalResult) > maxCallBytes || playbackDigest(canonicalResult) != entry.ResultSHA256 {
			broker.failPlayback()
			broker.record(call, operation, "error", nil)
			return encodeResponse(response{CallID: call.CallID, Status: "error", Error: &callError{Code: "invalid_result", Message: "Host playback result does not match the capability schema"}})
		}
		if err := broker.consumePlayback(operation); err != nil {
			broker.failPlayback()
			broker.record(call, operation, "error", nil)
			return encodeResponse(response{CallID: call.CallID, Status: "error", Error: &callError{Code: "playback_mismatch", Message: "Host playback transcript does not match this call"}})
		}
		broker.record(call, operation, "ok", canonicalResult)
		return encodeResponse(response{CallID: call.CallID, Status: "ok", Result: canonicalResult})
	}
	approvalRequestID := ""
	if registered.spec.Approval != nil {
		permit, approvalErr := broker.config.ApprovalController.Authorize(ctx, approval.Proposal{
			RunID: broker.config.RunIdentity, PlanSHA256: broker.config.Plan.Identity(), CallID: call.CallID,
			ParentCallID: call.ParentCallID, Capability: call.Capability,
			Arguments: append([]byte(nil), arguments...), Lease: time.Duration(registered.spec.Approval.LeaseMilliseconds) * time.Millisecond,
		})
		call.ApprovalRequestID = permit.RequestID
		if approvalErr != nil {
			broker.record(call, operation, "denied", nil)
			code, message := "approval_cancelled", "approval wait was cancelled"
			switch {
			case errors.Is(approvalErr, approval.ErrRejected):
				code, message = "approval_rejected", "Host tool approval was rejected"
			case errors.Is(approvalErr, approval.ErrExpired):
				code, message = "approval_expired", "Host tool approval lease expired"
			case errors.Is(approvalErr, approval.ErrAuditCapacity):
				code, message = "approval_unavailable", "Host approval audit capacity is exhausted"
			}
			return encodeResponse(response{CallID: call.CallID, Status: "denied", Error: &callError{Code: code, Message: message}})
		}
		approvalRequestID = permit.RequestID
		if dispatchErr := broker.config.ApprovalController.BeginDispatch(ctx, approvalRequestID); dispatchErr != nil {
			broker.record(call, operation, "denied", nil)
			code, message := "approval_cancelled", "approval was cancelled before Host tool dispatch"
			if errors.Is(dispatchErr, approval.ErrExpired) {
				code, message = "approval_expired", "Host tool approval lease expired before dispatch"
			}
			return encodeResponse(response{CallID: call.CallID, Status: "denied", Error: &callError{Code: code, Message: message}})
		}
	}
	broker.observeCallLifecycle(ctx, call, operation, CallLifecycleStarted)
	var result json.RawMessage
	var evidence TransportEvidence
	if evidenced, ok := registered.handler.(EvidenceHandler); ok {
		result, evidence, err = evidenced.CallWithEvidence(ctx, append(json.RawMessage(nil), arguments...))
	} else {
		result, err = registered.handler.Call(ctx, append(json.RawMessage(nil), arguments...))
	}
	if err != nil {
		if !broker.completeApproval(approvalRequestID, "error") {
			broker.record(call, operation, "ambiguous", nil)
			return encodeResponse(response{CallID: call.CallID, Status: "error", Error: &callError{Code: "approval_audit_failed", Message: "approved Host tool executed but its audit completion failed"}})
		}
		broker.failBranch()
		broker.record(call, operation, "error", nil)
		return encodeResponse(response{CallID: call.CallID, Status: "error", Error: &callError{Code: "handler_error", Message: "Host tool failed"}})
	}
	canonicalResult, err := canonicalForSchema(registered.outputSchema, result)
	if err == nil {
		err = validateSpecResultSemantics(registered.spec, canonicalResult)
	}
	if err != nil || len(canonicalResult) > maxCallBytes || (registered.spec.Playback == PlaybackCaptured && !validLiveTransportEvidence(evidence)) {
		if !broker.completeApproval(approvalRequestID, "error") {
			broker.record(call, operation, "ambiguous", nil)
			return encodeResponse(response{CallID: call.CallID, Status: "error", Error: &callError{Code: "approval_audit_failed", Message: "approved Host tool executed but its audit completion failed"}})
		}
		broker.failBranch()
		broker.record(call, operation, "error", nil)
		return encodeResponse(response{CallID: call.CallID, Status: "error", Error: &callError{Code: "invalid_result", Message: "Host tool returned a result outside its capability schema"}})
	}
	if !broker.completeApproval(approvalRequestID, "ok") {
		broker.record(call, operation, "ambiguous", nil)
		return encodeResponse(response{CallID: call.CallID, Status: "error", Error: &callError{Code: "approval_audit_failed", Message: "approved Host tool executed but its audit completion failed"}})
	}
	broker.record(call, operation, "ok", canonicalResult)
	if registered.spec.Playback == PlaybackCaptured {
		broker.recordTranscript(operation, call.Capability, arguments, canonicalResult, evidence)
	}
	return encodeResponse(response{CallID: call.CallID, Status: "ok", Result: canonicalResult})
}

func (broker *Broker) completeApproval(requestID, outcome string) bool {
	return requestID == "" || broker.config.ApprovalController.Complete(requestID, outcome) == nil
}

func (broker *Broker) record(call request, operation uint32, outcome string, result []byte) {
	created := receipt.NewAuthorized(broker.config.RunIdentity, broker.config.Plan.Identity(), call.CallID, call.ParentCallID, call.ApprovalRequestID, call.Capability, operation, string(call.Arguments), outcome, result)
	if call.Source != nil {
		bound, err := receipt.BindSource(created, *call.Source)
		if err != nil {
			created.ReceiptID = ""
		} else {
			created = bound
		}
	}
	broker.mu.Lock()
	broker.receipts = append(broker.receipts, created)
	broker.mu.Unlock()
}

func (broker *Broker) recordTranscript(operation uint32, capability string, arguments, result json.RawMessage, evidence TransportEvidence) {
	argumentsDigest := sha256.Sum256(arguments)
	resultDigest := sha256.Sum256(result)
	entry := TranscriptEntry{
		OperationIndex: operation, Capability: capability,
		Arguments: append(json.RawMessage(nil), arguments...), ArgumentsSHA256: fmt.Sprintf("sha256:%x", argumentsDigest[:]),
		Result: append(json.RawMessage(nil), result...), ResultSHA256: fmt.Sprintf("sha256:%x", resultDigest[:]), Evidence: evidence,
	}
	broker.mu.Lock()
	broker.transcript = append(broker.transcript, entry)
	broker.mu.Unlock()
}

func (broker *Broker) SnapshotTranscript() []TranscriptEntry {
	if broker == nil {
		return nil
	}
	broker.mu.Lock()
	defer broker.mu.Unlock()
	return cloneTranscript(broker.transcript)
}

func (broker *Broker) SnapshotReceipts() []receipt.Receipt {
	if broker == nil {
		return nil
	}
	broker.mu.Lock()
	defer broker.mu.Unlock()
	cloned := append([]receipt.Receipt(nil), broker.receipts...)
	for index := range cloned {
		if cloned[index].Source != nil {
			copy := *cloned[index].Source
			cloned[index].Source = &copy
		}
	}
	return cloned
}

func (broker *Broker) Calls() uint32 {
	if broker == nil {
		return 0
	}
	broker.mu.Lock()
	defer broker.mu.Unlock()
	return broker.calls
}

func (broker *Broker) RunIdentity() string {
	if broker == nil {
		return ""
	}
	return broker.config.RunIdentity
}

func (broker *Broker) SemanticPreDispatchEnabled() bool {
	return broker != nil && broker.config.SemanticPreDispatch
}

func (broker *Broker) ApprovalSuspensionEnabled() bool {
	return broker != nil && broker.config.ApprovalSuspension
}

func (broker *Broker) ProgrammaticParentBound() bool {
	return broker != nil && broker.config.ProgrammaticParentCallID != ""
}

func (broker *Broker) DirectCallsAllowedWithProgrammaticParent() bool {
	return broker != nil && broker.config.AllowDirectCalls
}

func (broker *Broker) CapabilityPlanSHA256() string {
	if broker == nil || broker.config.Plan == nil {
		return ""
	}
	return broker.config.Plan.Identity()
}

func (broker *Broker) Receipts() []receipt.Receipt { return broker.SnapshotReceipts() }
func (broker *Broker) CallCount() uint32           { return broker.Calls() }

// Finalize rejects unused offline records. CloseJournal remains a tiny lifecycle
// hook; this runtime has no durable transaction journal.
func (broker *Broker) Finalize(success bool) error {
	if broker == nil {
		return ErrInvalidBroker
	}
	broker.mu.Lock()
	if broker.playbackEntries != nil && (broker.playbackFailed || len(broker.playbackConsumed) != len(broker.playbackEntries)) {
		broker.mu.Unlock()
		return ErrPlaybackIncomplete
	}
	if err := broker.finalizeBranch(); err != nil {
		broker.mu.Unlock()
		return err
	}
	claimer := broker.config.StagedClaimer
	broker.mu.Unlock()
	if claimer != nil {
		return claimer.Finalize(success)
	}
	return nil
}
func (broker *Broker) CloseJournal() error { return nil }

func encodeResponse(value response) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode Host tool response: %w", err)
	}
	return encoded, nil
}

func validIdentity(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '_' && character != '-' && character != '.' && character != ':' {
			return false
		}
	}
	return true
}

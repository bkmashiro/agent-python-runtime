package capability

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
)

var (
	ErrPreDispatchUnavailable    = errors.New("pre-dispatch capability is unavailable")
	ErrPreDispatchAlreadyStarted = errors.New("pre-dispatch physical call already started")
	ErrPreDispatchInvalidResult  = errors.New("pre-dispatch result is outside the capability contract")
)

type StagedCapabilityOutcome struct {
	Result              json.RawMessage `json:"result,omitempty"`
	ErrorCode           string          `json:"error_code,omitempty"`
	PhysicalResultBytes uint64          `json:"-"`
}

func (outcome StagedCapabilityOutcome) Validate() error {
	hasResult := len(outcome.Result) != 0
	hasError := outcome.ErrorCode != ""
	if hasResult == hasError || (hasError && outcome.ErrorCode != "handler_error" && outcome.ErrorCode != "invalid_result") {
		return ErrPreDispatchUnavailable
	}
	return nil
}

// PreparedPreDispatch is a Host-only one-shot physical read prepared from a
// sealed Plan. Creating it validates capability eligibility and canonical
// arguments but starts no work.
type PreparedPreDispatch struct {
	mu             sync.Mutex
	registered     registration
	arguments      json.RawMessage
	maxResultBytes uint64
	started        bool
}

// PreparePreDispatch validates one exact read against the same sealed
// registration and schemas used by Broker. It grants no Guest-call authority;
// semantic legality and budget reservation remain separate Host obligations.
func (plan *Plan) PreparePreDispatch(name string, raw json.RawMessage) (*PreparedPreDispatch, error) {
	registered, ok := plan.lookup(name)
	qualification, qualified := preDispatchQualification(registered.spec)
	if !ok || !qualified || !qualification.Eligible() || registered.spec.Playback != PlaybackLiveOnly || len(raw) == 0 {
		return nil, ErrPreDispatchUnavailable
	}
	arguments, err := canonicalForSchema(registered.inputSchema, raw)
	if err != nil {
		return nil, ErrPreDispatchUnavailable
	}
	return &PreparedPreDispatch{
		registered: registered, arguments: append(json.RawMessage(nil), arguments...),
		maxResultBytes: qualification.Contract().MaxResultBytes,
	}, nil
}

// PrepareFuture validates any live capability for direct Future execution.
// Unlike speculative pre-dispatch, the Future is created only when Python
// reaches the call, so writes are allowed. Approval-gated and playback calls
// stay on the synchronous Broker path.
func (plan *Plan) PrepareFuture(name string, raw json.RawMessage) (*PreparedPreDispatch, error) {
	registered, ok := plan.lookup(name)
	if !ok || registered.spec.Playback != PlaybackLiveOnly || registered.spec.Approval != nil || len(raw) == 0 {
		return nil, ErrPreDispatchUnavailable
	}
	arguments, err := canonicalForSchema(registered.inputSchema, raw)
	if err != nil {
		return nil, ErrPreDispatchUnavailable
	}
	return &PreparedPreDispatch{
		registered: registered, arguments: append(json.RawMessage(nil), arguments...), maxResultBytes: maxCallBytes,
	}, nil
}

func (prepared *PreparedPreDispatch) Arguments() json.RawMessage {
	if prepared == nil {
		return nil
	}
	prepared.mu.Lock()
	defer prepared.mu.Unlock()
	return append(json.RawMessage(nil), prepared.arguments...)
}

// Call starts the physical operation exactly once. Scheduling is intentionally
// external: this method never creates a goroutine or task. Ordinary handler and
// result-schema failures are staged as logical outcomes so the unchanged Broker
// boundary can reproduce baseline exception semantics.
func (prepared *PreparedPreDispatch) Call(ctx context.Context) (StagedCapabilityOutcome, error) {
	if prepared == nil {
		return StagedCapabilityOutcome{}, ErrPreDispatchUnavailable
	}
	prepared.mu.Lock()
	if prepared.started {
		prepared.mu.Unlock()
		return StagedCapabilityOutcome{}, ErrPreDispatchAlreadyStarted
	}
	prepared.started = true
	registered := prepared.registered
	arguments := append(json.RawMessage(nil), prepared.arguments...)
	prepared.mu.Unlock()

	var result json.RawMessage
	var evidence TransportEvidence
	var err error
	if evidenced, ok := registered.handler.(EvidenceHandler); ok {
		result, evidence, err = evidenced.CallWithEvidence(ctx, arguments)
	} else {
		result, err = registered.handler.Call(ctx, arguments)
	}
	physicalResultBytes := uint64(len(result))
	if err != nil {
		if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
			return StagedCapabilityOutcome{PhysicalResultBytes: physicalResultBytes}, err
		}
		return StagedCapabilityOutcome{ErrorCode: "handler_error", PhysicalResultBytes: physicalResultBytes}, nil
	}
	canonical, err := canonicalForSchema(registered.outputSchema, result)
	if err == nil {
		err = validateSpecResultSemantics(registered.spec, canonical)
	}
	if err != nil || len(canonical) > maxCallBytes || len(canonical) > int(prepared.maxResultBytes) ||
		(registered.spec.Playback == PlaybackCaptured && !validLiveTransportEvidence(evidence)) {
		return StagedCapabilityOutcome{ErrorCode: "invalid_result", PhysicalResultBytes: physicalResultBytes}, nil
	}
	return StagedCapabilityOutcome{Result: append(json.RawMessage(nil), canonical...), PhysicalResultBytes: physicalResultBytes}, nil
}

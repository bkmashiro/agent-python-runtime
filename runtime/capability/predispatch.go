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

// PreparedPreDispatch is a Host-only one-shot physical read prepared from a
// sealed Plan. Creating it validates capability eligibility and canonical
// arguments but starts no work.
type PreparedPreDispatch struct {
	mu         sync.Mutex
	registered registration
	arguments  json.RawMessage
	started    bool
}

// PreparePreDispatch validates one exact read against the same sealed
// registration and schemas used by Broker. It grants no Guest-call authority;
// semantic legality and budget reservation remain separate Host obligations.
func (plan *Plan) PreparePreDispatch(name string, raw json.RawMessage) (*PreparedPreDispatch, error) {
	registered, ok := plan.lookup(name)
	qualification, qualified := preDispatchQualification(registered.spec)
	if !ok || !qualified || !qualification.Eligible() || len(raw) == 0 {
		return nil, ErrPreDispatchUnavailable
	}
	arguments, err := canonicalForSchema(registered.inputSchema, raw)
	if err != nil {
		return nil, ErrPreDispatchUnavailable
	}
	return &PreparedPreDispatch{registered: registered, arguments: append(json.RawMessage(nil), arguments...)}, nil
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
// external: this method never creates a goroutine or task.
func (prepared *PreparedPreDispatch) Call(ctx context.Context) (json.RawMessage, error) {
	if prepared == nil {
		return nil, ErrPreDispatchUnavailable
	}
	prepared.mu.Lock()
	if prepared.started {
		prepared.mu.Unlock()
		return nil, ErrPreDispatchAlreadyStarted
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
	if err != nil {
		return nil, err
	}
	canonical, err := canonicalForSchema(registered.outputSchema, result)
	if err == nil {
		err = validateSpecResultSemantics(registered.spec, canonical)
	}
	if err != nil || len(canonical) > maxCallBytes ||
		(registered.spec.Playback == PlaybackCaptured && !validLiveTransportEvidence(evidence)) {
		return nil, ErrPreDispatchInvalidResult
	}
	return append(json.RawMessage(nil), canonical...), nil
}

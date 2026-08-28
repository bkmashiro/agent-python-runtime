package capability

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// PreparedPLM is one Host-private physical preparation derived from a sealed
// PLM-enabled capability registration. It grants no logical call authority.
type PreparedPLM struct {
	mu         sync.Mutex
	registered registration
	arguments  json.RawMessage
	contract   PLMContract
	started    bool
}

func (plan *Plan) PreparePLM(name string, raw json.RawMessage) (*PreparedPLM, error) {
	registered, ok := plan.lookup(name)
	if !ok || registered.spec.PLM == nil || registered.spec.Playback != PlaybackLiveOnly || len(raw) == 0 {
		return nil, ErrSplitPhaseUnavailable
	}
	contract := *registered.spec.PLM
	if contract.Validate() != nil || contract.PrepareEffect == PrepareNone {
		return nil, ErrSplitPhaseUnavailable
	}
	arguments, err := canonicalForSchema(registered.inputSchema, raw)
	if err != nil {
		return nil, ErrSplitPhaseUnavailable
	}
	return &PreparedPLM{registered: registered, arguments: append(json.RawMessage(nil), arguments...), contract: contract}, nil
}

func (prepared *PreparedPLM) Arguments() json.RawMessage {
	if prepared == nil {
		return nil
	}
	prepared.mu.Lock()
	defer prepared.mu.Unlock()
	return append(json.RawMessage(nil), prepared.arguments...)
}

func (prepared *PreparedPLM) Contract() PLMContract {
	if prepared == nil {
		return PLMContract{}
	}
	return prepared.contract
}

func (prepared *PreparedPLM) HandlerIdentity() string {
	if prepared == nil {
		return ""
	}
	return prepared.registered.spec.HandlerIdentity
}

func (prepared *PreparedPLM) ResourceIdentity() (string, error) {
	if prepared == nil || prepared.contract.Temporal == TemporalWallclockObserving {
		return "", ErrInvalidPLMContract
	}
	resource := prepared.contract.Resource
	if resource.Constant != "" {
		return resource.Namespace + ":" + resource.Constant, nil
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(prepared.Arguments(), &object) != nil {
		return "", ErrInvalidPLMContract
	}
	raw, ok := object[resource.Argument]
	if !ok {
		return "", ErrInvalidPLMContract
	}
	_, canonical, err := canonicalJSON(raw)
	if err != nil {
		return "", ErrInvalidPLMContract
	}
	digest := sha256.Sum256(canonical)
	return fmt.Sprintf("%s:sha256:%x", resource.Namespace, digest[:]), nil
}

func (plan *Plan) PLMResourceIdentity(name string, raw json.RawMessage) (string, error) {
	prepared, err := plan.PreparePLM(name, raw)
	if err != nil {
		return "", err
	}
	return prepared.ResourceIdentity()
}

func (prepared *PreparedPLM) Validate(ctx context.Context, request PLMValidationRequest) (PLMValidationResult, error) {
	if prepared == nil || ctx == nil {
		return PLMValidationResult{}, ErrInvalidPLMContract
	}
	validator, ok := prepared.registered.handler.(PLMValidator)
	if !ok {
		return PLMValidationResult{}, ErrInvalidPLMContract
	}
	return validator.ValidatePLM(ctx, request)
}

func (prepared *PreparedPLM) ProviderSessionIdentity(ctx context.Context) (string, error) {
	if prepared == nil || ctx == nil {
		return "", ErrInvalidPLMContract
	}
	provider, ok := prepared.registered.handler.(PLMProviderSession)
	if !ok {
		return "", ErrInvalidPLMContract
	}
	identity := provider.PLMProviderSessionIdentity(ctx)
	if !validIdentity(identity) {
		return "", ErrInvalidPLMContract
	}
	return identity, nil
}

func (prepared *PreparedPLM) Call(ctx context.Context) (StagedCapabilityOutcome, error) {
	if prepared == nil || ctx == nil {
		return StagedCapabilityOutcome{}, ErrSplitPhaseUnavailable
	}
	prepared.mu.Lock()
	if prepared.started {
		prepared.mu.Unlock()
		return StagedCapabilityOutcome{}, ErrPreDispatchAlreadyStarted
	}
	prepared.started = true
	registered := prepared.registered
	arguments := append(json.RawMessage(nil), prepared.arguments...)
	contract := prepared.contract
	prepared.mu.Unlock()

	if contract.PrepareEffect == PrepareTransportOnly {
		transport, ok := registered.handler.(PLMTransportPreparer)
		if !ok {
			return StagedCapabilityOutcome{}, ErrPLMTransportUnavailable
		}
		if err := transport.PreparePLMTransport(ctx, arguments); err != nil {
			return StagedCapabilityOutcome{}, err
		}
		return StagedCapabilityOutcome{}, nil
	}
	if contract.PrepareEffect != PrepareSilentRead {
		return StagedCapabilityOutcome{}, ErrSplitPhaseUnavailable
	}

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
		var uncertain PLMProviderOutcomeUncertain
		if errors.As(err, &uncertain) && uncertain.ProviderOutcomeUncertain() {
			return StagedCapabilityOutcome{ErrorCode: PLMProviderOutcomeUncertainCode, PhysicalResultBytes: physicalResultBytes}, nil
		}
		if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
			return StagedCapabilityOutcome{PhysicalResultBytes: physicalResultBytes}, err
		}
		return StagedCapabilityOutcome{ErrorCode: "handler_error", PhysicalResultBytes: physicalResultBytes}, nil
	}
	canonical, err := canonicalForSchema(registered.outputSchema, result)
	if err == nil {
		err = validateSpecResultSemantics(registered.spec, canonical)
	}
	if err != nil || len(canonical) > maxCallBytes || len(canonical) > int(contract.MaxResultBytes) ||
		(registered.spec.Playback == PlaybackCaptured && !validLiveTransportEvidence(evidence)) {
		return StagedCapabilityOutcome{ErrorCode: "invalid_result", PhysicalResultBytes: physicalResultBytes}, nil
	}
	return StagedCapabilityOutcome{Result: append(json.RawMessage(nil), canonical...), PhysicalResultBytes: physicalResultBytes}, nil
}

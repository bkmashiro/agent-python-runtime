package evaluationv2

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

type ConditionOutcome struct {
	Result                  json.RawMessage
	CapabilityCalls         uint32
	ControllerBoundaries    uint32
	ControllerRequestBytes  uint64
	ControllerResponseBytes uint64
	BrokerRequestBytes      uint64
	BrokerResponseBytes     uint64
}

func DeriveMetrics(definition Definition, condition Condition, controllerRequestBytes, controllerResponseBytes uint64, broker *capability.Broker) (PilotMetrics, error) {
	if broker == nil || validateDefinition(definition) != nil || controllerRequestBytes == 0 || controllerResponseBytes == 0 {
		return PilotMetrics{}, ErrInvalid
	}
	transcript := broker.SnapshotTranscript()
	receipts := broker.SnapshotReceipts()
	calls := broker.CallCount()
	if calls == 0 || len(transcript) != int(calls) || len(receipts) != int(calls) {
		return PilotMetrics{}, ErrInvalid
	}
	metrics := PilotMetrics{ControllerRequestBytes: controllerRequestBytes, ControllerResponseBytes: controllerResponseBytes, CapabilityCalls: calls, Receipts: uint32(len(receipts)), TranscriptEntries: uint32(len(transcript))}
	switch condition {
	case ConditionDirect:
		metrics.ControllerBoundaries = calls
	case ConditionGuest:
		metrics.ControllerBoundaries = 1
	default:
		return PilotMetrics{}, ErrInvalid
	}
	for index, entry := range transcript {
		if index >= len(definition.Workload.RequiredCapabilities) || entry.Capability != definition.Workload.RequiredCapabilities[index] {
			return PilotMetrics{}, ErrInvalid
		}
		canonical, err := canonicalJSON(entry.Result)
		if err != nil {
			return PilotMetrics{}, ErrInvalid
		}
		sourceHash := sha256.Sum256(canonical)
		if fmt.Sprintf("sha256:%x", sourceHash) != definition.Workload.SourceFixtureSHA256[index] {
			return PilotMetrics{}, ErrInvalid
		}
		if math.MaxUint64-metrics.CapabilityArgumentBytes < uint64(len(entry.Arguments)) || math.MaxUint64-metrics.CapabilityResultBytes < uint64(len(entry.Result)) {
			return PilotMetrics{}, ErrInvalid
		}
		metrics.CapabilityArgumentBytes += uint64(len(entry.Arguments))
		metrics.CapabilityResultBytes += uint64(len(entry.Result))
	}
	return metrics, nil
}

type brokerResponse struct {
	CallID string          `json:"call_id"`
	Status string          `json:"status"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

func RunDirect(ctx context.Context, definition Definition, broker *capability.Broker) (ConditionOutcome, error) {
	if broker == nil || validateDefinition(definition) != nil {
		return ConditionOutcome{}, ErrInvalid
	}
	results := make(map[string]json.RawMessage, len(definition.Workload.RequiredCapabilities))
	outcome := ConditionOutcome{}
	for index, name := range definition.Workload.RequiredCapabilities {
		request, err := json.Marshal(struct {
			CallID     string         `json:"call_id"`
			Capability string         `json:"capability"`
			Arguments  map[string]any `json:"arguments"`
		}{CallID: fmt.Sprintf("direct-%d", index), Capability: name, Arguments: map[string]any{}})
		if err != nil {
			return ConditionOutcome{}, ErrInvalid
		}
		response, err := broker.Call(ctx, request)
		if err != nil {
			return ConditionOutcome{}, err
		}
		decoded, err := decodeBrokerResponse(response)
		if err != nil || decoded.Status != "ok" || len(decoded.Result) == 0 {
			return ConditionOutcome{}, ErrInvalid
		}
		results[name] = append(json.RawMessage(nil), decoded.Result...)
		outcome.ControllerBoundaries++
		outcome.ControllerRequestBytes += uint64(len(request))
		outcome.ControllerResponseBytes += uint64(len(response))
		outcome.BrokerRequestBytes += uint64(len(request))
		outcome.BrokerResponseBytes += uint64(len(response))
	}
	result, err := transformDirect(definition, results)
	if err != nil {
		return ConditionOutcome{}, err
	}
	outcome.Result = result
	outcome.CapabilityCalls = broker.CallCount()
	if VerifyResult(definition, outcome.Result, outcome.CapabilityCalls) != nil {
		return ConditionOutcome{}, ErrInvalid
	}
	return outcome, nil
}

func decodeBrokerResponse(raw []byte) (brokerResponse, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var value brokerResponse
	if decoder.Decode(&value) != nil {
		return brokerResponse{}, ErrInvalid
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return brokerResponse{}, ErrInvalid
	}
	return value, nil
}

func transformDirect(definition Definition, results map[string]json.RawMessage) (json.RawMessage, error) {
	return transformExpandedDirect(definition.Workload.ID, results, definition.Inputs)
}

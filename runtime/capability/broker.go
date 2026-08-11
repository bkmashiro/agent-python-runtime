package capability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/bkmashiro/agent-python-runtime/runtime/receipt"
)

const maxCallBytes = 1 << 20

var ErrInvalidBroker = errors.New("invalid Host tool broker")

type Config struct {
	RunIdentity string
	Plan        *Plan
}

type Broker struct {
	config   Config
	mu       sync.Mutex
	calls    uint32
	seen     map[string]struct{}
	receipts []receipt.Receipt
}

type request struct {
	CallID     string          `json:"call_id"`
	Capability string          `json:"capability"`
	Arguments  json.RawMessage `json:"arguments"`
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
	if !validIdentity(config.RunIdentity) || config.Plan == nil || config.Plan.Identity() == "" || config.Plan.MaxCalls() == 0 {
		return nil, ErrInvalidBroker
	}
	return &Broker{config: config, seen: make(map[string]struct{})}, nil
}

func (broker *Broker) Call(ctx context.Context, raw []byte) ([]byte, error) {
	if broker == nil || len(raw) == 0 || len(raw) > maxCallBytes {
		return nil, ErrInvalidBroker
	}
	var call request
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&call); err != nil || !validIdentity(call.CallID) || !validName(call.Capability) || len(call.Arguments) == 0 || !json.Valid(call.Arguments) {
		return encodeResponse(response{CallID: call.CallID, Status: "error", Error: &callError{Code: "invalid_arguments", Message: "invalid Host tool call"}})
	}

	broker.mu.Lock()
	if broker.calls >= broker.config.Plan.MaxCalls() {
		broker.mu.Unlock()
		return encodeResponse(response{CallID: call.CallID, Status: "error", Error: &callError{Code: "call_budget_exceeded", Message: "Host tool call budget exhausted"}})
	}
	if _, duplicate := broker.seen[call.CallID]; duplicate {
		broker.mu.Unlock()
		return encodeResponse(response{CallID: call.CallID, Status: "error", Error: &callError{Code: "duplicate_call_id", Message: "call_id must be unique"}})
	}
	broker.seen[call.CallID] = struct{}{}
	operation := broker.calls
	broker.calls++
	broker.mu.Unlock()

	handler, ok := broker.config.Plan.lookup(call.Capability)
	if !ok {
		broker.record(call, operation, "denied", nil)
		return encodeResponse(response{CallID: call.CallID, Status: "denied", Error: &callError{Code: "capability_denied", Message: "Host tool is not granted"}})
	}
	result, err := handler.Call(ctx, append(json.RawMessage(nil), call.Arguments...))
	if err != nil {
		broker.record(call, operation, "error", nil)
		return encodeResponse(response{CallID: call.CallID, Status: "error", Error: &callError{Code: "handler_error", Message: "Host tool failed"}})
	}
	if len(result) == 0 || !json.Valid(result) || len(result) > maxCallBytes {
		broker.record(call, operation, "error", nil)
		return encodeResponse(response{CallID: call.CallID, Status: "error", Error: &callError{Code: "invalid_result", Message: "Host tool returned invalid JSON"}})
	}
	broker.record(call, operation, "ok", result)
	return encodeResponse(response{CallID: call.CallID, Status: "ok", Result: result})
}

func (broker *Broker) record(call request, operation uint32, outcome string, result []byte) {
	created := receipt.New(broker.config.RunIdentity, broker.config.Plan.Identity(), call.CallID, call.Capability, operation, string(call.Arguments), outcome, result)
	broker.mu.Lock()
	broker.receipts = append(broker.receipts, created)
	broker.mu.Unlock()
}

func (broker *Broker) SnapshotReceipts() []receipt.Receipt {
	if broker == nil {
		return nil
	}
	broker.mu.Lock()
	defer broker.mu.Unlock()
	return append([]receipt.Receipt(nil), broker.receipts...)
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

func (broker *Broker) CapabilityPlanSHA256() string {
	if broker == nil || broker.config.Plan == nil {
		return ""
	}
	return broker.config.Plan.Identity()
}

func (broker *Broker) Receipts() []receipt.Receipt { return broker.SnapshotReceipts() }
func (broker *Broker) CallCount() uint32           { return broker.Calls() }

// Finalize and CloseJournal remain tiny lifecycle hooks for the engine. The PoC
// deliberately has no durable transaction journal or recovery state machine.
func (broker *Broker) Finalize(bool) error { return nil }
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

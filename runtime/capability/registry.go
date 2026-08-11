package capability

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
)

var (
	ErrInvalidTool    = errors.New("invalid Host tool")
	ErrToolExists     = errors.New("Host tool is already registered")
	ErrRegistrySealed = errors.New("Host tool registry is sealed")
)

const capabilityPlanSchemaVersion = "pysolate.capability-plan.v1"

// Handler is the entire PoC Host-tool contract. The Host owns registration and
// authority; generated Python can only submit JSON arguments.
type Handler interface {
	Call(context.Context, json.RawMessage) (json.RawMessage, error)
}

type HandlerFunc func(context.Context, json.RawMessage) (json.RawMessage, error)

func (function HandlerFunc) Call(ctx context.Context, arguments json.RawMessage) (json.RawMessage, error) {
	return function(ctx, arguments)
}

type registration struct {
	handlerIdentity string
	handler         Handler
}

type Registry struct {
	mu       sync.RWMutex
	handlers map[string]registration
	sealed   bool
}

type CapabilityBinding struct {
	Name            string `json:"capability"`
	HandlerIdentity string `json:"handler_identity"`
}

type PlanConfig struct {
	MaxCalls uint32
}

type Plan struct {
	identity     string
	maxCalls     uint32
	capabilities []CapabilityBinding
	handlers     map[string]Handler
}

func NewRegistry() *Registry {
	return &Registry{handlers: make(map[string]registration)}
}

func (registry *Registry) Register(name, handlerIdentity string, handler Handler) error {
	if registry == nil || !validName(name) || !validHandlerIdentity(handlerIdentity) || handler == nil {
		return ErrInvalidTool
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.sealed {
		return ErrRegistrySealed
	}
	if _, exists := registry.handlers[name]; exists {
		return ErrToolExists
	}
	registry.handlers[name] = registration{handlerIdentity: handlerIdentity, handler: handler}
	return nil
}

func (registry *Registry) Seal(config PlanConfig) (*Plan, error) {
	if registry == nil || config.MaxCalls == 0 {
		return nil, ErrInvalidTool
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.sealed {
		return nil, ErrRegistrySealed
	}
	registry.sealed = true
	capabilities := make([]CapabilityBinding, 0, len(registry.handlers))
	handlers := make(map[string]Handler, len(registry.handlers))
	for name, registered := range registry.handlers {
		capabilities = append(capabilities, CapabilityBinding{Name: name, HandlerIdentity: registered.handlerIdentity})
		handlers[name] = registered.handler
	}
	sortCapabilityBindings(capabilities)
	document := struct {
		SchemaVersion string              `json:"schema_version"`
		MaxCalls      uint32              `json:"max_calls"`
		Capabilities  []CapabilityBinding `json:"capabilities"`
	}{SchemaVersion: capabilityPlanSchemaVersion, MaxCalls: config.MaxCalls, Capabilities: capabilities}
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, ErrInvalidTool
	}
	digest := sha256.Sum256(encoded)
	return &Plan{
		identity:     "sha256:" + hex.EncodeToString(digest[:]),
		maxCalls:     config.MaxCalls,
		capabilities: append([]CapabilityBinding(nil), capabilities...),
		handlers:     handlers,
	}, nil
}

func (plan *Plan) Identity() string {
	if plan == nil {
		return ""
	}
	return plan.identity
}

func (plan *Plan) MaxCalls() uint32 {
	if plan == nil {
		return 0
	}
	return plan.maxCalls
}

func (plan *Plan) Capabilities() []CapabilityBinding {
	if plan == nil {
		return nil
	}
	return append([]CapabilityBinding(nil), plan.capabilities...)
}

func (plan *Plan) lookup(name string) (Handler, bool) {
	if plan == nil {
		return nil, false
	}
	handler, ok := plan.handlers[name]
	return handler, ok
}

func sortCapabilityBindings(values []CapabilityBinding) {
	for index := 1; index < len(values); index++ {
		for current := index; current > 0 && values[current].Name < values[current-1].Name; current-- {
			values[current], values[current-1] = values[current-1], values[current]
		}
	}
}

func validName(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' && character != '.' {
			return false
		}
	}
	return true
}

func validHandlerIdentity(value string) bool {
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

package capability

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
)

var (
	ErrInvalidTool = errors.New("invalid Host tool")
	ErrToolExists  = errors.New("Host tool is already registered")
)

// Handler is the entire PoC Host-tool contract. The Host owns registration and
// authority; generated Python can only submit JSON arguments.
type Handler interface {
	Call(context.Context, json.RawMessage) (json.RawMessage, error)
}

type HandlerFunc func(context.Context, json.RawMessage) (json.RawMessage, error)

func (function HandlerFunc) Call(ctx context.Context, arguments json.RawMessage) (json.RawMessage, error) {
	return function(ctx, arguments)
}

type Registry struct {
	mu       sync.RWMutex
	handlers map[string]Handler
}

func NewRegistry() *Registry {
	return &Registry{handlers: make(map[string]Handler)}
}

func (registry *Registry) Register(name string, handler Handler) error {
	if registry == nil || !validName(name) || handler == nil {
		return ErrInvalidTool
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if _, exists := registry.handlers[name]; exists {
		return ErrToolExists
	}
	registry.handlers[name] = handler
	return nil
}

func (registry *Registry) lookup(name string) (Handler, bool) {
	if registry == nil {
		return nil, false
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	handler, ok := registry.handlers[name]
	return handler, ok
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

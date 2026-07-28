package fakeadapter

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

var (
	ErrCredentialDenied = errors.New("fake provider credential denied")
	ErrVersionDrift     = errors.New("fake provider version drift")
	ErrUnknownOperation = errors.New("fake provider operation is unknown")
	ErrAmbiguousCommit  = errors.New("fake provider committed before response loss")
)

type Record struct {
	Value   string `json:"value"`
	Version uint64 `json:"version"`
}

type Provider struct {
	mu                 sync.Mutex
	expectedCredential []byte
	record             Record
	ambiguousNext      bool
	compensations      int
}

func NewProvider(initial string, expectedCredential []byte) (*Provider, error) {
	if len(expectedCredential) == 0 {
		return nil, ErrCredentialDenied
	}
	return &Provider{expectedCredential: append([]byte(nil), expectedCredential...), record: Record{Value: initial, Version: 1}}, nil
}

func (provider *Provider) Close() {
	if provider == nil {
		return
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	for i := range provider.expectedCredential {
		provider.expectedCredential[i] = 0
	}
	provider.expectedCredential = nil
}

func (provider *Provider) SetAmbiguousNext() {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.ambiguousNext = true
}

func (provider *Provider) Drift(value string) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.record.Value = value
	provider.record.Version++
}

func (provider *Provider) Snapshot() (Record, int) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.record, provider.compensations
}

type undoRecord struct {
	Before Record
	After  Record
}

type Config struct {
	Resolver      capability.SecretResolver
	SecretRef     capability.SecretRef
	RunIdentity   string
	TaskIdentity  string
	Tenant        string
	ResourceAlias string
	PolicyVersion string
	LeaseDuration time.Duration
	Provider      *Provider
}

type ChangeAdapter struct {
	config Config
	mu     sync.Mutex
	undo   map[string]undoRecord
}

func NewChangeAdapter(config Config) (*ChangeAdapter, error) {
	if config.Resolver == nil || config.Provider == nil || config.SecretRef == "" || config.RunIdentity == "" || config.TaskIdentity == "" ||
		config.Tenant == "" || config.ResourceAlias == "" || config.PolicyVersion == "" ||
		config.LeaseDuration <= 0 || config.LeaseDuration > time.Hour {
		return nil, errors.New("invalid fake adapter configuration")
	}
	return &ChangeAdapter{config: config, undo: map[string]undoRecord{}}, nil
}

type changeArguments struct {
	Value string `json:"value"`
}

func (adapter *ChangeAdapter) Handle(ctx context.Context, call capability.HostCall) (json.RawMessage, error) {
	if call.RunIdentity != adapter.config.RunIdentity {
		return nil, capability.NewHandlerFailure("identity_denied", "fake provider Run identity was denied", errors.New("Run identity mismatch"))
	}
	var arguments changeArguments
	if err := decodeStrict(call.Arguments, &arguments); err != nil || arguments.Value == "" || len(arguments.Value) > 4096 {
		return nil, capability.NewHandlerFailure("invalid_arguments", "fake provider arguments are invalid", errors.New("invalid value"))
	}
	var before, after Record
	var ambiguous bool
	err := adapter.withCredential(ctx, call.ToolID, "apply", func(credential []byte) error {
		var applyErr error
		before, after, ambiguous, applyErr = adapter.config.Provider.apply(credential, arguments.Value)
		return applyErr
	})
	if err != nil {
		return nil, classifyFailure(err)
	}
	adapter.mu.Lock()
	adapter.undo[call.OperationID] = undoRecord{Before: before, After: after}
	adapter.mu.Unlock()
	if ambiguous {
		return nil, capability.NewAmbiguousHandlerFailure("provider_ambiguous", "fake provider outcome is ambiguous", ErrAmbiguousCommit)
	}
	return json.Marshal(after)
}

func (adapter *ChangeAdapter) Rollback(ctx context.Context, call capability.AbortCall) error {
	adapter.mu.Lock()
	undo, exists := adapter.undo[call.Operation.ID]
	adapter.mu.Unlock()
	if !exists {
		return ErrUnknownOperation
	}
	return adapter.withCredential(ctx, call.Operation.ToolID, "rollback", func(credential []byte) error {
		return adapter.config.Provider.rollback(credential, undo)
	})
}

func (adapter *ChangeAdapter) Compensate(ctx context.Context, call capability.AbortCall) error {
	adapter.mu.Lock()
	_, exists := adapter.undo[call.Operation.ID]
	adapter.mu.Unlock()
	if !exists {
		return ErrUnknownOperation
	}
	return adapter.withCredential(ctx, call.Operation.ToolID, "compensate", func(credential []byte) error {
		return adapter.config.Provider.compensate(credential)
	})
}

func (adapter *ChangeAdapter) withCredential(ctx context.Context, toolID, operation string, use func([]byte) error) error {
	lease, err := adapter.config.Resolver.Resolve(ctx, capability.SecretRequest{
		Reference: adapter.config.SecretRef, RunIdentity: adapter.config.RunIdentity, TaskIdentity: adapter.config.TaskIdentity,
		Tenant: adapter.config.Tenant, ToolID: toolID, Operation: operation, ResourceAlias: adapter.config.ResourceAlias,
		PolicyVersion: adapter.config.PolicyVersion, RequestedLease: adapter.config.LeaseDuration,
	})
	if err != nil {
		return err
	}
	defer lease.Release()
	return lease.WithValue(use)
}

func (provider *Provider) authenticate(credential []byte) bool {
	return len(credential) == len(provider.expectedCredential) && subtle.ConstantTimeCompare(credential, provider.expectedCredential) == 1
}

func (provider *Provider) apply(credential []byte, value string) (Record, Record, bool, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if !provider.authenticate(credential) {
		return Record{}, Record{}, false, ErrCredentialDenied
	}
	before := provider.record
	provider.record = Record{Value: value, Version: before.Version + 1}
	ambiguous := provider.ambiguousNext
	provider.ambiguousNext = false
	return before, provider.record, ambiguous, nil
}

func (provider *Provider) rollback(credential []byte, undo undoRecord) error {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if !provider.authenticate(credential) {
		return ErrCredentialDenied
	}
	if provider.record != undo.After {
		return ErrVersionDrift
	}
	provider.record = Record{Value: undo.Before.Value, Version: provider.record.Version + 1}
	return nil
}

func (provider *Provider) compensate(credential []byte) error {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if !provider.authenticate(credential) {
		return ErrCredentialDenied
	}
	provider.compensations++
	return nil
}

func classifyFailure(err error) error {
	if errors.Is(err, capability.ErrSecretDenied) || errors.Is(err, capability.ErrSecretExpired) || errors.Is(err, capability.ErrSecretReleased) || errors.Is(err, ErrCredentialDenied) {
		return capability.NewHandlerFailure("credential_denied", "fake provider credential was denied", err)
	}
	return capability.NewHandlerFailure("provider_failed", "fake provider operation failed", err)
}

func decodeStrict(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

var _ capability.Handler = (*ChangeAdapter)(nil)
var _ capability.AbortHandler = (*ChangeAdapter)(nil)

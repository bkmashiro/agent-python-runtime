package capability

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrSecretDenied   = errors.New("Host secret resolution denied")
	ErrSecretExpired  = errors.New("Host secret lease expired")
	ErrSecretReleased = errors.New("Host secret lease released")
)

type SecretRef string

type SecretRequest struct {
	Reference      SecretRef
	RunIdentity    string
	TaskIdentity   string
	Tenant         string
	ToolID         string
	Operation      string
	ResourceAlias  string
	PolicyVersion  string
	RequestedLease time.Duration
}

type SecretAudit struct {
	RunIdentity   string
	TaskIdentity  string
	Tenant        string
	ToolID        string
	Operation     string
	ResourceAlias string
	PolicyVersion string
	Outcome       string
}

type SecretObserver func(SecretAudit)

type SecretResolver interface {
	Resolve(context.Context, SecretRequest) (*SecretLease, error)
}

type SecretLease struct {
	mu        sync.Mutex
	value     []byte
	expiresAt time.Time
	now       func() time.Time
	released  bool
}

func (lease *SecretLease) String() string   { return "[REDACTED]" }
func (lease *SecretLease) GoString() string { return "[REDACTED]" }

func (lease *SecretLease) ExpiresAt() time.Time {
	if lease == nil {
		return time.Time{}
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	return lease.expiresAt
}

func (lease *SecretLease) WithValue(use func([]byte) error) error {
	if lease == nil || use == nil {
		return ErrSecretDenied
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.released {
		return ErrSecretReleased
	}
	if !lease.expiresAt.After(lease.now()) {
		return ErrSecretExpired
	}
	value := append([]byte(nil), lease.value...)
	defer zeroBytes(value)
	return use(value)
}

func (lease *SecretLease) Release() {
	if lease == nil {
		return
	}
	lease.mu.Lock()
	defer lease.mu.Unlock()
	if lease.released {
		return
	}
	zeroBytes(lease.value)
	lease.value = nil
	lease.released = true
}

// StaticSecretResolver is a bounded in-memory resolver for deterministic tests
// and local development. It is not a production secret-store integration.
type StaticSecretResolver struct {
	mu       sync.RWMutex
	secrets  map[SecretRef][]byte
	now      func() time.Time
	observer SecretObserver
}

func NewStaticSecretResolver(secrets map[SecretRef][]byte, now func() time.Time, observer SecretObserver) (*StaticSecretResolver, error) {
	if len(secrets) == 0 || len(secrets) > 1024 || now == nil {
		return nil, ErrSecretDenied
	}
	cloned := make(map[SecretRef][]byte, len(secrets))
	for reference, value := range secrets {
		if !validSecretRef(reference) || len(value) == 0 || len(value) > 64*1024 {
			return nil, ErrSecretDenied
		}
		cloned[reference] = append([]byte(nil), value...)
	}
	return &StaticSecretResolver{secrets: cloned, now: now, observer: observer}, nil
}

func (resolver *StaticSecretResolver) Close() {
	if resolver == nil {
		return
	}
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	for reference, value := range resolver.secrets {
		zeroBytes(value)
		delete(resolver.secrets, reference)
	}
}

func (resolver *StaticSecretResolver) Resolve(ctx context.Context, request SecretRequest) (*SecretLease, error) {
	if resolver == nil || ctx == nil || ctx.Err() != nil || !validSecretRequest(request) {
		return nil, ErrSecretDenied
	}
	resolver.mu.RLock()
	value, exists := resolver.secrets[request.Reference]
	if exists {
		value = append([]byte(nil), value...)
	}
	resolver.mu.RUnlock()
	outcome := "denied"
	if exists {
		outcome = "resolved"
	}
	if resolver.observer != nil {
		resolver.observer(SecretAudit{RunIdentity: request.RunIdentity, TaskIdentity: request.TaskIdentity, Tenant: request.Tenant, ToolID: request.ToolID, Operation: request.Operation, ResourceAlias: request.ResourceAlias, PolicyVersion: request.PolicyVersion, Outcome: outcome})
	}
	if !exists {
		return nil, ErrSecretDenied
	}
	return &SecretLease{value: value, expiresAt: resolver.now().Add(request.RequestedLease), now: resolver.now}, nil
}

func validSecretRef(reference SecretRef) bool {
	value := string(reference)
	return len(value) > 0 && len(value) <= 256 && validIdentifier(value)
}

func validSecretRequest(request SecretRequest) bool {
	return validSecretRef(request.Reference) && validIdentifier(request.RunIdentity) && validIdentifier(request.TaskIdentity) &&
		validIdentifier(request.Tenant) && validIdentifier(request.ToolID) && validIdentifier(request.Operation) &&
		validIdentifier(request.ResourceAlias) && validIdentifier(request.PolicyVersion) &&
		request.RequestedLease > 0 && request.RequestedLease <= 24*time.Hour
}

func zeroBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

var _ fmt.Stringer = (*SecretLease)(nil)

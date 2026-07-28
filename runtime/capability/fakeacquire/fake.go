package fakeacquire

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability/fakeworkspace"
	"github.com/bkmashiro/agent-python-runtime/runtime/toolcatalog"
)

const (
	ToolID         = "repo.acquire"
	HandlerVersion = "fake-acquire-v1"
	maxSources     = 1024
	maxSourceFiles = 4096
	maxSourceBytes = 16 << 20
)

var (
	ErrSourceDenied     = errors.New("fake acquisition source denied")
	ErrCredentialDenied = errors.New("fake acquisition credential denied")
)

type Source struct {
	Alias           string
	RepositoryAlias string
	Revision        string
	ManifestDigest  string
	Files           map[string][]byte
}

type Provider struct {
	mu         sync.Mutex
	credential []byte
	sources    map[string]Source
}

func NewProvider(sources []Source, credential []byte) (*Provider, error) {
	if len(sources) == 0 || len(sources) > maxSources || len(credential) == 0 {
		return nil, ErrSourceDenied
	}
	provider := &Provider{credential: append([]byte(nil), credential...), sources: make(map[string]Source, len(sources))}
	for _, source := range sources {
		if !validIdentity(source.Alias) || !validIdentity(source.RepositoryAlias) || strings.Contains(source.RepositoryAlias, "/") || !validRevision(source.Revision) || !validDigest(source.ManifestDigest) || len(source.Files) == 0 || len(source.Files) > maxSourceFiles {
			provider.Close()
			return nil, ErrSourceDenied
		}
		if _, duplicate := provider.sources[source.Alias]; duplicate {
			provider.Close()
			return nil, ErrSourceDenied
		}
		copySource, err := cloneSource(source)
		if err != nil {
			provider.Close()
			return nil, err
		}
		provider.sources[source.Alias] = copySource
	}
	return provider, nil
}

func (provider *Provider) Close() {
	if provider == nil {
		return
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	zero(provider.credential)
	for alias, source := range provider.sources {
		zeroFiles(source.Files)
		delete(provider.sources, alias)
	}
	provider.credential = nil
	provider.sources = nil
}
func (provider *Provider) resolve(credential []byte, alias string) (Source, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if !sameSecret(credential, provider.credential) {
		return Source{}, ErrCredentialDenied
	}
	source, exists := provider.sources[alias]
	if !exists {
		return Source{}, ErrSourceDenied
	}
	return cloneSource(source)
}

type Config struct {
	Resolver            capability.SecretResolver
	SourceSecretRef     capability.SecretRef
	RunIdentity         string
	TaskIdentity        string
	Tenant              string
	SourceRegistryAlias string
	PolicyVersion       string
	LeaseDuration       time.Duration
	Provider            *Provider
	Store               *fakeworkspace.Store
}

type Adapter struct{ config Config }

func NewAdapter(config Config) (*Adapter, error) {
	if config.Resolver == nil || config.Provider == nil || config.Store == nil || config.SourceSecretRef == "" || !validIdentity(config.RunIdentity) || !validIdentity(config.TaskIdentity) || !validIdentity(config.Tenant) || !validIdentity(config.SourceRegistryAlias) || !validIdentity(config.PolicyVersion) || config.LeaseDuration <= 0 || config.LeaseDuration > time.Hour {
		return nil, ErrSourceDenied
	}
	return &Adapter{config: config}, nil
}

type arguments struct {
	SourceAlias string `json:"source_alias"`
}
type Result struct {
	SourceAlias string `json:"source_alias"`
	fakeworkspace.WorkspaceDescriptor
}

func (adapter *Adapter) Handle(ctx context.Context, call capability.HostCall) (json.RawMessage, error) {
	if call.RunIdentity != adapter.config.RunIdentity {
		return nil, classified(fakeworkspace.ErrWorkspaceDenied)
	}
	var args arguments
	if json.Unmarshal(call.Arguments, &args) != nil || !validIdentity(args.SourceAlias) {
		return nil, classified(ErrSourceDenied)
	}
	lease, err := adapter.config.Resolver.Resolve(ctx, capability.SecretRequest{Reference: adapter.config.SourceSecretRef, RunIdentity: adapter.config.RunIdentity, TaskIdentity: adapter.config.TaskIdentity, Tenant: adapter.config.Tenant, ToolID: ToolID, Operation: "acquire", ResourceAlias: adapter.config.SourceRegistryAlias, PolicyVersion: adapter.config.PolicyVersion, RequestedLease: adapter.config.LeaseDuration})
	if err != nil {
		return nil, classified(err)
	}
	defer lease.Release()
	var source Source
	err = lease.WithValue(func(secret []byte) error {
		var e error
		source, e = adapter.config.Provider.resolve(secret, args.SourceAlias)
		return e
	})
	if err != nil {
		return nil, classified(err)
	}
	defer zeroFiles(source.Files)
	descriptor, err := adapter.config.Store.Acquire(fakeworkspace.Binding{RunIdentity: adapter.config.RunIdentity, TaskIdentity: adapter.config.TaskIdentity}, fakeworkspace.Fixture{Alias: source.RepositoryAlias, Revision: source.Revision, Files: source.Files}, source.ManifestDigest)
	if err != nil {
		return nil, classified(err)
	}
	return json.Marshal(Result{SourceAlias: source.Alias, WorkspaceDescriptor: descriptor})
}

func HandlerSpecs(adapter *Adapter) ([]capability.HandlerSpec, error) {
	if adapter == nil {
		return nil, ErrSourceDenied
	}
	return []capability.HandlerSpec{{ToolID: ToolID, HandlerVersion: HandlerVersion, InputSchema: []byte(inputSchema), OutputSchema: []byte(outputSchema), Handler: adapter}}, nil
}
func ToolGrants(policyVersion string, maxCalls uint32) (map[string]capability.ToolGrant, error) {
	if !validIdentity(policyVersion) || maxCalls == 0 || maxCalls > 1024 {
		return nil, ErrSourceDenied
	}
	return map[string]capability.ToolGrant{ToolID: {ToolID: ToolID, HandlerVersion: HandlerVersion, EffectClass: "read_only", Policy: "AUTO_COMMIT", PolicyVersion: policyVersion, MaxCalls: maxCalls}}, nil
}
func CatalogTools(maxCalls uint32, grantVersion string) ([]toolcatalog.DiscoveredTool, map[string]toolcatalog.Grant, error) {
	if maxCalls == 0 || maxCalls > 1024 || !validIdentity(grantVersion) {
		return nil, nil, ErrSourceDenied
	}
	tools := []toolcatalog.DiscoveredTool{{ToolID: ToolID, ServerID: "fake-acquire", Name: "repo_acquire", Description: "Acquire a Host-pre-registered fake source as an immutable scoped workspace.", HandlerVersion: HandlerVersion, InputSchema: []byte(inputSchema), OutputSchema: []byte(outputSchema)}}
	grants := map[string]toolcatalog.Grant{ToolID: {ToolID: ToolID, EffectClass: "read_only", Policy: "AUTO_COMMIT", GrantVersion: grantVersion, MaxCalls: maxCalls}}
	return tools, grants, nil
}

func cloneSource(source Source) (Source, error) {
	copySource := source
	copySource.Files = make(map[string][]byte, len(source.Files))
	total := 0
	for name, content := range source.Files {
		if name == "" || len(content) == 0 {
			zeroFiles(copySource.Files)
			return Source{}, ErrSourceDenied
		}
		total += len(content)
		if total > maxSourceBytes {
			zeroFiles(copySource.Files)
			return Source{}, ErrSourceDenied
		}
		copySource.Files[name] = append([]byte(nil), content...)
	}
	return copySource, nil
}
func zeroFiles(files map[string][]byte) {
	for name, content := range files {
		zero(content)
		delete(files, name)
	}
}
func zero(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
func sameSecret(left, right []byte) bool {
	return len(left) == len(right) && subtle.ConstantTimeCompare(left, right) == 1
}
func validDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != 71 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}
func validRevision(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
func validIdentity(value string) bool {
	if value == "" || len(value) > 256 {
		return false
	}
	for _, r := range value {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("._:-/", r)) {
			return false
		}
	}
	return true
}
func classified(err error) error {
	switch {
	case errors.Is(err, capability.ErrSecretDenied), errors.Is(err, capability.ErrSecretExpired), errors.Is(err, capability.ErrSecretReleased), errors.Is(err, ErrCredentialDenied):
		return capability.NewHandlerFailure("credential_denied", "fake acquisition credential was denied", err)
	case errors.Is(err, fakeworkspace.ErrManifestDrift):
		return capability.NewHandlerFailure("source_drift", "fake acquisition source manifest changed", err)
	case errors.Is(err, fakeworkspace.ErrQuotaExceeded):
		return capability.NewHandlerFailure("quota_exceeded", "fake acquisition quota exceeded", err)
	case errors.Is(err, fakeworkspace.ErrWorkspaceDenied):
		return capability.NewHandlerFailure("workspace_denied", "fake acquisition workspace was denied", err)
	default:
		return capability.NewHandlerFailure("source_denied", "fake acquisition source was denied", err)
	}
}

const inputSchema = `{"type":"object","additionalProperties":false,"required":["source_alias"],"properties":{"source_alias":{"type":"string","minLength":1,"maxLength":256}}}`
const outputSchema = `{"type":"object","additionalProperties":false,"required":["source_alias","workspace_id","alias","resolved_revision","manifest_digest","file_count","total_bytes","expires_at"],"properties":{"source_alias":{"type":"string"},"workspace_id":{"type":"string"},"alias":{"type":"string"},"resolved_revision":{"type":"string"},"manifest_digest":{"type":"string","pattern":"^sha256:[0-9a-f]{64}$"},"file_count":{"type":"integer","minimum":1},"total_bytes":{"type":"integer","minimum":1},"expires_at":{"type":"string"}}}`

var _ capability.Handler = (*Adapter)(nil)

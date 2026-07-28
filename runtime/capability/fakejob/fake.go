package fakejob

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/toolcatalog"
)

const (
	SubmitToolID    = "job.submit"
	PollManyToolID  = "job.poll_many"
	LogsToolID      = "job.logs"
	ArtifactsToolID = "job.artifacts"
	HandlerVersion  = "fake-job-v1"
	maxJobs         = 4096
	maxLogsPerJob   = 1000
	maxLogBytes     = 1 << 20
	maxArtifacts    = 100
)

var (
	ErrCredentialDenied = errors.New("fake job credential denied")
	ErrJobDenied        = errors.New("fake job denied")
	ErrJobDrift         = errors.New("fake job version drift")
	ErrJobTerminal      = errors.New("fake job is terminal")
)

type Recipe struct {
	Alias  string `json:"alias"`
	Digest string `json:"digest"`
}

type Job struct {
	ID           string `json:"id"`
	RecipeAlias  string `json:"recipe_alias"`
	RecipeDigest string `json:"recipe_digest"`
	Status       string `json:"status"`
	Version      uint64 `json:"version"`
}

type LogLine struct {
	Sequence uint32 `json:"sequence"`
	Stream   string `json:"stream"`
	Text     string `json:"text"`
}

type Artifact struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Bytes  uint64 `json:"bytes"`
}

type jobState struct {
	job       Job
	operation string
	logs      []LogLine
	logBytes  int
	artifacts []Artifact
}

type Provider struct {
	mu                sync.Mutex
	readCredential    []byte
	controlCredential []byte
	recipes           map[string]Recipe
	jobs              map[string]*jobState
	operationJobs     map[string]string
	nextJob           uint64
	nextVersion       uint64
}

func NewProvider(recipes []Recipe, readCredential, controlCredential []byte) (*Provider, error) {
	if len(recipes) == 0 || len(recipes) > 1024 || len(readCredential) == 0 || len(controlCredential) == 0 {
		return nil, ErrJobDenied
	}
	provider := &Provider{readCredential: append([]byte(nil), readCredential...), controlCredential: append([]byte(nil), controlCredential...), recipes: map[string]Recipe{}, jobs: map[string]*jobState{}, operationJobs: map[string]string{}, nextVersion: 1}
	for _, recipe := range recipes {
		if !validIdentity(recipe.Alias) || !validDigest(recipe.Digest) {
			return nil, ErrJobDenied
		}
		if _, duplicate := provider.recipes[recipe.Alias]; duplicate {
			return nil, ErrJobDenied
		}
		provider.recipes[recipe.Alias] = recipe
	}
	return provider, nil
}

func (provider *Provider) Close() {
	if provider == nil {
		return
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	zero(provider.readCredential)
	zero(provider.controlCredential)
	provider.readCredential, provider.controlCredential = nil, nil
	provider.recipes, provider.jobs, provider.operationJobs = nil, nil, nil
}

func (provider *Provider) JobCount() int {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return len(provider.jobs)
}
func (provider *Provider) Snapshot(id string) Job {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if value := provider.jobs[id]; value != nil {
		return value.job
	}
	return Job{}
}

func (provider *Provider) Advance(id string, expectedVersion uint64, status string, logs []LogLine, artifacts []Artifact) error {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	state := provider.jobs[id]
	if state == nil || state.job.Version != expectedVersion {
		return ErrJobDrift
	}
	if !validTransition(state.job.Status, status) {
		return ErrJobTerminal
	}
	if len(state.logs)+len(logs) > maxLogsPerJob || len(state.artifacts)+len(artifacts) > maxArtifacts {
		return ErrJobDenied
	}
	newLogBytes := state.logBytes
	for _, line := range logs {
		if (line.Stream != "stdout" && line.Stream != "stderr") || line.Text == "" || len(line.Text) > 4096 || strings.ContainsRune(line.Text, 0) || line.Sequence != 0 {
			return ErrJobDenied
		}
		newLogBytes += len(line.Text)
	}
	if newLogBytes > maxLogBytes {
		return ErrJobDenied
	}
	seen := map[string]struct{}{}
	for _, prior := range state.artifacts {
		seen[prior.Name] = struct{}{}
	}
	for _, artifact := range artifacts {
		if !validArtifact(artifact) {
			return ErrJobDenied
		}
		if _, duplicate := seen[artifact.Name]; duplicate {
			return ErrJobDenied
		}
		seen[artifact.Name] = struct{}{}
	}
	for _, line := range logs {
		line.Sequence = uint32(len(state.logs) + 1)
		state.logs = append(state.logs, line)
	}
	state.logBytes = newLogBytes
	state.artifacts = append(state.artifacts, artifacts...)
	state.job.Status = status
	state.job.Version = provider.allocateVersion()
	return nil
}

func validTransition(before, after string) bool {
	switch before {
	case "queued":
		return after == "running" || after == "succeeded" || after == "failed"
	case "running":
		return after == "succeeded" || after == "failed"
	default:
		return false
	}
}

type Config struct {
	Resolver         capability.SecretResolver
	ReadSecretRef    capability.SecretRef
	ControlSecretRef capability.SecretRef
	RunIdentity      string
	TaskIdentity     string
	Tenant           string
	QueueAlias       string
	PolicyVersion    string
	LeaseDuration    time.Duration
	Provider         *Provider
}

type Adapter struct {
	config        Config
	mu            sync.Mutex
	operationJobs map[string]string
}

func NewAdapter(config Config) (*Adapter, error) {
	if config.Resolver == nil || config.Provider == nil || config.ReadSecretRef == "" || config.ControlSecretRef == "" || !validIdentity(config.RunIdentity) || !validIdentity(config.TaskIdentity) || !validIdentity(config.Tenant) || !validIdentity(config.QueueAlias) || !validIdentity(config.PolicyVersion) || config.LeaseDuration <= 0 || config.LeaseDuration > time.Hour {
		return nil, ErrJobDenied
	}
	return &Adapter{config: config, operationJobs: map[string]string{}}, nil
}

func HandlerSpecs(adapter *Adapter) ([]capability.HandlerSpec, error) {
	if adapter == nil {
		return nil, ErrJobDenied
	}
	return []capability.HandlerSpec{
		{ToolID: SubmitToolID, HandlerVersion: HandlerVersion, InputSchema: []byte(submitInputSchema), OutputSchema: []byte(jobSchema), Handler: adapter},
		{ToolID: PollManyToolID, HandlerVersion: HandlerVersion, InputSchema: []byte(pollInputSchema), OutputSchema: []byte(pollOutputSchema), Handler: adapter},
		{ToolID: LogsToolID, HandlerVersion: HandlerVersion, InputSchema: []byte(logsInputSchema), OutputSchema: []byte(logsOutputSchema), Handler: adapter},
		{ToolID: ArtifactsToolID, HandlerVersion: HandlerVersion, InputSchema: []byte(artifactsInputSchema), OutputSchema: []byte(artifactsOutputSchema), Handler: adapter},
	}, nil
}

func ToolGrants(policyVersion string, maxCalls uint32) (map[string]capability.ToolGrant, error) {
	if !validIdentity(policyVersion) || maxCalls == 0 {
		return nil, ErrJobDenied
	}
	return map[string]capability.ToolGrant{
		SubmitToolID:    {ToolID: SubmitToolID, HandlerVersion: HandlerVersion, EffectClass: "compensatable", Policy: "AUTO_COMMIT", PolicyVersion: policyVersion, MaxCalls: maxCalls},
		PollManyToolID:  {ToolID: PollManyToolID, HandlerVersion: HandlerVersion, EffectClass: "read_only", Policy: "AUTO_COMMIT", PolicyVersion: policyVersion, MaxCalls: maxCalls},
		LogsToolID:      {ToolID: LogsToolID, HandlerVersion: HandlerVersion, EffectClass: "read_only", Policy: "AUTO_COMMIT", PolicyVersion: policyVersion, MaxCalls: maxCalls},
		ArtifactsToolID: {ToolID: ArtifactsToolID, HandlerVersion: HandlerVersion, EffectClass: "read_only", Policy: "AUTO_COMMIT", PolicyVersion: policyVersion, MaxCalls: maxCalls},
	}, nil
}

func CatalogTools(maxCalls uint32, grantVersion string) ([]toolcatalog.DiscoveredTool, map[string]toolcatalog.Grant, error) {
	if maxCalls == 0 || maxCalls > 1024 || !validIdentity(grantVersion) {
		return nil, nil, ErrJobDenied
	}
	tools := []toolcatalog.DiscoveredTool{
		{ToolID: SubmitToolID, ServerID: "fake-job", Name: "job_submit", Description: "Submit a Host-pre-registered fake job recipe without command authority.", HandlerVersion: HandlerVersion, InputSchema: []byte(submitInputSchema), OutputSchema: []byte(jobSchema)},
		{ToolID: PollManyToolID, ServerID: "fake-job", Name: "job_poll_many", Description: "Poll bounded fake job identities in request order.", HandlerVersion: HandlerVersion, InputSchema: []byte(pollInputSchema), OutputSchema: []byte(pollOutputSchema)},
		{ToolID: LogsToolID, ServerID: "fake-job", Name: "job_logs", Description: "Page through bounded fake job logs.", HandlerVersion: HandlerVersion, InputSchema: []byte(logsInputSchema), OutputSchema: []byte(logsOutputSchema)},
		{ToolID: ArtifactsToolID, ServerID: "fake-job", Name: "job_artifacts", Description: "List bounded fake job artifact metadata without file access.", HandlerVersion: HandlerVersion, InputSchema: []byte(artifactsInputSchema), OutputSchema: []byte(artifactsOutputSchema)},
	}
	grants := map[string]toolcatalog.Grant{}
	for _, tool := range tools {
		effect := "read_only"
		if tool.ToolID == SubmitToolID {
			effect = "compensatable"
		}
		grants[tool.ToolID] = toolcatalog.Grant{ToolID: tool.ToolID, EffectClass: effect, Policy: "AUTO_COMMIT", GrantVersion: grantVersion, MaxCalls: maxCalls}
	}
	return tools, grants, nil
}

type submitArguments struct {
	RecipeAlias string `json:"recipe_alias"`
}
type pollArguments struct {
	JobIDs []string `json:"job_ids"`
}
type logsArguments struct {
	JobID  string `json:"job_id"`
	Cursor uint32 `json:"cursor"`
	Limit  uint32 `json:"limit"`
}
type artifactArguments struct {
	JobID string `json:"job_id"`
}

func (adapter *Adapter) Handle(ctx context.Context, call capability.HostCall) (json.RawMessage, error) {
	if call.RunIdentity != adapter.config.RunIdentity {
		return nil, classified(ErrJobDenied)
	}
	switch call.ToolID {
	case SubmitToolID:
		var args submitArguments
		if json.Unmarshal(call.Arguments, &args) != nil {
			return nil, classified(ErrJobDenied)
		}
		adapter.mu.Lock()
		if _, exists := adapter.operationJobs[call.OperationID]; exists || len(adapter.operationJobs) >= maxJobs {
			adapter.mu.Unlock()
			return nil, classified(ErrJobDenied)
		}
		adapter.operationJobs[call.OperationID] = ""
		adapter.mu.Unlock()
		var job Job
		err := adapter.withSecret(ctx, adapter.config.ControlSecretRef, call.ToolID, "submit", func(secret []byte) error {
			var e error
			job, e = adapter.config.Provider.submit(secret, call.OperationID, args.RecipeAlias)
			return e
		})
		if err != nil {
			adapter.mu.Lock()
			delete(adapter.operationJobs, call.OperationID)
			adapter.mu.Unlock()
			return nil, classified(err)
		}
		adapter.mu.Lock()
		adapter.operationJobs[call.OperationID] = job.ID
		adapter.mu.Unlock()
		return json.Marshal(job)
	case PollManyToolID:
		var args pollArguments
		if json.Unmarshal(call.Arguments, &args) != nil {
			return nil, classified(ErrJobDenied)
		}
		var jobs []Job
		err := adapter.withSecret(ctx, adapter.config.ReadSecretRef, call.ToolID, "read", func(secret []byte) error {
			var e error
			jobs, e = adapter.config.Provider.poll(secret, args.JobIDs)
			return e
		})
		if err != nil {
			return nil, classified(err)
		}
		return json.Marshal(struct {
			Jobs []Job `json:"jobs"`
		}{jobs})
	case LogsToolID:
		var args logsArguments
		if json.Unmarshal(call.Arguments, &args) != nil {
			return nil, classified(ErrJobDenied)
		}
		var lines []LogLine
		var next *uint32
		err := adapter.withSecret(ctx, adapter.config.ReadSecretRef, call.ToolID, "read", func(secret []byte) error {
			var e error
			lines, next, e = adapter.config.Provider.logsPage(secret, args)
			return e
		})
		if err != nil {
			return nil, classified(err)
		}
		return json.Marshal(struct {
			Lines      []LogLine `json:"lines"`
			NextCursor *uint32   `json:"next_cursor"`
		}{lines, next})
	case ArtifactsToolID:
		var args artifactArguments
		if json.Unmarshal(call.Arguments, &args) != nil {
			return nil, classified(ErrJobDenied)
		}
		var artifacts []Artifact
		err := adapter.withSecret(ctx, adapter.config.ReadSecretRef, call.ToolID, "read", func(secret []byte) error {
			var e error
			artifacts, e = adapter.config.Provider.artifactsFor(secret, args.JobID)
			return e
		})
		if err != nil {
			return nil, classified(err)
		}
		return json.Marshal(struct {
			Artifacts []Artifact `json:"artifacts"`
		}{artifacts})
	default:
		return nil, classified(ErrJobDenied)
	}
}

func (adapter *Adapter) Rollback(context.Context, capability.AbortCall) error {
	return errors.New("fake job submit is compensatable, not reversible")
}
func (adapter *Adapter) Compensate(ctx context.Context, call capability.AbortCall) error {
	adapter.mu.Lock()
	jobID, exists := adapter.operationJobs[call.Operation.ID]
	adapter.mu.Unlock()
	if !exists {
		return ErrJobDenied
	}
	return adapter.withSecret(ctx, adapter.config.ControlSecretRef, call.Operation.ToolID, "compensate", func(secret []byte) error { return adapter.config.Provider.cancelQueued(secret, jobID) })
}
func (adapter *Adapter) withSecret(ctx context.Context, ref capability.SecretRef, tool, operation string, use func([]byte) error) error {
	lease, err := adapter.config.Resolver.Resolve(ctx, capability.SecretRequest{Reference: ref, RunIdentity: adapter.config.RunIdentity, TaskIdentity: adapter.config.TaskIdentity, Tenant: adapter.config.Tenant, ToolID: tool, Operation: operation, ResourceAlias: adapter.config.QueueAlias, PolicyVersion: adapter.config.PolicyVersion, RequestedLease: adapter.config.LeaseDuration})
	if err != nil {
		return err
	}
	defer lease.Release()
	return lease.WithValue(use)
}

func (provider *Provider) submit(credential []byte, operation, alias string) (Job, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if !sameSecret(credential, provider.controlCredential) {
		return Job{}, ErrCredentialDenied
	}
	recipe, exists := provider.recipes[alias]
	if !exists || !validIdentity(operation) {
		return Job{}, ErrJobDenied
	}
	if prior := provider.operationJobs[operation]; prior != "" {
		return provider.jobs[prior].job, nil
	}
	if len(provider.jobs) >= maxJobs {
		return Job{}, ErrJobDenied
	}
	provider.nextJob++
	id := fmt.Sprintf("job:%d", provider.nextJob)
	job := Job{ID: id, RecipeAlias: recipe.Alias, RecipeDigest: recipe.Digest, Status: "queued", Version: provider.allocateVersion()}
	provider.jobs[id] = &jobState{job: job, operation: operation, logs: []LogLine{}, artifacts: []Artifact{}}
	provider.operationJobs[operation] = id
	return job, nil
}
func (provider *Provider) poll(credential []byte, ids []string) ([]Job, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if !sameSecret(credential, provider.readCredential) {
		return nil, ErrCredentialDenied
	}
	if len(ids) == 0 || len(ids) > 32 {
		return nil, ErrJobDenied
	}
	result := make([]Job, 0, len(ids))
	seen := map[string]struct{}{}
	for _, id := range ids {
		state := provider.jobs[id]
		if state == nil {
			return nil, ErrJobDenied
		}
		if _, dup := seen[id]; dup {
			return nil, ErrJobDenied
		}
		seen[id] = struct{}{}
		result = append(result, state.job)
	}
	return result, nil
}
func (provider *Provider) logsPage(credential []byte, args logsArguments) ([]LogLine, *uint32, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if !sameSecret(credential, provider.readCredential) {
		return nil, nil, ErrCredentialDenied
	}
	state := provider.jobs[args.JobID]
	if state == nil || args.Limit == 0 || args.Limit > 100 || uint64(args.Cursor) > uint64(len(state.logs)) {
		return nil, nil, ErrJobDenied
	}
	start := int(args.Cursor)
	end := start + int(args.Limit)
	if end > len(state.logs) {
		end = len(state.logs)
	}
	lines := append([]LogLine(nil), state.logs[start:end]...)
	var next *uint32
	if end < len(state.logs) {
		v := uint32(end)
		next = &v
	}
	return lines, next, nil
}
func (provider *Provider) artifactsFor(credential []byte, id string) ([]Artifact, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if !sameSecret(credential, provider.readCredential) {
		return nil, ErrCredentialDenied
	}
	state := provider.jobs[id]
	if state == nil {
		return nil, ErrJobDenied
	}
	result := append([]Artifact(nil), state.artifacts...)
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}
func (provider *Provider) cancelQueued(credential []byte, id string) error {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if !sameSecret(credential, provider.controlCredential) {
		return ErrCredentialDenied
	}
	state := provider.jobs[id]
	if state == nil {
		return ErrJobDenied
	}
	if state.job.Status != "queued" && state.job.Status != "running" {
		return ErrJobTerminal
	}
	state.job.Status = "canceled"
	state.job.Version = provider.allocateVersion()
	return nil
}
func (provider *Provider) allocateVersion() uint64 {
	v := provider.nextVersion
	provider.nextVersion++
	return v
}

func validArtifact(value Artifact) bool {
	return validIdentity(value.Name) && validDigest(value.SHA256) && value.Bytes > 0
}
func validDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != 71 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
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
func sameSecret(a, b []byte) bool { return len(a) == len(b) && subtle.ConstantTimeCompare(a, b) == 1 }
func zero(v []byte) {
	for i := range v {
		v[i] = 0
	}
}

func classified(err error) error {
	switch {
	case errors.Is(err, capability.ErrSecretDenied), errors.Is(err, capability.ErrSecretExpired), errors.Is(err, capability.ErrSecretReleased), errors.Is(err, ErrCredentialDenied):
		return capability.NewHandlerFailure("credential_denied", "fake job credential was denied", err)
	case errors.Is(err, ErrJobDrift):
		return capability.NewHandlerFailure("version_drift", "fake job changed", err)
	default:
		return capability.NewHandlerFailure("job_denied", "fake job operation was denied", err)
	}
}

const jobSchema = `{"type":"object","additionalProperties":false,"required":["id","recipe_alias","recipe_digest","status","version"],"properties":{"id":{"type":"string"},"recipe_alias":{"type":"string"},"recipe_digest":{"type":"string","pattern":"^sha256:[0-9a-f]{64}$"},"status":{"type":"string","enum":["queued","running","succeeded","failed","canceled"]},"version":{"type":"integer","minimum":1}}}`
const submitInputSchema = `{"type":"object","additionalProperties":false,"required":["recipe_alias"],"properties":{"recipe_alias":{"type":"string","minLength":1,"maxLength":256}}}`
const pollInputSchema = `{"type":"object","additionalProperties":false,"required":["job_ids"],"properties":{"job_ids":{"type":"array","minItems":1,"maxItems":32,"items":{"type":"string"}}}}`
const pollOutputSchema = `{"type":"object","additionalProperties":false,"required":["jobs"],"properties":{"jobs":{"type":"array","maxItems":32,"items":` + jobSchema + `}}}`
const logsInputSchema = `{"type":"object","additionalProperties":false,"required":["job_id","cursor","limit"],"properties":{"job_id":{"type":"string"},"cursor":{"type":"integer","minimum":0},"limit":{"type":"integer","minimum":1,"maximum":100}}}`
const nullableCursorSchema = `{"anyOf":[{"type":"integer","minimum":1},{"type":"null"}]}`
const logLineSchema = `{"type":"object","additionalProperties":false,"required":["sequence","stream","text"],"properties":{"sequence":{"type":"integer","minimum":1},"stream":{"type":"string","enum":["stdout","stderr"]},"text":{"type":"string"}}}`
const logsOutputSchema = `{"type":"object","additionalProperties":false,"required":["lines","next_cursor"],"properties":{"lines":{"type":"array","maxItems":100,"items":` + logLineSchema + `},"next_cursor":` + nullableCursorSchema + `}}`
const artifactsInputSchema = `{"type":"object","additionalProperties":false,"required":["job_id"],"properties":{"job_id":{"type":"string"}}}`
const artifactSchema = `{"type":"object","additionalProperties":false,"required":["name","sha256","bytes"],"properties":{"name":{"type":"string"},"sha256":{"type":"string","pattern":"^sha256:[0-9a-f]{64}$"},"bytes":{"type":"integer","minimum":1}}}`
const artifactsOutputSchema = `{"type":"object","additionalProperties":false,"required":["artifacts"],"properties":{"artifacts":{"type":"array","maxItems":100,"items":` + artifactSchema + `}}}`

var _ capability.Handler = (*Adapter)(nil)
var _ capability.AbortHandler = (*Adapter)(nil)

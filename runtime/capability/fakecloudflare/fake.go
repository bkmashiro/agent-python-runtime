package fakecloudflare

import (
	"context"
	"crypto/sha256"
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
	DNSListToolID   = "cloudflare.dns.list"
	DNSPlanToolID   = "cloudflare.dns.plan_change"
	DNSApplyToolID  = "cloudflare.dns.apply_change"
	HandlerVersion  = "fake-cloudflare-dns-v1"
	maxTrackedPlans = 4096
)

var (
	ErrCredentialDenied = errors.New("fake Cloudflare credential denied")
	ErrPlanDenied       = errors.New("fake Cloudflare DNS plan denied")
	ErrVersionDrift     = errors.New("fake Cloudflare DNS version drift")
	ErrUnknownOperation = errors.New("fake Cloudflare operation is unknown")
	ErrAmbiguousCommit  = errors.New("fake Cloudflare committed before response loss")
)

type Record struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content string `json:"content"`
	TTL     uint32 `json:"ttl"`
	Version uint64 `json:"version"`
}

type Provider struct {
	mu              sync.Mutex
	readCredential  []byte
	writeCredential []byte
	records         map[string]Record
	nextRecord      uint64
	nextVersion     uint64
	ambiguousNext   bool
}

func NewProvider(records []Record, readCredential, writeCredential []byte) (*Provider, error) {
	if len(readCredential) == 0 || len(writeCredential) == 0 || len(records) > 10000 {
		return nil, ErrCredentialDenied
	}
	provider := &Provider{readCredential: append([]byte(nil), readCredential...), writeCredential: append([]byte(nil), writeCredential...), records: map[string]Record{}, nextVersion: 1}
	for _, record := range records {
		if !validRecord(record, true) {
			return nil, ErrPlanDenied
		}
		if _, exists := provider.records[record.ID]; exists {
			return nil, ErrPlanDenied
		}
		provider.records[record.ID] = record
		if record.Version >= provider.nextVersion {
			provider.nextVersion = record.Version + 1
		}
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
	zero(provider.writeCredential)
	provider.readCredential = nil
	provider.writeCredential = nil
	provider.records = nil
}

func (provider *Provider) SetAmbiguousNext() {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.ambiguousNext = true
}

func (provider *Provider) Drift(recordID, content string) error {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	record, exists := provider.records[recordID]
	if !exists || content == "" {
		return ErrUnknownOperation
	}
	record.Content = content
	record.Version = provider.allocateVersion()
	provider.records[recordID] = record
	return nil
}

func (provider *Provider) Snapshot() []Record {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return sortedRecords(provider.records)
}

type Config struct {
	Resolver       capability.SecretResolver
	ReadSecretRef  capability.SecretRef
	WriteSecretRef capability.SecretRef
	RunIdentity    string
	TaskIdentity   string
	Tenant         string
	ZoneAlias      string
	PolicyVersion  string
	LeaseDuration  time.Duration
	Provider       *Provider
}

type Adapter struct {
	config Config
	mu     sync.Mutex
	plans  map[string]Plan
	undo   map[string]undoRecord
}

type Plan struct {
	Digest          string  `json:"digest"`
	Action          string  `json:"action"`
	RecordID        string  `json:"record_id,omitempty"`
	ExpectedVersion uint64  `json:"expected_version"`
	Before          *Record `json:"before"`
	After           *Record `json:"after"`
}

type undoRecord struct {
	action string
	before *Record
	after  *Record
}

func NewAdapter(config Config) (*Adapter, error) {
	if config.Resolver == nil || config.Provider == nil || config.ReadSecretRef == "" || config.WriteSecretRef == "" ||
		!validIdentity(config.RunIdentity) || !validIdentity(config.TaskIdentity) || !validIdentity(config.Tenant) ||
		!validIdentity(config.ZoneAlias) || !validIdentity(config.PolicyVersion) || config.LeaseDuration <= 0 || config.LeaseDuration > time.Hour {
		return nil, errors.New("invalid fake Cloudflare adapter configuration")
	}
	return &Adapter{config: config, plans: map[string]Plan{}, undo: map[string]undoRecord{}}, nil
}

func HandlerSpecs(adapter *Adapter) ([]capability.HandlerSpec, error) {
	if adapter == nil {
		return nil, errors.New("fake Cloudflare adapter is nil")
	}
	return []capability.HandlerSpec{
		{ToolID: DNSListToolID, HandlerVersion: HandlerVersion, InputSchema: []byte(emptyInputSchema), OutputSchema: []byte(listOutputSchema), Handler: capability.HandlerFunc(adapter.handleList)},
		{ToolID: DNSPlanToolID, HandlerVersion: HandlerVersion, InputSchema: []byte(planInputSchema), OutputSchema: []byte(planOutputSchema), Handler: capability.HandlerFunc(adapter.handlePlan)},
		{ToolID: DNSApplyToolID, HandlerVersion: HandlerVersion, InputSchema: []byte(applyInputSchema), OutputSchema: []byte(applyOutputSchema), Handler: adapter},
	}, nil
}

func ToolGrants(policyVersion string, maxCalls uint32) (map[string]capability.ToolGrant, error) {
	if !validIdentity(policyVersion) || maxCalls == 0 {
		return nil, errors.New("invalid fake Cloudflare grants")
	}
	return map[string]capability.ToolGrant{
		DNSListToolID:  {ToolID: DNSListToolID, HandlerVersion: HandlerVersion, EffectClass: "read_only", Policy: "AUTO_COMMIT", PolicyVersion: policyVersion, MaxCalls: maxCalls},
		DNSPlanToolID:  {ToolID: DNSPlanToolID, HandlerVersion: HandlerVersion, EffectClass: "read_only", Policy: "AUTO_COMMIT", PolicyVersion: policyVersion, MaxCalls: maxCalls},
		DNSApplyToolID: {ToolID: DNSApplyToolID, HandlerVersion: HandlerVersion, EffectClass: "reversible", Policy: "AUTO_COMMIT", PolicyVersion: policyVersion, MaxCalls: maxCalls},
	}, nil
}

func CatalogTools(maxCalls uint32, grantVersion string) ([]toolcatalog.DiscoveredTool, map[string]toolcatalog.Grant, error) {
	if maxCalls == 0 || maxCalls > 1024 || !validIdentity(grantVersion) {
		return nil, nil, errors.New("invalid fake Cloudflare catalog grants")
	}
	tools := []toolcatalog.DiscoveredTool{
		{ToolID: DNSListToolID, ServerID: "fake-cloudflare", Name: "cloudflare_dns_list", Description: "List bounded DNS records from the Host-bound fake Cloudflare zone.", HandlerVersion: HandlerVersion, InputSchema: []byte(emptyInputSchema), OutputSchema: []byte(listOutputSchema)},
		{ToolID: DNSPlanToolID, ServerID: "fake-cloudflare", Name: "cloudflare_dns_plan_change", Description: "Prepare a digest-bound fake DNS create, update, or delete plan without mutation.", HandlerVersion: HandlerVersion, InputSchema: []byte(planInputSchema), OutputSchema: []byte(planOutputSchema)},
		{ToolID: DNSApplyToolID, ServerID: "fake-cloudflare", Name: "cloudflare_dns_apply_change", Description: "Apply an approved fake DNS plan with conditional Host-only rollback.", HandlerVersion: HandlerVersion, InputSchema: []byte(applyInputSchema), OutputSchema: []byte(applyOutputSchema)},
	}
	grants := map[string]toolcatalog.Grant{
		DNSListToolID:  {ToolID: DNSListToolID, EffectClass: "read_only", Policy: "AUTO_COMMIT", GrantVersion: grantVersion, MaxCalls: maxCalls},
		DNSPlanToolID:  {ToolID: DNSPlanToolID, EffectClass: "read_only", Policy: "AUTO_COMMIT", GrantVersion: grantVersion, MaxCalls: maxCalls},
		DNSApplyToolID: {ToolID: DNSApplyToolID, EffectClass: "reversible", Policy: "AUTO_COMMIT", GrantVersion: grantVersion, MaxCalls: maxCalls},
	}
	return tools, grants, nil
}

func (adapter *Adapter) handleList(ctx context.Context, call capability.HostCall) (json.RawMessage, error) {
	if call.RunIdentity != adapter.config.RunIdentity {
		return nil, classified(ErrCredentialDenied)
	}
	var records []Record
	err := adapter.withSecret(ctx, adapter.config.ReadSecretRef, call.ToolID, "read", func(value []byte) error {
		var listErr error
		records, listErr = adapter.config.Provider.list(value)
		return listErr
	})
	if err != nil {
		return nil, classified(err)
	}
	return json.Marshal(struct {
		ZoneAlias string   `json:"zone_alias"`
		Records   []Record `json:"records"`
	}{ZoneAlias: adapter.config.ZoneAlias, Records: records})
}

type planArguments struct {
	Action   string `json:"action"`
	RecordID string `json:"record_id,omitempty"`
	Name     string `json:"name,omitempty"`
	Type     string `json:"type,omitempty"`
	Content  string `json:"content,omitempty"`
	TTL      uint32 `json:"ttl,omitempty"`
}

func (adapter *Adapter) handlePlan(ctx context.Context, call capability.HostCall) (json.RawMessage, error) {
	if call.RunIdentity != adapter.config.RunIdentity {
		return nil, classified(ErrCredentialDenied)
	}
	var arguments planArguments
	if err := json.Unmarshal(call.Arguments, &arguments); err != nil {
		return nil, classified(ErrPlanDenied)
	}
	var records []Record
	err := adapter.withSecret(ctx, adapter.config.ReadSecretRef, call.ToolID, "read", func(value []byte) error {
		var listErr error
		records, listErr = adapter.config.Provider.list(value)
		return listErr
	})
	if err != nil {
		return nil, classified(err)
	}
	plan, err := buildPlan(arguments, records)
	if err != nil {
		return nil, classified(err)
	}
	adapter.mu.Lock()
	if _, exists := adapter.plans[plan.Digest]; !exists && len(adapter.plans) >= maxTrackedPlans {
		adapter.mu.Unlock()
		return nil, classified(ErrPlanDenied)
	}
	adapter.plans[plan.Digest] = clonePlan(plan)
	adapter.mu.Unlock()
	return json.Marshal(plan)
}

type applyArguments struct {
	PlanDigest string `json:"plan_digest"`
}

type applyResult struct {
	PlanDigest string  `json:"plan_digest"`
	Action     string  `json:"action"`
	Record     *Record `json:"record"`
}

func (adapter *Adapter) Handle(ctx context.Context, call capability.HostCall) (json.RawMessage, error) {
	if call.RunIdentity != adapter.config.RunIdentity {
		return nil, classified(ErrCredentialDenied)
	}
	var arguments applyArguments
	if err := json.Unmarshal(call.Arguments, &arguments); err != nil || !validDigest(arguments.PlanDigest) {
		return nil, classified(ErrPlanDenied)
	}
	adapter.mu.Lock()
	plan, exists := adapter.plans[arguments.PlanDigest]
	adapter.mu.Unlock()
	if !exists {
		return nil, classified(ErrPlanDenied)
	}
	adapter.mu.Lock()
	if _, exists := adapter.undo[call.OperationID]; exists || len(adapter.undo) >= maxTrackedPlans {
		adapter.mu.Unlock()
		return nil, classified(ErrPlanDenied)
	}
	adapter.undo[call.OperationID] = undoRecord{}
	adapter.mu.Unlock()
	var undo undoRecord
	var applied *Record
	var ambiguous bool
	err := adapter.withSecret(ctx, adapter.config.WriteSecretRef, call.ToolID, "apply", func(value []byte) error {
		var applyErr error
		undo, applied, ambiguous, applyErr = adapter.config.Provider.apply(value, plan)
		return applyErr
	})
	if err != nil {
		adapter.mu.Lock()
		delete(adapter.undo, call.OperationID)
		adapter.mu.Unlock()
		return nil, classified(err)
	}
	adapter.mu.Lock()
	adapter.undo[call.OperationID] = undo
	adapter.mu.Unlock()
	if ambiguous {
		return nil, capability.NewAmbiguousHandlerFailure("provider_ambiguous", "fake Cloudflare outcome requires reconciliation", ErrAmbiguousCommit)
	}
	return json.Marshal(applyResult{PlanDigest: plan.Digest, Action: plan.Action, Record: applied})
}

func (adapter *Adapter) Rollback(ctx context.Context, call capability.AbortCall) error {
	adapter.mu.Lock()
	undo, exists := adapter.undo[call.Operation.ID]
	adapter.mu.Unlock()
	if !exists {
		return ErrUnknownOperation
	}
	return adapter.withSecret(ctx, adapter.config.WriteSecretRef, call.Operation.ToolID, "rollback", func(value []byte) error {
		return adapter.config.Provider.rollback(value, undo)
	})
}

func (adapter *Adapter) Compensate(context.Context, capability.AbortCall) error {
	return errors.New("fake Cloudflare DNS operations are not compensatable")
}

func (adapter *Adapter) withSecret(ctx context.Context, reference capability.SecretRef, toolID, operation string, use func([]byte) error) error {
	lease, err := adapter.config.Resolver.Resolve(ctx, capability.SecretRequest{Reference: reference, RunIdentity: adapter.config.RunIdentity, TaskIdentity: adapter.config.TaskIdentity, Tenant: adapter.config.Tenant, ToolID: toolID, Operation: operation, ResourceAlias: adapter.config.ZoneAlias, PolicyVersion: adapter.config.PolicyVersion, RequestedLease: adapter.config.LeaseDuration})
	if err != nil {
		return err
	}
	defer lease.Release()
	return lease.WithValue(use)
}

func (provider *Provider) list(credential []byte) ([]Record, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if !sameSecret(credential, provider.readCredential) {
		return nil, ErrCredentialDenied
	}
	return sortedRecords(provider.records), nil
}

func (provider *Provider) apply(credential []byte, plan Plan) (undoRecord, *Record, bool, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if !sameSecret(credential, provider.writeCredential) {
		return undoRecord{}, nil, false, ErrCredentialDenied
	}
	var undo undoRecord
	var applied *Record
	switch plan.Action {
	case "create":
		if plan.Before != nil || plan.After == nil || plan.ExpectedVersion != 0 {
			return undo, nil, false, ErrPlanDenied
		}
		for _, existing := range provider.records {
			if existing.Name == plan.After.Name && existing.Type == plan.After.Type {
				return undo, nil, false, ErrVersionDrift
			}
		}
		var recordID string
		for {
			provider.nextRecord++
			recordID = fmt.Sprintf("record:%d", provider.nextRecord)
			if _, exists := provider.records[recordID]; !exists {
				break
			}
		}
		record := *plan.After
		record.ID = recordID
		record.Version = provider.allocateVersion()
		provider.records[record.ID] = record
		applied = cloneRecord(&record)
		undo = undoRecord{action: plan.Action, after: cloneRecord(&record)}
	case "update":
		current, exists := provider.records[plan.RecordID]
		if !exists || plan.Before == nil || plan.After == nil || current != *plan.Before || current.Version != plan.ExpectedVersion {
			return undo, nil, false, ErrVersionDrift
		}
		record := *plan.After
		record.ID = current.ID
		record.Version = provider.allocateVersion()
		provider.records[record.ID] = record
		applied = cloneRecord(&record)
		undo = undoRecord{action: plan.Action, before: cloneRecord(&current), after: cloneRecord(&record)}
	case "delete":
		current, exists := provider.records[plan.RecordID]
		if !exists || plan.Before == nil || plan.After != nil || current != *plan.Before || current.Version != plan.ExpectedVersion {
			return undo, nil, false, ErrVersionDrift
		}
		delete(provider.records, current.ID)
		undo = undoRecord{action: plan.Action, before: cloneRecord(&current)}
	default:
		return undo, nil, false, ErrPlanDenied
	}
	ambiguous := provider.ambiguousNext
	provider.ambiguousNext = false
	return undo, applied, ambiguous, nil
}

func (provider *Provider) rollback(credential []byte, undo undoRecord) error {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if !sameSecret(credential, provider.writeCredential) {
		return ErrCredentialDenied
	}
	switch undo.action {
	case "create":
		current, exists := provider.records[undo.after.ID]
		if !exists || current != *undo.after {
			return ErrVersionDrift
		}
		delete(provider.records, current.ID)
	case "update":
		current, exists := provider.records[undo.after.ID]
		if !exists || current != *undo.after {
			return ErrVersionDrift
		}
		restored := *undo.before
		restored.Version = provider.allocateVersion()
		provider.records[restored.ID] = restored
	case "delete":
		if _, exists := provider.records[undo.before.ID]; exists {
			return ErrVersionDrift
		}
		restored := *undo.before
		restored.Version = provider.allocateVersion()
		provider.records[restored.ID] = restored
	default:
		return ErrUnknownOperation
	}
	return nil
}

func (provider *Provider) allocateVersion() uint64 {
	version := provider.nextVersion
	provider.nextVersion++
	return version
}

func buildPlan(arguments planArguments, records []Record) (Plan, error) {
	byID := make(map[string]Record, len(records))
	for _, record := range records {
		byID[record.ID] = record
	}
	plan := Plan{Action: arguments.Action, RecordID: arguments.RecordID}
	switch arguments.Action {
	case "create":
		candidate := Record{Name: arguments.Name, Type: strings.ToUpper(arguments.Type), Content: arguments.Content, TTL: arguments.TTL}
		if arguments.RecordID != "" || !validRecord(candidate, false) {
			return Plan{}, ErrPlanDenied
		}
		plan.After = &candidate
	case "update":
		before, exists := byID[arguments.RecordID]
		candidate := Record{ID: arguments.RecordID, Name: arguments.Name, Type: strings.ToUpper(arguments.Type), Content: arguments.Content, TTL: arguments.TTL}
		if !exists || !validIdentity(candidate.ID) || !validRecordData(candidate) {
			return Plan{}, ErrPlanDenied
		}
		plan.Before = cloneRecord(&before)
		plan.ExpectedVersion = before.Version
		plan.After = &candidate
	case "delete":
		before, exists := byID[arguments.RecordID]
		if !exists || arguments.Name != "" || arguments.Type != "" || arguments.Content != "" || arguments.TTL != 0 {
			return Plan{}, ErrPlanDenied
		}
		plan.Before = cloneRecord(&before)
		plan.ExpectedVersion = before.Version
	default:
		return Plan{}, ErrPlanDenied
	}
	canonical, err := json.Marshal(struct {
		Action          string  `json:"action"`
		RecordID        string  `json:"record_id,omitempty"`
		ExpectedVersion uint64  `json:"expected_version"`
		Before          *Record `json:"before"`
		After           *Record `json:"after"`
	}{Action: plan.Action, RecordID: plan.RecordID, ExpectedVersion: plan.ExpectedVersion, Before: plan.Before, After: plan.After})
	if err != nil {
		return Plan{}, err
	}
	plan.Digest = digest(canonical)
	return plan, nil
}

func sortedRecords(source map[string]Record) []Record {
	records := make([]Record, 0, len(source))
	for _, record := range source {
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
	return records
}

func cloneRecord(record *Record) *Record {
	if record == nil {
		return nil
	}
	cloned := *record
	return &cloned
}

func clonePlan(plan Plan) Plan {
	plan.Before = cloneRecord(plan.Before)
	plan.After = cloneRecord(plan.After)
	return plan
}

func validRecord(record Record, requireID bool) bool {
	if requireID && (!validIdentity(record.ID) || record.Version == 0) {
		return false
	}
	if !requireID && (record.ID != "" || record.Version != 0) {
		return false
	}
	return validRecordData(record)
}

func validRecordData(record Record) bool {
	if record.Name == "" || len(record.Name) > 253 || record.Content == "" || len(record.Content) > 4096 || record.TTL > 86400 {
		return false
	}
	switch record.Type {
	case "A", "AAAA", "CNAME", "TXT":
		return true
	default:
		return false
	}
}

func validIdentity(value string) bool {
	if value == "" || len(value) > 256 {
		return false
	}
	for _, character := range value {
		if !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || strings.ContainsRune("._:-", character)) {
			return false
		}
	}
	return true
}

func validDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func sameSecret(left, right []byte) bool {
	return len(left) == len(right) && subtle.ConstantTimeCompare(left, right) == 1
}

func zero(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func classified(err error) error {
	switch {
	case errors.Is(err, capability.ErrSecretDenied), errors.Is(err, capability.ErrSecretExpired), errors.Is(err, capability.ErrSecretReleased), errors.Is(err, ErrCredentialDenied):
		return capability.NewHandlerFailure("credential_denied", "fake Cloudflare credential was denied", err)
	case errors.Is(err, ErrVersionDrift):
		return capability.NewHandlerFailure("version_drift", "fake Cloudflare DNS state changed", err)
	default:
		return capability.NewHandlerFailure("plan_denied", "fake Cloudflare DNS plan was denied", err)
	}
}

const emptyInputSchema = `{"type":"object","additionalProperties":false,"properties":{}}`
const recordSchema = `{"type":"object","additionalProperties":false,"required":["id","name","type","content","ttl","version"],"properties":{"id":{"type":"string"},"name":{"type":"string"},"type":{"type":"string","enum":["A","AAAA","CNAME","TXT"]},"content":{"type":"string"},"ttl":{"type":"integer","minimum":0,"maximum":86400},"version":{"type":"integer","minimum":1}}}`
const listOutputSchema = `{"type":"object","additionalProperties":false,"required":["zone_alias","records"],"properties":{"zone_alias":{"type":"string"},"records":{"type":"array","items":` + recordSchema + `}}}`
const planInputSchema = `{"type":"object","additionalProperties":false,"required":["action"],"properties":{"action":{"type":"string","enum":["create","update","delete"]},"record_id":{"type":"string"},"name":{"type":"string"},"type":{"type":"string","enum":["A","AAAA","CNAME","TXT"]},"content":{"type":"string"},"ttl":{"type":"integer","minimum":0,"maximum":86400}}}`
const nullableRecordSchema = `{"anyOf":[` + recordSchema + `,{"type":"null"}]}`
const planRecordSchema = `{"type":"object","additionalProperties":false,"required":["id","name","type","content","ttl","version"],"properties":{"id":{"type":"string"},"name":{"type":"string"},"type":{"type":"string","enum":["A","AAAA","CNAME","TXT"]},"content":{"type":"string"},"ttl":{"type":"integer","minimum":0,"maximum":86400},"version":{"type":"integer","minimum":0}}}`
const nullablePlanRecordSchema = `{"anyOf":[` + planRecordSchema + `,{"type":"null"}]}`
const planOutputSchema = `{"type":"object","additionalProperties":false,"required":["digest","action","expected_version","before","after"],"properties":{"digest":{"type":"string","pattern":"^sha256:[0-9a-f]{64}$"},"action":{"type":"string","enum":["create","update","delete"]},"record_id":{"type":"string"},"expected_version":{"type":"integer","minimum":0},"before":` + nullablePlanRecordSchema + `,"after":` + nullablePlanRecordSchema + `}}`
const applyInputSchema = `{"type":"object","additionalProperties":false,"required":["plan_digest"],"properties":{"plan_digest":{"type":"string","pattern":"^sha256:[0-9a-f]{64}$"}}}`
const applyOutputSchema = `{"type":"object","additionalProperties":false,"required":["plan_digest","action","record"],"properties":{"plan_digest":{"type":"string","pattern":"^sha256:[0-9a-f]{64}$"},"action":{"type":"string","enum":["create","update","delete"]},"record":` + nullableRecordSchema + `}}`

var _ capability.Handler = (*Adapter)(nil)
var _ capability.AbortHandler = (*Adapter)(nil)

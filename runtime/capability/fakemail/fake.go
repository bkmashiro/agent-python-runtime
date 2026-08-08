package fakemail

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
	"github.com/bkmashiro/agent-python-runtime/runtime/transaction"
)

const (
	SearchToolID       = "mail.search"
	ReadManyToolID     = "mail.read_many"
	DraftPrepareToolID = "mail.draft.prepare"
	DraftUpdateToolID  = "mail.draft.update"
	DraftDeleteToolID  = "mail.draft.delete"
	SendToolID         = "mail.send"
	HandlerVersion     = "fake-mail-v1"
	maxTrackedState    = 4096
	maxMailboxBytes    = 16 << 20
	maxResponseBytes   = 1 << 20
)

var (
	ErrCredentialDenied     = errors.New("fake mail credential denied")
	ErrMailboxDenied        = errors.New("fake mailbox request denied")
	ErrDraftDrift           = errors.New("fake mail draft version drift")
	ErrUnknownOperation     = errors.New("fake mail operation is unknown")
	ErrSendApprovalRequired = errors.New("fake mail send approval required")
	ErrSendReconciliation   = errors.New("fake mail send requires reconciliation")
	ErrAmbiguousSend        = errors.New("fake mail provider accepted send before response loss")
)

type Message struct {
	ID      string   `json:"id"`
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	Body    string   `json:"body"`
	Version uint64   `json:"version"`
}

type Draft struct {
	ID      string   `json:"id"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	Body    string   `json:"body"`
	Version uint64   `json:"version"`
}

type SendReceipt struct {
	ProviderMessageID string `json:"provider_message_id"`
	ManifestDigest    string `json:"manifest_digest"`
	ReceiptDigest     string `json:"receipt_digest"`
}

type Provider struct {
	mu                  sync.Mutex
	readCredential      []byte
	draftCredential     []byte
	sendCredential      []byte
	messages            map[string]Message
	drafts              map[string]Draft
	sent                map[string]SendReceipt
	nextDraft           uint64
	nextVersion         uint64
	nextMessage         uint64
	ambiguousNext       bool
	acceptedTimeoutNext bool
}

func NewProvider(messages []Message, readCredential, draftCredential, sendCredential []byte) (*Provider, error) {
	if len(readCredential) == 0 || len(draftCredential) == 0 || len(sendCredential) == 0 || len(messages) > 10000 {
		return nil, ErrCredentialDenied
	}
	provider := &Provider{readCredential: append([]byte(nil), readCredential...), draftCredential: append([]byte(nil), draftCredential...), sendCredential: append([]byte(nil), sendCredential...), messages: map[string]Message{}, drafts: map[string]Draft{}, sent: map[string]SendReceipt{}, nextVersion: 1}
	var mailboxBytes int
	for _, message := range messages {
		if !validMessage(message) {
			return nil, ErrMailboxDenied
		}
		if _, exists := provider.messages[message.ID]; exists {
			return nil, ErrMailboxDenied
		}
		provider.messages[message.ID] = cloneMessage(message)
		mailboxBytes += len(message.From) + len(message.Subject) + len(message.Body)
		for _, recipient := range message.To {
			mailboxBytes += len(recipient)
		}
		if mailboxBytes > maxMailboxBytes {
			return nil, ErrMailboxDenied
		}
		if message.Version >= provider.nextVersion {
			provider.nextVersion = message.Version + 1
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
	zero(provider.draftCredential)
	zero(provider.sendCredential)
	provider.readCredential, provider.draftCredential, provider.sendCredential = nil, nil, nil
	provider.messages, provider.drafts, provider.sent = nil, nil, nil
}

func (provider *Provider) SetAmbiguousNextSend() {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.ambiguousNext = true
}

// SetAcceptedTimeoutNextSend injects a response-loss fault after the provider
// has durably accepted the send. The caller must reconcile by idempotency key;
// retrying the send is unsafe.
func (provider *Provider) SetAcceptedTimeoutNextSend() {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.acceptedTimeoutNext = true
}

func (provider *Provider) DriftDraft(id, body string) error {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	draft, exists := provider.drafts[id]
	if !exists || body == "" {
		return ErrUnknownOperation
	}
	draft.Body = body
	draft.Version = provider.allocateVersion()
	provider.drafts[id] = draft
	return nil
}

func (provider *Provider) Drafts() []Draft {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	values := make([]Draft, 0, len(provider.drafts))
	for _, draft := range provider.drafts {
		values = append(values, cloneDraft(draft))
	}
	sort.Slice(values, func(i, j int) bool { return values[i].ID < values[j].ID })
	return values
}

func (provider *Provider) SentCount() int {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return len(provider.sent)
}

type Config struct {
	Resolver       capability.SecretResolver
	ReadSecretRef  capability.SecretRef
	DraftSecretRef capability.SecretRef
	SendSecretRef  capability.SecretRef
	RunIdentity    string
	TaskIdentity   string
	Tenant         string
	AccountAlias   string
	PolicyVersion  string
	LeaseDuration  time.Duration
	Provider       *Provider
}

type Adapter struct {
	config  Config
	mu      sync.Mutex
	undo    map[string]draftUndo
	touched map[string]struct{}
}

type draftUndo struct {
	action string
	before *Draft
	after  *Draft
}

func NewAdapter(config Config) (*Adapter, error) {
	if config.Resolver == nil || config.Provider == nil || config.ReadSecretRef == "" || config.DraftSecretRef == "" || config.SendSecretRef == "" || !validIdentity(config.RunIdentity) || !validIdentity(config.TaskIdentity) || !validIdentity(config.Tenant) || !validIdentity(config.AccountAlias) || !validIdentity(config.PolicyVersion) || config.LeaseDuration <= 0 || config.LeaseDuration > time.Hour {
		return nil, errors.New("invalid fake mail adapter configuration")
	}
	return &Adapter{config: config, undo: map[string]draftUndo{}, touched: map[string]struct{}{}}, nil
}

func HandlerSpecs(adapter *Adapter) ([]capability.HandlerSpec, error) {
	if adapter == nil {
		return nil, ErrMailboxDenied
	}
	return []capability.HandlerSpec{
		{ToolID: SearchToolID, HandlerVersion: HandlerVersion, InputSchema: []byte(searchInputSchema), OutputSchema: []byte(searchOutputSchema), Handler: adapter},
		{ToolID: ReadManyToolID, HandlerVersion: HandlerVersion, InputSchema: []byte(readInputSchema), OutputSchema: []byte(readOutputSchema), Handler: adapter},
		{ToolID: DraftPrepareToolID, HandlerVersion: HandlerVersion, InputSchema: []byte(draftPrepareSchema), OutputSchema: []byte(draftOutputSchema), Handler: adapter},
		{ToolID: DraftUpdateToolID, HandlerVersion: HandlerVersion, InputSchema: []byte(draftUpdateSchema), OutputSchema: []byte(draftOutputSchema), Handler: adapter},
		{ToolID: DraftDeleteToolID, HandlerVersion: HandlerVersion, InputSchema: []byte(draftDeleteSchema), OutputSchema: []byte(deleteOutputSchema), Handler: adapter},
	}, nil
}

func ToolGrants(policyVersion string, maxCalls uint32) (map[string]capability.ToolGrant, error) {
	if !validIdentity(policyVersion) || maxCalls == 0 {
		return nil, ErrMailboxDenied
	}
	grants := map[string]capability.ToolGrant{}
	for _, toolID := range []string{SearchToolID, ReadManyToolID} {
		grants[toolID] = capability.ToolGrant{ToolID: toolID, HandlerVersion: HandlerVersion, EffectClass: "read_only", Policy: "AUTO_COMMIT", PolicyVersion: policyVersion, MaxCalls: maxCalls}
	}
	for _, toolID := range []string{DraftPrepareToolID, DraftUpdateToolID, DraftDeleteToolID} {
		grants[toolID] = capability.ToolGrant{ToolID: toolID, HandlerVersion: HandlerVersion, EffectClass: "reversible", Policy: "AUTO_COMMIT", PolicyVersion: policyVersion, MaxCalls: maxCalls}
	}
	return grants, nil
}

func CatalogTools(maxCalls uint32, grantVersion string) ([]toolcatalog.DiscoveredTool, map[string]toolcatalog.Grant, error) {
	if maxCalls == 0 || maxCalls > 1024 || !validIdentity(grantVersion) {
		return nil, nil, ErrMailboxDenied
	}
	tools := []toolcatalog.DiscoveredTool{
		{ToolID: SearchToolID, ServerID: "fake-mail", Name: "mail_search", Description: "Search bounded messages in the Host-bound fake mailbox.", HandlerVersion: HandlerVersion, InputSchema: []byte(searchInputSchema), OutputSchema: []byte(searchOutputSchema)},
		{ToolID: ReadManyToolID, ServerID: "fake-mail", Name: "mail_read_many", Description: "Read bounded message IDs from the Host-bound fake mailbox.", HandlerVersion: HandlerVersion, InputSchema: []byte(readInputSchema), OutputSchema: []byte(readOutputSchema)},
		{ToolID: DraftPrepareToolID, ServerID: "fake-mail", Name: "mail_draft_prepare", Description: "Create a reversible draft in the Host-bound fake mailbox.", HandlerVersion: HandlerVersion, InputSchema: []byte(draftPrepareSchema), OutputSchema: []byte(draftOutputSchema)},
		{ToolID: DraftUpdateToolID, ServerID: "fake-mail", Name: "mail_draft_update", Description: "Conditionally update an exact fake draft version.", HandlerVersion: HandlerVersion, InputSchema: []byte(draftUpdateSchema), OutputSchema: []byte(draftOutputSchema)},
		{ToolID: DraftDeleteToolID, ServerID: "fake-mail", Name: "mail_draft_delete", Description: "Conditionally delete an exact fake draft version.", HandlerVersion: HandlerVersion, InputSchema: []byte(draftDeleteSchema), OutputSchema: []byte(deleteOutputSchema)},
	}
	grants := make(map[string]toolcatalog.Grant, len(tools))
	for _, tool := range tools {
		effectClass := "read_only"
		if tool.ToolID != SearchToolID && tool.ToolID != ReadManyToolID {
			effectClass = "reversible"
		}
		grants[tool.ToolID] = toolcatalog.Grant{ToolID: tool.ToolID, EffectClass: effectClass, Policy: "AUTO_COMMIT", GrantVersion: grantVersion, MaxCalls: maxCalls}
	}
	return tools, grants, nil
}

func (adapter *Adapter) Handle(ctx context.Context, call capability.HostCall) (json.RawMessage, error) {
	if call.RunIdentity != adapter.config.RunIdentity {
		return nil, classified(ErrCredentialDenied)
	}
	switch call.ToolID {
	case SearchToolID:
		return adapter.handleSearch(ctx, call)
	case ReadManyToolID:
		return adapter.handleRead(ctx, call)
	case DraftPrepareToolID, DraftUpdateToolID, DraftDeleteToolID:
		return adapter.handleDraft(ctx, call)
	default:
		return nil, classified(ErrMailboxDenied)
	}
}

type searchArguments struct {
	Query      string `json:"query"`
	MaxResults uint32 `json:"max_results"`
}

func (adapter *Adapter) handleSearch(ctx context.Context, call capability.HostCall) (json.RawMessage, error) {
	var arguments searchArguments
	if json.Unmarshal(call.Arguments, &arguments) != nil || arguments.Query == "" || len(arguments.Query) > 256 || arguments.MaxResults == 0 || arguments.MaxResults > 100 {
		return nil, classified(ErrMailboxDenied)
	}
	var messages []Message
	var truncated bool
	err := adapter.withSecret(ctx, adapter.config.ReadSecretRef, call.ToolID, "read", func(value []byte) error {
		var searchErr error
		messages, truncated, searchErr = adapter.config.Provider.search(value, arguments.Query, arguments.MaxResults)
		return searchErr
	})
	if err != nil {
		return nil, classified(err)
	}
	encoded, err := json.Marshal(struct {
		Messages  []Message `json:"messages"`
		Truncated bool      `json:"truncated"`
	}{Messages: messages, Truncated: truncated})
	if err != nil || len(encoded) > maxResponseBytes {
		return nil, classified(ErrMailboxDenied)
	}
	return encoded, nil
}

type readArguments struct {
	MessageIDs []string `json:"message_ids"`
}

func (adapter *Adapter) handleRead(ctx context.Context, call capability.HostCall) (json.RawMessage, error) {
	var arguments readArguments
	if json.Unmarshal(call.Arguments, &arguments) != nil || len(arguments.MessageIDs) == 0 || len(arguments.MessageIDs) > 32 {
		return nil, classified(ErrMailboxDenied)
	}
	var messages []Message
	err := adapter.withSecret(ctx, adapter.config.ReadSecretRef, call.ToolID, "read", func(value []byte) error {
		var readErr error
		messages, readErr = adapter.config.Provider.readMany(value, arguments.MessageIDs)
		return readErr
	})
	if err != nil {
		return nil, classified(err)
	}
	encoded, err := json.Marshal(struct {
		Messages []Message `json:"messages"`
	}{Messages: messages})
	if err != nil || len(encoded) > maxResponseBytes {
		return nil, classified(ErrMailboxDenied)
	}
	return encoded, nil
}

type draftArguments struct {
	DraftID         string   `json:"draft_id,omitempty"`
	ExpectedVersion uint64   `json:"expected_version,omitempty"`
	To              []string `json:"to,omitempty"`
	Subject         string   `json:"subject,omitempty"`
	Body            string   `json:"body,omitempty"`
}

func (adapter *Adapter) handleDraft(ctx context.Context, call capability.HostCall) (json.RawMessage, error) {
	var arguments draftArguments
	if json.Unmarshal(call.Arguments, &arguments) != nil {
		return nil, classified(ErrMailboxDenied)
	}
	touchKey := ""
	if call.ToolID != DraftPrepareToolID {
		touchKey = call.TransactionID + "\x00" + arguments.DraftID
	}
	adapter.mu.Lock()
	if len(adapter.undo) >= maxTrackedState || len(adapter.touched) >= maxTrackedState {
		adapter.mu.Unlock()
		return nil, classified(ErrMailboxDenied)
	}
	if _, exists := adapter.undo[call.OperationID]; exists {
		adapter.mu.Unlock()
		return nil, classified(ErrMailboxDenied)
	}
	if touchKey != "" {
		if _, exists := adapter.touched[touchKey]; exists {
			adapter.mu.Unlock()
			return nil, classified(ErrDraftDrift)
		}
		adapter.touched[touchKey] = struct{}{}
	}
	adapter.undo[call.OperationID] = draftUndo{}
	adapter.mu.Unlock()
	var undo draftUndo
	var result json.RawMessage
	err := adapter.withSecret(ctx, adapter.config.DraftSecretRef, call.ToolID, "apply", func(value []byte) error {
		switch call.ToolID {
		case DraftPrepareToolID:
			before, draft, applyErr := adapter.config.Provider.createDraft(value, arguments)
			undo = draftUndo{action: "create", before: before, after: cloneDraftPtr(draft)}
			if applyErr == nil {
				result, _ = json.Marshal(draft)
			}
			return applyErr
		case DraftUpdateToolID:
			before, draft, applyErr := adapter.config.Provider.updateDraft(value, arguments)
			undo = draftUndo{action: "update", before: cloneDraftPtr(before), after: cloneDraftPtr(draft)}
			if applyErr == nil {
				result, _ = json.Marshal(draft)
			}
			return applyErr
		default:
			before, applyErr := adapter.config.Provider.deleteDraft(value, arguments)
			undo = draftUndo{action: "delete", before: cloneDraftPtr(before)}
			if applyErr == nil {
				result, _ = json.Marshal(struct {
					DeletedID string `json:"deleted_id"`
				}{DeletedID: arguments.DraftID})
			}
			return applyErr
		}
	})
	if err != nil {
		adapter.mu.Lock()
		delete(adapter.undo, call.OperationID)
		if touchKey != "" {
			delete(adapter.touched, touchKey)
		}
		adapter.mu.Unlock()
		return nil, classified(err)
	}
	adapter.mu.Lock()
	adapter.undo[call.OperationID] = undo
	if call.ToolID == DraftPrepareToolID {
		adapter.touched[call.TransactionID+"\x00"+undo.after.ID] = struct{}{}
	}
	adapter.mu.Unlock()
	return result, nil
}

func (adapter *Adapter) Rollback(ctx context.Context, call capability.AbortCall) error {
	adapter.mu.Lock()
	undo, exists := adapter.undo[call.Operation.ID]
	adapter.mu.Unlock()
	if !exists || undo.action == "" {
		return ErrUnknownOperation
	}
	return adapter.withSecret(ctx, adapter.config.DraftSecretRef, call.Operation.ToolID, "rollback", func(value []byte) error {
		return adapter.config.Provider.rollbackDraft(value, undo)
	})
}

func (adapter *Adapter) Compensate(context.Context, capability.AbortCall) error {
	return errors.New("fake mail draft operations are not compensatable")
}

func (adapter *Adapter) withSecret(ctx context.Context, reference capability.SecretRef, toolID, operation string, use func([]byte) error) error {
	lease, err := adapter.config.Resolver.Resolve(ctx, capability.SecretRequest{Reference: reference, RunIdentity: adapter.config.RunIdentity, TaskIdentity: adapter.config.TaskIdentity, Tenant: adapter.config.Tenant, ToolID: toolID, Operation: operation, ResourceAlias: adapter.config.AccountAlias, PolicyVersion: adapter.config.PolicyVersion, RequestedLease: adapter.config.LeaseDuration})
	if err != nil {
		return err
	}
	defer lease.Release()
	return lease.WithValue(use)
}

func (provider *Provider) search(credential []byte, query string, max uint32) ([]Message, bool, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if !sameSecret(credential, provider.readCredential) {
		return nil, false, ErrCredentialDenied
	}
	ids := make([]string, 0, len(provider.messages))
	for id := range provider.messages {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := []Message{}
	query = strings.ToLower(query)
	for _, id := range ids {
		message := provider.messages[id]
		if !strings.Contains(strings.ToLower(message.Subject+"\n"+message.Body), query) {
			continue
		}
		if uint32(len(result)) == max {
			return result, true, nil
		}
		result = append(result, cloneMessage(message))
	}
	return result, false, nil
}

func (provider *Provider) readMany(credential []byte, ids []string) ([]Message, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if !sameSecret(credential, provider.readCredential) {
		return nil, ErrCredentialDenied
	}
	seen := map[string]struct{}{}
	result := make([]Message, 0, len(ids))
	for _, id := range ids {
		message, exists := provider.messages[id]
		if !exists || !validIdentity(id) {
			return nil, ErrMailboxDenied
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, ErrMailboxDenied
		}
		seen[id] = struct{}{}
		result = append(result, cloneMessage(message))
	}
	return result, nil
}

func (provider *Provider) createDraft(credential []byte, arguments draftArguments) (*Draft, Draft, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if !sameSecret(credential, provider.draftCredential) || arguments.DraftID != "" || arguments.ExpectedVersion != 0 || !validMailPayload(arguments.To, arguments.Subject, arguments.Body) {
		return nil, Draft{}, ErrCredentialOrDraft(arguments, provider.draftCredential, credential)
	}
	for {
		provider.nextDraft++
		id := fmt.Sprintf("draft:%d", provider.nextDraft)
		if _, exists := provider.drafts[id]; exists {
			continue
		}
		draft := Draft{ID: id, To: append([]string(nil), arguments.To...), Subject: arguments.Subject, Body: arguments.Body, Version: provider.allocateVersion()}
		provider.drafts[id] = draft
		return nil, cloneDraft(draft), nil
	}
}

func (provider *Provider) updateDraft(credential []byte, arguments draftArguments) (Draft, Draft, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if !sameSecret(credential, provider.draftCredential) || !validMailPayload(arguments.To, arguments.Subject, arguments.Body) {
		return Draft{}, Draft{}, ErrCredentialDenied
	}
	before, exists := provider.drafts[arguments.DraftID]
	if !exists || before.Version != arguments.ExpectedVersion {
		return Draft{}, Draft{}, ErrDraftDrift
	}
	after := Draft{ID: before.ID, To: append([]string(nil), arguments.To...), Subject: arguments.Subject, Body: arguments.Body, Version: provider.allocateVersion()}
	provider.drafts[after.ID] = after
	return cloneDraft(before), cloneDraft(after), nil
}

func (provider *Provider) deleteDraft(credential []byte, arguments draftArguments) (Draft, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if !sameSecret(credential, provider.draftCredential) {
		return Draft{}, ErrCredentialDenied
	}
	before, exists := provider.drafts[arguments.DraftID]
	if !exists || before.Version != arguments.ExpectedVersion || len(arguments.To) != 0 || arguments.Subject != "" || arguments.Body != "" {
		return Draft{}, ErrDraftDrift
	}
	delete(provider.drafts, before.ID)
	return cloneDraft(before), nil
}

func (provider *Provider) rollbackDraft(credential []byte, undo draftUndo) error {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if !sameSecret(credential, provider.draftCredential) {
		return ErrCredentialDenied
	}
	switch undo.action {
	case "create":
		current, exists := provider.drafts[undo.after.ID]
		if !exists || !sameDraft(current, *undo.after) {
			return ErrDraftDrift
		}
		delete(provider.drafts, current.ID)
	case "update":
		current, exists := provider.drafts[undo.after.ID]
		if !exists || !sameDraft(current, *undo.after) {
			return ErrDraftDrift
		}
		restored := cloneDraft(*undo.before)
		restored.Version = provider.allocateVersion()
		provider.drafts[restored.ID] = restored
	case "delete":
		if _, exists := provider.drafts[undo.before.ID]; exists {
			return ErrDraftDrift
		}
		restored := cloneDraft(*undo.before)
		restored.Version = provider.allocateVersion()
		provider.drafts[restored.ID] = restored
	default:
		return ErrUnknownOperation
	}
	return nil
}

func ErrCredentialOrDraft(arguments draftArguments, expected, actual []byte) error {
	if !sameSecret(expected, actual) {
		return ErrCredentialDenied
	}
	return ErrMailboxDenied
}

// SendRequest is Host-staged data. Recipients are restricted to .invalid in the
// fake provider so tests cannot accidentally address a real mailbox.
type SendRequest struct {
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	Body    string   `json:"body"`
}

type StagedSend struct {
	OperationID     string `json:"operation_id"`
	ManifestDigest  string `json:"manifest_digest"`
	RecipientDigest string `json:"recipient_digest"`
	PayloadDigest   string `json:"payload_digest"`
}

type stagedSend struct {
	public         StagedSend
	request        SendRequest
	approvalDigest string
	attemptID      string
	status         string
	receipt        SendReceipt
}

type SendController struct {
	mu            sync.Mutex
	coordinator   *transaction.Coordinator
	transactionID string
	adapter       *Adapter
	ordinal       uint32
	staged        map[string]*stagedSend
}

func NewSendController(coordinator *transaction.Coordinator, transactionID string, adapter *Adapter) (*SendController, error) {
	if coordinator == nil || adapter == nil || !validIdentity(transactionID) {
		return nil, ErrSendApprovalRequired
	}
	if _, err := coordinator.InspectTransaction(transactionID); err != nil {
		return nil, ErrSendApprovalRequired
	}
	return &SendController{coordinator: coordinator, transactionID: transactionID, adapter: adapter, staged: map[string]*stagedSend{}}, nil
}

func (controller *SendController) Prepare(request SendRequest) (StagedSend, error) {
	if !validMailPayload(request.To, request.Subject, request.Body) {
		return StagedSend{}, ErrMailboxDenied
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if len(controller.staged) >= maxTrackedState {
		return StagedSend{}, ErrMailboxDenied
	}
	canonical, _ := json.Marshal(request)
	argumentDigest := digest(canonical)
	operation, err := controller.coordinator.Propose(transaction.ProposeRequest{TransactionID: controller.transactionID, ToolID: SendToolID, HandlerVersion: HandlerVersion, EffectClass: transaction.EffectIrreversible, Policy: transaction.PolicyUserApprovalRequired, PolicyVersion: controller.adapter.config.PolicyVersion, ArgumentDigest: argumentDigest})
	if err != nil {
		return StagedSend{}, err
	}
	staged := &stagedSend{public: StagedSend{OperationID: operation.ID, ManifestDigest: operation.ManifestDigest, RecipientDigest: digest([]byte(strings.Join(request.To, "\x00"))), PayloadDigest: digest([]byte(request.Subject + "\x00" + request.Body))}, request: SendRequest{To: append([]string(nil), request.To...), Subject: request.Subject, Body: request.Body}, status: "awaiting_approval"}
	controller.staged[operation.ID] = staged
	return staged.public, nil
}

func (controller *SendController) RegisterApproval(credential transaction.CommitCredential, operationID, authorityID, actorID string, expiresAt time.Time) error {
	controller.mu.Lock()
	staged, exists := controller.staged[operationID]
	controller.mu.Unlock()
	if !exists {
		return ErrSendApprovalRequired
	}
	_, err := controller.coordinator.RegisterApproval(credential, transaction.AuthorityClaims{AuthorityID: authorityID, TransactionID: controller.transactionID, OperationID: operationID, ManifestDigest: staged.public.ManifestDigest, Source: transaction.CommitSourceUser, ActorID: actorID, ExpiresAt: expiresAt})
	return err
}

func (controller *SendController) Commit(ctx context.Context, credential transaction.CommitCredential, operationID string) (SendReceipt, error) {
	controller.mu.Lock()
	staged, exists := controller.staged[operationID]
	if !exists {
		controller.mu.Unlock()
		return SendReceipt{}, ErrSendApprovalRequired
	}
	credentialDigest := digest([]byte("approval\x00" + credential.Token))
	if staged.status == "committed" {
		if staged.approvalDigest != credentialDigest {
			controller.mu.Unlock()
			return SendReceipt{}, ErrSendApprovalRequired
		}
		receipt := staged.receipt
		controller.mu.Unlock()
		return receipt, nil
	}
	if staged.status == "ambiguous" || staged.status == "dispatching" {
		controller.mu.Unlock()
		return SendReceipt{}, ErrSendReconciliation
	}
	if staged.status == "failed" {
		controller.mu.Unlock()
		return SendReceipt{}, ErrSendApprovalRequired
	}
	controller.mu.Unlock()
	operation, err := controller.coordinator.Authorize(credential)
	if err != nil || operation.ID != operationID || operation.ManifestDigest != staged.public.ManifestDigest {
		return SendReceipt{}, ErrSendApprovalRequired
	}
	controller.mu.Lock()
	controller.ordinal++
	ordinal := controller.ordinal
	controller.mu.Unlock()
	dispatch, err := controller.coordinator.BeginDispatch(transaction.DispatchRequest{OperationID: operationID, Kind: transaction.AttemptApply, Ordinal: ordinal, LeaseDuration: controller.adapter.config.LeaseDuration, ProviderRequestDigest: staged.public.ManifestDigest})
	if err != nil {
		return SendReceipt{}, err
	}
	controller.mu.Lock()
	staged.attemptID = dispatch.Attempt.ID
	staged.approvalDigest = credentialDigest
	staged.status = "dispatching"
	controller.mu.Unlock()
	var receipt SendReceipt
	var ambiguous bool
	err = controller.adapter.withSecret(ctx, controller.adapter.config.SendSecretRef, SendToolID, "commit", func(value []byte) error {
		var sendErr error
		receipt, ambiguous, sendErr = controller.adapter.config.Provider.send(value, staged.public.ManifestDigest, staged.request)
		return sendErr
	})
	if err != nil {
		outcome := transaction.DispatchFailed
		status := "failed"
		resultErr := classified(err)
		if errors.Is(err, ErrAmbiguousSend) {
			outcome = transaction.DispatchAmbiguous
			status = "ambiguous"
			resultErr = ErrSendReconciliation
		}
		controller.mu.Lock()
		staged.status = status
		controller.mu.Unlock()
		_, completeErr := controller.coordinator.CompleteDispatch(transaction.CompleteDispatchRequest{OperationID: operationID, AttemptID: dispatch.Attempt.ID, Outcome: outcome})
		return SendReceipt{}, errors.Join(resultErr, completeErr)
	}
	controller.mu.Lock()
	staged.receipt = receipt
	if ambiguous {
		staged.status = "ambiguous"
	} else {
		staged.status = "completing"
	}
	controller.mu.Unlock()
	if ambiguous {
		_, completeErr := controller.coordinator.CompleteDispatch(transaction.CompleteDispatchRequest{OperationID: operationID, AttemptID: dispatch.Attempt.ID, Outcome: transaction.DispatchAmbiguous})
		return SendReceipt{}, errors.Join(ErrSendReconciliation, completeErr)
	}
	_, err = controller.coordinator.CompleteAuthorizedDispatch(credential, transaction.CompleteDispatchRequest{OperationID: operationID, AttemptID: dispatch.Attempt.ID, Outcome: transaction.DispatchSucceeded, ProviderReceiptDigest: receipt.ReceiptDigest})
	if err != nil {
		_, _ = controller.coordinator.CompleteDispatch(transaction.CompleteDispatchRequest{OperationID: operationID, AttemptID: dispatch.Attempt.ID, Outcome: transaction.DispatchAmbiguous})
		controller.mu.Lock()
		staged.status = "ambiguous"
		controller.mu.Unlock()
		return SendReceipt{}, errors.Join(ErrSendReconciliation, err)
	}
	controller.mu.Lock()
	staged.status = "committed"
	controller.mu.Unlock()
	return receipt, nil
}

func (controller *SendController) Reconcile(ctx context.Context, credential transaction.CommitCredential, operationID string) (SendReceipt, error) {
	controller.mu.Lock()
	staged, exists := controller.staged[operationID]
	if !exists || staged.status != "ambiguous" || staged.approvalDigest != digest([]byte("approval\x00"+credential.Token)) {
		controller.mu.Unlock()
		return SendReceipt{}, ErrSendReconciliation
	}
	attemptID := staged.attemptID
	manifest := staged.public.ManifestDigest
	controller.mu.Unlock()
	var receipt SendReceipt
	err := controller.adapter.withSecret(ctx, controller.adapter.config.SendSecretRef, SendToolID, "reconcile", func(value []byte) error {
		var lookupErr error
		receipt, lookupErr = controller.adapter.config.Provider.lookupSent(value, manifest)
		return lookupErr
	})
	if err != nil {
		return SendReceipt{}, ErrSendReconciliation
	}
	_, err = controller.coordinator.ReconcileAuthorizedDispatch(credential, transaction.ReconcileDispatchRequest{
		OperationID: operationID, AttemptID: attemptID, Outcome: transaction.DispatchSucceeded,
		ProviderReceiptDigest: receipt.ReceiptDigest, ObservationDigest: receipt.ReceiptDigest,
	})
	if err != nil {
		return SendReceipt{}, err
	}
	controller.mu.Lock()
	staged.status = "committed"
	staged.receipt = receipt
	controller.mu.Unlock()
	return receipt, nil
}

func (provider *Provider) send(credential []byte, manifest string, request SendRequest) (SendReceipt, bool, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if !sameSecret(credential, provider.sendCredential) || !validMailPayload(request.To, request.Subject, request.Body) {
		return SendReceipt{}, false, ErrCredentialDenied
	}
	if prior, exists := provider.sent[manifest]; exists {
		return prior, false, nil
	}
	provider.nextMessage++
	receipt := SendReceipt{ProviderMessageID: fmt.Sprintf("sent:%d", provider.nextMessage), ManifestDigest: manifest}
	receipt.ReceiptDigest = digest([]byte(receipt.ProviderMessageID + "\x00" + manifest))
	provider.sent[manifest] = receipt
	if provider.acceptedTimeoutNext {
		provider.acceptedTimeoutNext = false
		return SendReceipt{}, false, ErrAmbiguousSend
	}
	ambiguous := provider.ambiguousNext
	provider.ambiguousNext = false
	return receipt, ambiguous, nil
}

func (provider *Provider) lookupSent(credential []byte, manifest string) (SendReceipt, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if !sameSecret(credential, provider.sendCredential) {
		return SendReceipt{}, ErrCredentialDenied
	}
	receipt, exists := provider.sent[manifest]
	if !exists {
		return SendReceipt{}, ErrUnknownOperation
	}
	return receipt, nil
}

func (provider *Provider) allocateVersion() uint64 {
	version := provider.nextVersion
	provider.nextVersion++
	return version
}
func validMessage(value Message) bool {
	return validIdentity(value.ID) && validAddress(value.From) && validMailPayload(value.To, value.Subject, value.Body) && value.Version > 0
}
func validMailPayload(to []string, subject, body string) bool {
	if len(to) == 0 || len(to) > 16 || subject == "" || len(subject) > 998 || body == "" || len(body) > 64<<10 || strings.ContainsAny(subject, "\r\n") {
		return false
	}
	for _, address := range to {
		if !validAddress(address) {
			return false
		}
	}
	return true
}
func validAddress(value string) bool {
	if strings.ContainsAny(value, "\r\n") {
		return false
	}
	at := strings.LastIndex(value, "@")
	return at > 0 && strings.HasSuffix(strings.ToLower(value[at+1:]), ".invalid") && len(value) <= 320
}
func validIdentity(value string) bool {
	if value == "" || len(value) > 256 {
		return false
	}
	for _, c := range value {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || strings.ContainsRune("._:-", c)) {
			return false
		}
	}
	return true
}
func sameSecret(a, b []byte) bool { return len(a) == len(b) && subtle.ConstantTimeCompare(a, b) == 1 }
func sameDraft(a, b Draft) bool {
	if a.ID != b.ID || a.Subject != b.Subject || a.Body != b.Body || a.Version != b.Version || len(a.To) != len(b.To) {
		return false
	}
	for i := range a.To {
		if a.To[i] != b.To[i] {
			return false
		}
	}
	return true
}
func cloneDraft(value Draft) Draft       { value.To = append([]string(nil), value.To...); return value }
func cloneDraftPtr(value Draft) *Draft   { cloned := cloneDraft(value); return &cloned }
func cloneMessage(value Message) Message { value.To = append([]string(nil), value.To...); return value }
func zero(value []byte) {
	for i := range value {
		value[i] = 0
	}
}
func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func classified(err error) error {
	switch {
	case errors.Is(err, capability.ErrSecretDenied), errors.Is(err, capability.ErrSecretExpired), errors.Is(err, capability.ErrSecretReleased), errors.Is(err, ErrCredentialDenied):
		return capability.NewHandlerFailure("credential_denied", "fake mail credential was denied", err)
	case errors.Is(err, ErrDraftDrift):
		return capability.NewHandlerFailure("version_drift", "fake mail draft changed", err)
	default:
		return capability.NewHandlerFailure("mail_denied", "fake mail operation was denied", err)
	}
}

const messageSchema = `{"type":"object","additionalProperties":false,"required":["id","from","to","subject","body","version"],"properties":{"id":{"type":"string"},"from":{"type":"string"},"to":{"type":"array","items":{"type":"string"}},"subject":{"type":"string"},"body":{"type":"string"},"version":{"type":"integer","minimum":1}}}`
const draftSchema = `{"type":"object","additionalProperties":false,"required":["id","to","subject","body","version"],"properties":{"id":{"type":"string"},"to":{"type":"array","items":{"type":"string"}},"subject":{"type":"string"},"body":{"type":"string"},"version":{"type":"integer","minimum":1}}}`
const searchInputSchema = `{"type":"object","additionalProperties":false,"required":["query","max_results"],"properties":{"query":{"type":"string","minLength":1,"maxLength":256},"max_results":{"type":"integer","minimum":1,"maximum":100}}}`
const searchOutputSchema = `{"type":"object","additionalProperties":false,"required":["messages","truncated"],"properties":{"messages":{"type":"array","items":` + messageSchema + `},"truncated":{"type":"boolean"}}}`
const readInputSchema = `{"type":"object","additionalProperties":false,"required":["message_ids"],"properties":{"message_ids":{"type":"array","minItems":1,"maxItems":32,"items":{"type":"string"}}}}`
const readOutputSchema = `{"type":"object","additionalProperties":false,"required":["messages"],"properties":{"messages":{"type":"array","items":` + messageSchema + `}}}`
const mailPayloadProperties = `"to":{"type":"array","minItems":1,"maxItems":16,"items":{"type":"string"}},"subject":{"type":"string","minLength":1,"maxLength":998},"body":{"type":"string","minLength":1,"maxLength":65536}`
const draftPrepareSchema = `{"type":"object","additionalProperties":false,"required":["to","subject","body"],"properties":{` + mailPayloadProperties + `}}`
const draftUpdateSchema = `{"type":"object","additionalProperties":false,"required":["draft_id","expected_version","to","subject","body"],"properties":{"draft_id":{"type":"string"},"expected_version":{"type":"integer","minimum":1},` + mailPayloadProperties + `}}`
const draftDeleteSchema = `{"type":"object","additionalProperties":false,"required":["draft_id","expected_version"],"properties":{"draft_id":{"type":"string"},"expected_version":{"type":"integer","minimum":1}}}`
const draftOutputSchema = draftSchema
const deleteOutputSchema = `{"type":"object","additionalProperties":false,"required":["deleted_id"],"properties":{"deleted_id":{"type":"string"}}}`

var _ capability.Handler = (*Adapter)(nil)
var _ capability.AbortHandler = (*Adapter)(nil)

// Package compensation provides Host-owned planning and execution contracts for
// semantic undo of already-published tool effects. It does not roll back private
// workspace state and does not infer authority from Agent-authored Python.
package compensation

import (
	"context"
	"errors"
)

const PlanSchemaVersion = "pysolate.compensation-plan.v1"

type Semantics string

const (
	SemanticsExact        Semantics = "exact"
	SemanticsCompensating Semantics = "compensating"
	SemanticsBestEffort   Semantics = "best_effort"
	SemanticsGuidance     Semantics = "guidance"
	SemanticsIrreversible Semantics = "irreversible"
	SemanticsUnknown      Semantics = "unknown"
)

type StrategyMode string

const (
	ModeExecutable StrategyMode = "executable"
	ModeGuidance   StrategyMode = "guidance"
)

type Approval string

const (
	ApprovalAgentReview  Approval = "agent_review_required"
	ApprovalUserRequired Approval = "user_approval_required"
)

type DryRunMode string

const (
	DryRunPlan     DryRunMode = "plan"
	DryRunValidate DryRunMode = "validate"
)

type EffectOutcome string

const (
	EffectApplied   EffectOutcome = "applied"
	EffectAmbiguous EffectOutcome = "ambiguous"
)

type ValidationStatus string

const (
	ValidationNotChecked             ValidationStatus = "not_checked"
	ValidationApplicable             ValidationStatus = "applicable"
	ValidationGuidance               ValidationStatus = "guidance_only"
	ValidationReconciliationRequired ValidationStatus = "reconciliation_required"
)

type ExecutionStatus string

const (
	ExecutionCompensated          ExecutionStatus = "compensated"
	ExecutionPartiallyCompensated ExecutionStatus = "partially_compensated"
	ExecutionManualRequired       ExecutionStatus = "manual_required"
	ExecutionFailed               ExecutionStatus = "failed"
	ExecutionReconciliationNeeded ExecutionStatus = "reconciliation_required"
)

type ReceiptStatus string

const (
	ReceiptCompensated          ReceiptStatus = "compensated"
	ReceiptFailed               ReceiptStatus = "failed"
	ReceiptManualRequired       ReceiptStatus = "manual_required"
	ReceiptBlocked              ReceiptStatus = "blocked"
	ReceiptReconciliationNeeded ReceiptStatus = "reconciliation_required"
)

type ProviderOutcome string

const (
	ProviderCompensated          ProviderOutcome = "compensated"
	ProviderNotApplied           ProviderOutcome = "not_applied"
	ProviderReconciliationNeeded ProviderOutcome = "reconciliation_required"
)

var (
	ErrInvalidConfig          = errors.New("invalid compensation controller configuration")
	ErrInvalidContract        = errors.New("invalid tool compensation contract")
	ErrInvalidRequest         = errors.New("invalid compensation preview request")
	ErrUnknownCapability      = errors.New("effect capability has no compensation contract")
	ErrNoApplicableStrategy   = errors.New("no applicable compensation strategy")
	ErrValidationFailed       = errors.New("compensation validation failed")
	ErrPlanCapacity           = errors.New("compensation plan capacity exhausted")
	ErrJournalCapacity        = errors.New("compensation journal capacity exhausted")
	ErrReviewMismatch         = errors.New("review does not bind the exact compensation plan")
	ErrAuthorityRequired      = errors.New("fresh compensation authority is required")
	ErrUserApprovalRequired   = errors.New("user approval is required")
	ErrStalePlan              = errors.New("compensation plan is stale")
	ErrPlanInProgress         = errors.New("compensation plan is already executing")
	ErrEffectInProgress       = errors.New("effect compensation is already executing")
	ErrAlreadyCompensated     = errors.New("effect is already compensated")
	ErrEffectBlocked          = errors.New("effect is blocked by an earlier compensation outcome")
	ErrExecutionFailed        = errors.New("compensation execution failed")
	ErrReconciliationRequired = errors.New("provider reconciliation is required")
)

// Strategy is Host-authored adapter policy. Executable operations and guidance
// are selected from this bounded declaration, never invented by the Agent.
type Strategy struct {
	ID           string       `json:"id"`
	Semantics    Semantics    `json:"semantics"`
	Mode         StrategyMode `json:"mode"`
	Rank         uint16       `json:"rank"`
	Operation    string       `json:"operation,omitempty"`
	Approval     Approval     `json:"approval"`
	Guidance     string       `json:"guidance,omitempty"`
	Precondition string       `json:"precondition,omitempty"`
	Risk         string       `json:"risk,omitempty"`
}

// ToolContract declares ordered compensation fallbacks for one original tool
// capability. ReconciliationGuidance applies only when the original provider
// outcome is ambiguous and compensation must not start yet.
type ToolContract struct {
	Capability             string     `json:"capability"`
	Strategies             []Strategy `json:"strategies"`
	ReconciliationGuidance string     `json:"reconciliation_guidance,omitempty"`
}

// EffectReceipt is the minimum Host-owned fact needed by the v1 planner. The
// target and version are never projected into Agent review Python.
type EffectReceipt struct {
	EffectID        string        `json:"effect_id"`
	EffectGroupID   string        `json:"effect_group_id"`
	RunID           string        `json:"run_id"`
	Capability      string        `json:"capability"`
	OperationIndex  uint32        `json:"operation_index"`
	TargetID        string        `json:"target_id"`
	AfterVersion    string        `json:"after_version"`
	ArgumentsSHA256 string        `json:"arguments_sha256"`
	ResultSHA256    string        `json:"result_sha256"`
	Outcome         EffectOutcome `json:"outcome"`
	DependsOn       []string      `json:"depends_on,omitempty"`
}

type PreviewRequest struct {
	EffectGroupID string          `json:"effect_group_id"`
	Mode          DryRunMode      `json:"mode"`
	Receipts      []EffectReceipt `json:"receipts"`
}

type Validation struct {
	Applicable     bool   `json:"applicable"`
	Reason         string `json:"reason,omitempty"`
	CurrentVersion string `json:"current_version,omitempty"`
}

type StrategyRejection struct {
	StrategyID string `json:"strategy_id"`
	Reason     string `json:"reason"`
}

type Step struct {
	EffectID            string              `json:"effect_id"`
	SourceRunID         string              `json:"source_run_id"`
	SourceReceiptSHA256 string              `json:"source_receipt_sha256"`
	Capability          string              `json:"capability"`
	StrategyID          string              `json:"strategy_id"`
	Semantics           Semantics           `json:"semantics"`
	Mode                StrategyMode        `json:"mode"`
	Operation           string              `json:"operation,omitempty"`
	Approval            Approval            `json:"approval"`
	Guidance            string              `json:"guidance,omitempty"`
	Precondition        string              `json:"precondition,omitempty"`
	Risk                string              `json:"risk,omitempty"`
	ValidationStatus    ValidationStatus    `json:"validation_status"`
	CurrentVersion      string              `json:"current_version,omitempty"`
	Rejections          []StrategyRejection `json:"rejections,omitempty"`
	DependsOn           []string            `json:"depends_on,omitempty"`
}

type Summary struct {
	Exact                  uint32 `json:"exact"`
	Compensating           uint32 `json:"compensating"`
	BestEffort             uint32 `json:"best_effort"`
	Guidance               uint32 `json:"guidance"`
	Irreversible           uint32 `json:"irreversible"`
	ReconciliationRequired uint32 `json:"reconciliation_required"`
	UserApprovals          uint32 `json:"user_approvals"`
	Executable             uint32 `json:"executable"`
}

type Plan struct {
	SchemaVersion string     `json:"schema_version"`
	Identity      string     `json:"identity"`
	EffectGroupID string     `json:"effect_group_id"`
	Mode          DryRunMode `json:"mode"`
	Steps         []Step     `json:"steps"`
	Summary       Summary    `json:"summary"`
	InversePython string     `json:"inverse_python"`
}

// Review proves Agent review of one exact dry-run artifact. Authority and user
// approval remain separate Host-owned digests; review alone grants neither.
type Review struct {
	PlanSHA256         string `json:"plan_sha256"`
	ReviewerRunID      string `json:"reviewer_run_id"`
	AuthoritySHA256    string `json:"authority_sha256"`
	UserApprovalSHA256 string `json:"user_approval_sha256,omitempty"`
}

type ProviderResult struct {
	Outcome       ProviderOutcome `json:"outcome"`
	ReceiptSHA256 string          `json:"receipt_sha256,omitempty"`
}

type CompensationReceipt struct {
	PlanSHA256            string        `json:"plan_sha256"`
	Compensates           string        `json:"compensates"`
	OriginalRunID         string        `json:"original_run_id"`
	StrategyID            string        `json:"strategy_id"`
	Semantics             Semantics     `json:"semantics"`
	Status                ReceiptStatus `json:"status"`
	ReviewerRunID         string        `json:"reviewer_run_id"`
	AuthoritySHA256       string        `json:"authority_sha256"`
	UserApprovalSHA256    string        `json:"user_approval_sha256,omitempty"`
	ProviderReceiptSHA256 string        `json:"provider_receipt_sha256,omitempty"`
}

type ExecutionResult struct {
	PlanSHA256 string                `json:"plan_sha256"`
	Status     ExecutionStatus       `json:"status"`
	Receipts   []CompensationReceipt `json:"receipts"`
}

type Validator interface {
	Validate(context.Context, EffectReceipt, Strategy) (Validation, error)
}

type Executor interface {
	// Execute may return ProviderNotApplied only with adapter evidence that the
	// provider did not commit. Unclassified errors are treated as ambiguous by
	// the controller, not as proof of failure.
	Execute(context.Context, EffectReceipt, Strategy) (ProviderResult, error)
}

// Authorizer validates that fresh Host authority and any required user
// approval are bound to the exact reviewed plan. Digest syntax alone is never
// sufficient authority.
type Authorizer interface {
	AuthorizeCompensation(context.Context, Plan, Review) error
}

type Config struct {
	Contracts  []ToolContract
	Validator  Validator
	Executor   Executor
	Authorizer Authorizer
	MaxPlans   int
	MaxRecords int
}

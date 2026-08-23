package compensation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

const (
	maxEffectsPerPlan        = 1024
	maxStrategies            = 16
	maxContracts             = 1024
	maxDependenciesPerEffect = 64
	maxDependencyEdges       = 4096
	maxPlanBytes             = 8 << 20
	maxNameBytes             = 256
	maxGuidanceBytes         = 4096
)

type storedPlan struct {
	plan        Plan
	receipts    []EffectReceipt
	executing   bool
	stale       bool
	result      *ExecutionResult
	terminalErr error
}

type Controller struct {
	mu                 sync.Mutex
	contracts          map[string]ToolContract
	validator          Validator
	executor           Executor
	authorizer         Authorizer
	plans              map[string]*storedPlan
	journal            []CompensationReceipt
	reservedRecords    int
	inFlightEffects    map[string]struct{}
	compensatedEffects map[string]struct{}
	uncertainEffects   map[string]struct{}
	blockedEffects     map[string]struct{}
	maxPlans           int
	maxRecords         int
}

func NewController(config Config) (*Controller, error) {
	if config.MaxPlans < 1 || config.MaxPlans > 1<<20 || config.MaxRecords < 1 || config.MaxRecords > 1<<22 || len(config.Contracts) == 0 || len(config.Contracts) > maxContracts {
		return nil, ErrInvalidConfig
	}
	contracts := make(map[string]ToolContract, len(config.Contracts))
	for _, contract := range config.Contracts {
		canonical, err := canonicalContract(contract)
		if err != nil {
			return nil, err
		}
		if _, exists := contracts[canonical.Capability]; exists {
			return nil, ErrInvalidContract
		}
		contracts[canonical.Capability] = canonical
	}
	return &Controller{
		contracts: contracts, validator: config.Validator, executor: config.Executor, authorizer: config.Authorizer,
		plans: make(map[string]*storedPlan), inFlightEffects: make(map[string]struct{}), compensatedEffects: make(map[string]struct{}), uncertainEffects: make(map[string]struct{}), blockedEffects: make(map[string]struct{}),
		maxPlans: config.MaxPlans, maxRecords: config.MaxRecords,
	}, nil
}

func canonicalContract(contract ToolContract) (ToolContract, error) {
	if !validName(contract.Capability) || len(contract.Strategies) == 0 || len(contract.Strategies) > maxStrategies || !validOptionalText(contract.ReconciliationGuidance, maxGuidanceBytes) {
		return ToolContract{}, ErrInvalidContract
	}
	canonical := contract
	canonical.Strategies = append([]Strategy(nil), contract.Strategies...)
	seenIDs := make(map[string]struct{}, len(canonical.Strategies))
	seenRanks := make(map[uint16]struct{}, len(canonical.Strategies))
	for _, strategy := range canonical.Strategies {
		if !validName(strategy.ID) || strategy.Rank == 0 || !validApproval(strategy.Approval) ||
			!validOptionalText(strategy.Guidance, maxGuidanceBytes) || !validOptionalText(strategy.Precondition, maxGuidanceBytes) ||
			!validOptionalText(strategy.Risk, maxGuidanceBytes) {
			return ToolContract{}, ErrInvalidContract
		}
		if _, duplicate := seenIDs[strategy.ID]; duplicate {
			return ToolContract{}, ErrInvalidContract
		}
		if _, duplicate := seenRanks[strategy.Rank]; duplicate {
			return ToolContract{}, ErrInvalidContract
		}
		seenIDs[strategy.ID] = struct{}{}
		seenRanks[strategy.Rank] = struct{}{}
		switch strategy.Mode {
		case ModeExecutable:
			if !validName(strategy.Operation) || (strategy.Semantics != SemanticsExact && strategy.Semantics != SemanticsCompensating && strategy.Semantics != SemanticsBestEffort) {
				return ToolContract{}, ErrInvalidContract
			}
		case ModeGuidance:
			if strategy.Operation != "" || strategy.Guidance == "" || strategy.Approval != ApprovalAgentReview || (strategy.Semantics != SemanticsGuidance && strategy.Semantics != SemanticsIrreversible) {
				return ToolContract{}, ErrInvalidContract
			}
		default:
			return ToolContract{}, ErrInvalidContract
		}
	}
	sort.Slice(canonical.Strategies, func(left, right int) bool {
		return canonical.Strategies[left].Rank > canonical.Strategies[right].Rank
	})
	return canonical, nil
}

func (controller *Controller) Preview(ctx context.Context, request PreviewRequest) (Plan, error) {
	if controller == nil || ctx == nil || (request.Mode != DryRunPlan && request.Mode != DryRunValidate) {
		return Plan{}, ErrInvalidRequest
	}
	ordered, err := prepareReceipts(request)
	if err != nil {
		return Plan{}, err
	}
	controller.mu.Lock()
	for _, receipt := range ordered {
		key := effectKey(receipt.RunID, receipt.EffectID)
		if _, uncertain := controller.uncertainEffects[key]; uncertain {
			controller.mu.Unlock()
			return Plan{}, ErrReconciliationRequired
		}
		if _, blocked := controller.blockedEffects[key]; blocked {
			controller.mu.Unlock()
			return Plan{}, ErrEffectBlocked
		}
		if _, compensated := controller.compensatedEffects[key]; compensated {
			controller.mu.Unlock()
			return Plan{}, ErrAlreadyCompensated
		}
		if _, inFlight := controller.inFlightEffects[key]; inFlight {
			controller.mu.Unlock()
			return Plan{}, ErrEffectInProgress
		}
	}
	controller.mu.Unlock()
	steps := make([]Step, 0, len(ordered))
	for _, receipt := range ordered {
		contract, exists := controller.contracts[receipt.Capability]
		if !exists {
			return Plan{}, fmt.Errorf("%w: %s", ErrUnknownCapability, receipt.Capability)
		}
		step, selectErr := controller.selectStep(ctx, request.Mode, receipt, contract)
		if selectErr != nil {
			return Plan{}, selectErr
		}
		steps = append(steps, step)
	}
	plan := Plan{
		SchemaVersion: PlanSchemaVersion, EffectGroupID: request.EffectGroupID, Mode: request.Mode,
		Steps: steps, Summary: summarize(steps), InversePython: renderInversePython(steps),
	}
	identity, err := planIdentity(plan)
	if err != nil {
		return Plan{}, err
	}
	plan.Identity = identity

	controller.mu.Lock()
	defer controller.mu.Unlock()
	if existing, ok := controller.plans[identity]; ok {
		return clonePlan(existing.plan), nil
	}
	if len(controller.plans) >= controller.maxPlans {
		return Plan{}, ErrPlanCapacity
	}
	controller.plans[identity] = &storedPlan{plan: clonePlan(plan), receipts: cloneReceipts(ordered)}
	return clonePlan(plan), nil
}

func (controller *Controller) selectStep(ctx context.Context, mode DryRunMode, receipt EffectReceipt, contract ToolContract) (Step, error) {
	receiptSHA256, err := effectIdentity(receipt)
	if err != nil {
		return Step{}, ErrInvalidRequest
	}
	base := Step{
		EffectID: receipt.EffectID, SourceRunID: receipt.RunID, SourceReceiptSHA256: receiptSHA256, Capability: receipt.Capability,
		DependsOn: append([]string(nil), receipt.DependsOn...),
	}
	if receipt.Outcome == EffectAmbiguous {
		if contract.ReconciliationGuidance == "" {
			return Step{}, fmt.Errorf("%w: %s", ErrReconciliationRequired, receipt.EffectID)
		}
		base.StrategyID = "reconcile_before_compensation"
		base.Semantics = SemanticsUnknown
		base.Mode = ModeGuidance
		base.Approval = ApprovalAgentReview
		base.Guidance = contract.ReconciliationGuidance
		base.ValidationStatus = ValidationReconciliationRequired
		return base, nil
	}
	if mode == DryRunPlan {
		return stepFromStrategy(base, contract.Strategies[0], ValidationNotChecked, "", nil), nil
	}
	if controller.validator == nil {
		return Step{}, ErrValidationFailed
	}
	rejections := make([]StrategyRejection, 0, len(contract.Strategies)-1)
	for _, strategy := range contract.Strategies {
		if strategy.Mode == ModeGuidance {
			return stepFromStrategy(base, strategy, ValidationGuidance, "", rejections), nil
		}
		validation, validateErr := controller.validator.Validate(ctx, receipt, strategy)
		if validateErr != nil {
			return Step{}, fmt.Errorf("%w: %s", ErrValidationFailed, receipt.EffectID)
		}
		if validation.Applicable {
			if !validOptionalText(validation.CurrentVersion, maxNameBytes) {
				return Step{}, ErrValidationFailed
			}
			return stepFromStrategy(base, strategy, ValidationApplicable, validation.CurrentVersion, rejections), nil
		}
		if !validRequiredText(validation.Reason, maxGuidanceBytes) {
			return Step{}, ErrValidationFailed
		}
		rejections = append(rejections, StrategyRejection{StrategyID: strategy.ID, Reason: validation.Reason})
	}
	return Step{}, fmt.Errorf("%w: %s", ErrNoApplicableStrategy, receipt.EffectID)
}

func stepFromStrategy(base Step, strategy Strategy, status ValidationStatus, currentVersion string, rejections []StrategyRejection) Step {
	base.StrategyID = strategy.ID
	base.Semantics = strategy.Semantics
	base.Mode = strategy.Mode
	base.Operation = strategy.Operation
	base.Approval = strategy.Approval
	base.Guidance = strategy.Guidance
	base.Precondition = strategy.Precondition
	base.Risk = strategy.Risk
	base.ValidationStatus = status
	base.CurrentVersion = currentVersion
	base.Rejections = append([]StrategyRejection(nil), rejections...)
	return base
}

func (controller *Controller) Execute(ctx context.Context, plan Plan, review Review) (ExecutionResult, error) {
	if controller == nil || ctx == nil {
		return ExecutionResult{}, ErrReviewMismatch
	}
	identity, err := planIdentity(plan)
	if err != nil || identity != plan.Identity || review.PlanSHA256 != plan.Identity || !validRequiredText(review.ReviewerRunID, maxNameBytes) {
		return ExecutionResult{}, ErrReviewMismatch
	}
	if !validSHA256(review.AuthoritySHA256) {
		return ExecutionResult{}, ErrAuthorityRequired
	}
	if requiresUserApproval(plan.Steps) && !validSHA256(review.UserApprovalSHA256) {
		return ExecutionResult{}, ErrUserApprovalRequired
	}
	for _, step := range plan.Steps {
		if step.SourceRunID == review.ReviewerRunID {
			return ExecutionResult{}, ErrReviewMismatch
		}
	}

	controller.mu.Lock()
	stored, exists := controller.plans[plan.Identity]
	if !exists || stored.plan.Identity != identity {
		controller.mu.Unlock()
		return ExecutionResult{}, ErrReviewMismatch
	}
	if stored.result != nil {
		result, terminalErr := cloneExecutionResult(*stored.result), stored.terminalErr
		controller.mu.Unlock()
		return result, terminalErr
	}
	if stored.stale {
		controller.mu.Unlock()
		return ExecutionResult{}, ErrStalePlan
	}
	if stored.executing {
		controller.mu.Unlock()
		return ExecutionResult{}, ErrPlanInProgress
	}
	for _, step := range stored.plan.Steps {
		key := effectKey(step.SourceRunID, step.EffectID)
		if _, uncertain := controller.uncertainEffects[key]; uncertain {
			controller.mu.Unlock()
			return ExecutionResult{}, ErrReconciliationRequired
		}
		if _, blocked := controller.blockedEffects[key]; blocked {
			controller.mu.Unlock()
			return ExecutionResult{}, ErrEffectBlocked
		}
		if _, compensated := controller.compensatedEffects[key]; compensated {
			controller.mu.Unlock()
			return ExecutionResult{}, ErrAlreadyCompensated
		}
		if _, inFlight := controller.inFlightEffects[key]; inFlight {
			controller.mu.Unlock()
			return ExecutionResult{}, ErrEffectInProgress
		}
	}
	if len(controller.journal)+controller.reservedRecords+len(stored.plan.Steps) > controller.maxRecords {
		controller.mu.Unlock()
		return ExecutionResult{}, ErrJournalCapacity
	}
	stored.executing = true
	controller.reservedRecords += len(stored.plan.Steps)
	for _, step := range stored.plan.Steps {
		controller.inFlightEffects[effectKey(step.SourceRunID, step.EffectID)] = struct{}{}
	}
	storedPlan := clonePlan(stored.plan)
	receipts := cloneReceipts(stored.receipts)
	controller.mu.Unlock()

	if controller.authorizer == nil || controller.authorizer.AuthorizeCompensation(ctx, storedPlan, review) != nil {
		controller.mu.Lock()
		stored.executing = false
		controller.reservedRecords -= len(storedPlan.Steps)
		controller.releaseEffects(storedPlan)
		controller.mu.Unlock()
		return ExecutionResult{}, ErrAuthorityRequired
	}

	if err := controller.revalidate(ctx, storedPlan, receipts); err != nil {
		controller.mu.Lock()
		stored.executing = false
		controller.reservedRecords -= len(storedPlan.Steps)
		controller.releaseEffects(storedPlan)
		if errorsIsStale(err) {
			stored.stale = true
		}
		controller.mu.Unlock()
		return ExecutionResult{}, err
	}

	result, terminalErr := controller.executeSteps(ctx, storedPlan, receipts, review)
	controller.mu.Lock()
	stored.executing = false
	controller.reservedRecords -= len(storedPlan.Steps)
	controller.releaseEffects(storedPlan)
	stored.result = pointerToExecutionResult(cloneExecutionResult(result))
	stored.terminalErr = terminalErr
	controller.journal = append(controller.journal, cloneCompensationReceipts(result.Receipts)...)
	for _, receipt := range result.Receipts {
		if receipt.Status == ReceiptCompensated {
			controller.compensatedEffects[effectKey(receipt.OriginalRunID, receipt.Compensates)] = struct{}{}
		} else if receipt.Status == ReceiptReconciliationNeeded {
			controller.uncertainEffects[effectKey(receipt.OriginalRunID, receipt.Compensates)] = struct{}{}
		} else if receipt.Status == ReceiptBlocked {
			controller.blockedEffects[effectKey(receipt.OriginalRunID, receipt.Compensates)] = struct{}{}
		}
	}
	controller.mu.Unlock()
	return cloneExecutionResult(result), terminalErr
}

func (controller *Controller) revalidate(ctx context.Context, plan Plan, receipts []EffectReceipt) error {
	if controller.validator == nil {
		for _, step := range plan.Steps {
			if step.Mode == ModeExecutable {
				return ErrValidationFailed
			}
		}
		return nil
	}
	for index, step := range plan.Steps {
		if step.Mode != ModeExecutable {
			continue
		}
		strategy, ok := controller.strategy(step.Capability, step.StrategyID)
		if !ok || strategy.Mode != ModeExecutable {
			return ErrReviewMismatch
		}
		validation, err := controller.validator.Validate(ctx, receipts[index], strategy)
		if err != nil {
			return ErrValidationFailed
		}
		if !validation.Applicable {
			return ErrStalePlan
		}
	}
	return nil
}

func (controller *Controller) executeSteps(ctx context.Context, plan Plan, receipts []EffectReceipt, review Review) (ExecutionResult, error) {
	result := ExecutionResult{PlanSHA256: plan.Identity, Status: ExecutionCompensated}
	compensated := 0
	for index, step := range plan.Steps {
		receipt := CompensationReceipt{
			PlanSHA256: plan.Identity, Compensates: step.EffectID, StrategyID: step.StrategyID, Semantics: step.Semantics,
			OriginalRunID: step.SourceRunID,
			ReviewerRunID: review.ReviewerRunID, AuthoritySHA256: review.AuthoritySHA256, UserApprovalSHA256: review.UserApprovalSHA256,
		}
		if step.ValidationStatus == ValidationReconciliationRequired {
			receipt.Status = ReceiptReconciliationNeeded
			result.Receipts = append(result.Receipts, receipt)
			result.Receipts = append(result.Receipts, blockedReceipts(plan, review, index+1)...)
			result.Status = ExecutionReconciliationNeeded
			return result, ErrReconciliationRequired
		}
		if step.Mode == ModeGuidance {
			receipt.Status = ReceiptManualRequired
			result.Receipts = append(result.Receipts, receipt)
			result.Receipts = append(result.Receipts, blockedReceipts(plan, review, index+1)...)
			result.Status = ExecutionManualRequired
			return result, nil
		}
		if controller.executor == nil {
			receipt.Status = ReceiptFailed
			result.Receipts = append(result.Receipts, receipt)
			result.Receipts = append(result.Receipts, blockedReceipts(plan, review, index+1)...)
			result.Status = failedStatus(compensated)
			return result, ErrExecutionFailed
		}
		strategy, ok := controller.strategy(step.Capability, step.StrategyID)
		if !ok {
			receipt.Status = ReceiptFailed
			result.Receipts = append(result.Receipts, receipt)
			result.Receipts = append(result.Receipts, blockedReceipts(plan, review, index+1)...)
			result.Status = failedStatus(compensated)
			return result, ErrExecutionFailed
		}
		providerResult, err := controller.executor.Execute(ctx, receipts[index], strategy)
		if err != nil {
			receipt.Status = ReceiptReconciliationNeeded
			result.Receipts = append(result.Receipts, receipt)
			result.Receipts = append(result.Receipts, blockedReceipts(plan, review, index+1)...)
			result.Status = ExecutionReconciliationNeeded
			return result, ErrReconciliationRequired
		}
		switch providerResult.Outcome {
		case ProviderCompensated:
			if !validSHA256(providerResult.ReceiptSHA256) {
				receipt.Status = ReceiptReconciliationNeeded
				result.Receipts = append(result.Receipts, receipt)
				result.Receipts = append(result.Receipts, blockedReceipts(plan, review, index+1)...)
				result.Status = ExecutionReconciliationNeeded
				return result, ErrReconciliationRequired
			}
			receipt.Status = ReceiptCompensated
			receipt.ProviderReceiptSHA256 = providerResult.ReceiptSHA256
			result.Receipts = append(result.Receipts, receipt)
			compensated++
		case ProviderNotApplied:
			if !validSHA256(providerResult.ReceiptSHA256) {
				receipt.Status = ReceiptReconciliationNeeded
				result.Receipts = append(result.Receipts, receipt)
				result.Receipts = append(result.Receipts, blockedReceipts(plan, review, index+1)...)
				result.Status = ExecutionReconciliationNeeded
				return result, ErrReconciliationRequired
			}
			receipt.Status = ReceiptFailed
			receipt.ProviderReceiptSHA256 = providerResult.ReceiptSHA256
			result.Receipts = append(result.Receipts, receipt)
			result.Receipts = append(result.Receipts, blockedReceipts(plan, review, index+1)...)
			result.Status = failedStatus(compensated)
			return result, ErrExecutionFailed
		case ProviderReconciliationNeeded:
			receipt.Status = ReceiptReconciliationNeeded
			if validSHA256(providerResult.ReceiptSHA256) {
				receipt.ProviderReceiptSHA256 = providerResult.ReceiptSHA256
			}
			result.Receipts = append(result.Receipts, receipt)
			result.Receipts = append(result.Receipts, blockedReceipts(plan, review, index+1)...)
			result.Status = ExecutionReconciliationNeeded
			return result, ErrReconciliationRequired
		default:
			receipt.Status = ReceiptReconciliationNeeded
			result.Receipts = append(result.Receipts, receipt)
			result.Receipts = append(result.Receipts, blockedReceipts(plan, review, index+1)...)
			result.Status = ExecutionReconciliationNeeded
			return result, ErrReconciliationRequired
		}
	}
	return result, nil
}

func (controller *Controller) strategy(capability, strategyID string) (Strategy, bool) {
	contract, ok := controller.contracts[capability]
	if !ok {
		return Strategy{}, false
	}
	for _, strategy := range contract.Strategies {
		if strategy.ID == strategyID {
			return strategy, true
		}
	}
	return Strategy{}, false
}

// releaseEffects is called only while controller.mu is held.
func (controller *Controller) releaseEffects(plan Plan) {
	for _, step := range plan.Steps {
		delete(controller.inFlightEffects, effectKey(step.SourceRunID, step.EffectID))
	}
}

func effectKey(runID, effectID string) string {
	return runID + "\x00" + effectID
}

func (controller *Controller) Snapshot() []CompensationReceipt {
	if controller == nil {
		return nil
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	return cloneCompensationReceipts(controller.journal)
}

func prepareReceipts(request PreviewRequest) ([]EffectReceipt, error) {
	if !validRequiredText(request.EffectGroupID, maxNameBytes) || len(request.Receipts) == 0 || len(request.Receipts) > maxEffectsPerPlan {
		return nil, ErrInvalidRequest
	}
	byID := make(map[string]EffectReceipt, len(request.Receipts))
	indices := make(map[uint32]struct{}, len(request.Receipts))
	resources := make(map[string]struct{}, len(request.Receipts))
	dependentCount := make(map[string]int, len(request.Receipts))
	dependencyEdges := 0
	for _, original := range request.Receipts {
		if len(original.DependsOn) > maxDependenciesPerEffect {
			return nil, ErrInvalidRequest
		}
		dependencyEdges += len(original.DependsOn)
		if dependencyEdges > maxDependencyEdges {
			return nil, ErrInvalidRequest
		}
		receipt := original
		receipt.DependsOn = append([]string(nil), original.DependsOn...)
		sort.Strings(receipt.DependsOn)
		if !validReceipt(receipt, request.EffectGroupID) {
			return nil, ErrInvalidRequest
		}
		if _, duplicate := byID[receipt.EffectID]; duplicate {
			return nil, ErrInvalidRequest
		}
		if _, duplicate := indices[receipt.OperationIndex]; duplicate {
			return nil, ErrInvalidRequest
		}
		if _, duplicate := resources[receipt.TargetID]; duplicate {
			return nil, ErrInvalidRequest
		}
		byID[receipt.EffectID] = receipt
		indices[receipt.OperationIndex] = struct{}{}
		resources[receipt.TargetID] = struct{}{}
		dependentCount[receipt.EffectID] = 0
	}
	for _, receipt := range byID {
		seen := make(map[string]struct{}, len(receipt.DependsOn))
		for _, dependency := range receipt.DependsOn {
			if dependency == receipt.EffectID {
				return nil, ErrInvalidRequest
			}
			if _, duplicate := seen[dependency]; duplicate {
				return nil, ErrInvalidRequest
			}
			if _, exists := byID[dependency]; !exists {
				return nil, ErrInvalidRequest
			}
			seen[dependency] = struct{}{}
			dependentCount[dependency]++
		}
	}
	ordered := make([]EffectReceipt, 0, len(byID))
	used := make(map[string]struct{}, len(byID))
	for len(ordered) < len(byID) {
		var candidate EffectReceipt
		found := false
		for id, receipt := range byID {
			if _, done := used[id]; done || dependentCount[id] != 0 {
				continue
			}
			if !found || receipt.OperationIndex > candidate.OperationIndex {
				candidate = receipt
				found = true
			}
		}
		if !found {
			return nil, ErrInvalidRequest
		}
		ordered = append(ordered, candidate)
		used[candidate.EffectID] = struct{}{}
		for _, dependency := range candidate.DependsOn {
			dependentCount[dependency]--
		}
	}
	return ordered, nil
}

func validReceipt(receipt EffectReceipt, groupID string) bool {
	if !validRequiredText(receipt.EffectID, maxNameBytes) || receipt.EffectGroupID != groupID || !validRequiredText(receipt.RunID, maxNameBytes) || !validName(receipt.Capability) ||
		!validRequiredText(receipt.TargetID, maxNameBytes) || !validOptionalText(receipt.AfterVersion, maxNameBytes) ||
		!validSHA256(receipt.ArgumentsSHA256) {
		return false
	}
	switch receipt.Outcome {
	case EffectApplied:
		return validSHA256(receipt.ResultSHA256)
	case EffectAmbiguous:
		return receipt.ResultSHA256 == "" || validSHA256(receipt.ResultSHA256)
	default:
		return false
	}
}

func effectIdentity(receipt EffectReceipt) (string, error) {
	encoded, err := json.Marshal(struct {
		SchemaVersion string        `json:"schema_version"`
		Receipt       EffectReceipt `json:"receipt"`
	}{SchemaVersion: "pysolate.effect-receipt.v1", Receipt: receipt})
	if err != nil {
		return "", err
	}
	return sha256Identity(encoded), nil
}

func planIdentity(plan Plan) (string, error) {
	canonical := clonePlan(plan)
	canonical.Identity = ""
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	if len(encoded) > maxPlanBytes {
		return "", ErrInvalidRequest
	}
	return sha256Identity(encoded), nil
}

func sha256Identity(value []byte) string {
	digest := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func summarize(steps []Step) Summary {
	var summary Summary
	for _, step := range steps {
		switch step.Semantics {
		case SemanticsExact:
			summary.Exact++
		case SemanticsCompensating:
			summary.Compensating++
		case SemanticsBestEffort:
			summary.BestEffort++
		case SemanticsGuidance:
			summary.Guidance++
		case SemanticsIrreversible:
			summary.Irreversible++
		case SemanticsUnknown:
			summary.ReconciliationRequired++
		}
		if step.Mode == ModeExecutable {
			summary.Executable++
		}
		if step.Approval == ApprovalUserRequired {
			summary.UserApprovals++
		}
	}
	return summary
}

func renderInversePython(steps []Step) string {
	var builder strings.Builder
	builder.WriteString("compensation_plan = [\n")
	for _, step := range steps {
		mode := "guide"
		if step.Mode == ModeExecutable {
			mode = "apply"
		}
		fmt.Fprintf(&builder, "    {\"effect\": %q, \"strategy\": %q, \"mode\": %q},\n", step.EffectID, step.StrategyID, mode)
	}
	builder.WriteString("]\n")
	return builder.String()
}

func requiresUserApproval(steps []Step) bool {
	for _, step := range steps {
		if step.Approval == ApprovalUserRequired {
			return true
		}
	}
	return false
}

func failedStatus(compensated int) ExecutionStatus {
	if compensated > 0 {
		return ExecutionPartiallyCompensated
	}
	return ExecutionFailed
}

func blockedReceipts(plan Plan, review Review, start int) []CompensationReceipt {
	blocked := make([]CompensationReceipt, 0, len(plan.Steps)-start)
	for _, step := range plan.Steps[start:] {
		blocked = append(blocked, CompensationReceipt{
			PlanSHA256: plan.Identity, Compensates: step.EffectID, StrategyID: step.StrategyID,
			OriginalRunID: step.SourceRunID, Semantics: step.Semantics, Status: ReceiptBlocked, ReviewerRunID: review.ReviewerRunID,
			AuthoritySHA256: review.AuthoritySHA256, UserApprovalSHA256: review.UserApprovalSHA256,
		})
	}
	return blocked
}

func errorsIsStale(err error) bool {
	return err == ErrStalePlan
}

func validApproval(value Approval) bool {
	return value == ApprovalAgentReview || value == ApprovalUserRequired
}

func validName(value string) bool {
	if !validRequiredText(value, maxNameBytes) {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune("._:-/", character) {
			continue
		}
		return false
	}
	return true
}

func validRequiredText(value string, maxBytes int) bool {
	return value != "" && len(value) <= maxBytes && utf8.ValidString(value)
}

func validOptionalText(value string, maxBytes int) bool {
	return len(value) <= maxBytes && utf8.ValidString(value)
}

func validSHA256(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func clonePlan(plan Plan) Plan {
	cloned := plan
	cloned.Steps = make([]Step, len(plan.Steps))
	for index, step := range plan.Steps {
		cloned.Steps[index] = step
		cloned.Steps[index].DependsOn = append([]string(nil), step.DependsOn...)
		cloned.Steps[index].Rejections = append([]StrategyRejection(nil), step.Rejections...)
	}
	return cloned
}

func cloneReceipts(receipts []EffectReceipt) []EffectReceipt {
	cloned := make([]EffectReceipt, len(receipts))
	for index, receipt := range receipts {
		cloned[index] = receipt
		cloned[index].DependsOn = append([]string(nil), receipt.DependsOn...)
	}
	return cloned
}

func cloneCompensationReceipts(receipts []CompensationReceipt) []CompensationReceipt {
	return append([]CompensationReceipt(nil), receipts...)
}

func cloneExecutionResult(result ExecutionResult) ExecutionResult {
	result.Receipts = cloneCompensationReceipts(result.Receipts)
	return result
}

func pointerToExecutionResult(result ExecutionResult) *ExecutionResult {
	return &result
}

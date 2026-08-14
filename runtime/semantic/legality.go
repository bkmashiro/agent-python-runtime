package semantic

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"sort"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

// RejectionReason is a stable fail-closed explanation emitted by every shared
// legality predicate. Consumers may filter or count reasons but may not reinterpret
// an unknown reason as authority.
type RejectionReason string

const (
	RejectUnverifiedAnalysis          RejectionReason = "unverified_analysis"
	RejectCallSiteMissing             RejectionReason = "call_site_missing"
	RejectCallNotNecessarilyReached   RejectionReason = "call_not_necessarily_reached"
	RejectCapabilityPlanMissing       RejectionReason = "capability_plan_missing"
	RejectCapabilityPlanMismatch      RejectionReason = "capability_plan_mismatch"
	RejectCapabilityUnqualified       RejectionReason = "capability_unqualified"
	RejectObservationBindingMissing   RejectionReason = "observation_binding_missing"
	RejectCanonicalArgumentsInvalid   RejectionReason = "canonical_arguments_invalid"
	RejectResourceArgumentMissing     RejectionReason = "resource_argument_missing"
	RejectFrozenContextInvalid        RejectionReason = "frozen_context_invalid"
	RejectSpeculationBudgetExhausted  RejectionReason = "speculation_budget_exhausted"
	RejectQualifiedCallInvalid        RejectionReason = "qualified_call_invalid"
	RejectObservationIdentityMismatch RejectionReason = "observation_identity_mismatch"
	RejectObservationNotReady         RejectionReason = "observation_not_ready"
	RejectCoalescingContractMissing   RejectionReason = "coalescing_contract_missing"
	RejectCacheContractMissing        RejectionReason = "cache_contract_missing"
	RejectBackendContractMissing      RejectionReason = "backend_contract_missing"
)

// Decision is immutable to callers. A consumer receives authority only when Allowed
// is true and a predicate-specific proof object is also present.
type Decision struct {
	allowed    bool
	rejections []RejectionReason
	call       *QualifiedCall
}

func (decision Decision) Allowed() bool { return decision.allowed }

func (decision Decision) Rejections() []RejectionReason {
	return append([]RejectionReason(nil), decision.rejections...)
}

func (decision Decision) QualifiedCall() (QualifiedCall, bool) {
	if !decision.allowed || decision.call == nil || !decision.call.valid() {
		return QualifiedCall{}, false
	}
	return decision.call.clone(), true
}

// PreissueContext is Host-authored per-Run state. It contains identity and budget
// snapshot facts only; it carries no provider handle or executable authority. The
// reservation identity must be minted by a Host budget manager, and Track E must
// consume it atomically before starting physical work.
type PreissueContext struct {
	StreamEpoch             string
	WorkflowEpoch           string
	FreshnessEpoch          string
	ExpiryEpoch             string
	PrivacyPartition        string
	ParentLineageSHA256     string
	BudgetReservationSHA256 string
	RemainingPhysicalReads  uint32
}

var legalityTokenPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)

func (context PreissueContext) valid() bool {
	return legalityTokenPattern.MatchString(context.StreamEpoch) &&
		legalityTokenPattern.MatchString(context.WorkflowEpoch) &&
		legalityTokenPattern.MatchString(context.FreshnessEpoch) &&
		legalityTokenPattern.MatchString(context.ExpiryEpoch) &&
		legalityTokenPattern.MatchString(context.PrivacyPartition) &&
		digestPattern.MatchString(context.ParentLineageSHA256) &&
		digestPattern.MatchString(context.BudgetReservationSHA256)
}

// QualifiedCall is an opaque proof that one exact verified source occurrence joined
// successfully with one sealed Host capability contract and frozen per-Run context.
type QualifiedCall struct {
	callSiteID              string
	sourceSHA256            string
	capability              string
	canonicalArguments      []byte
	argumentsSHA256         string
	resourceSHA256          string
	dynamicOccurrence       uint32
	binding                 capability.StreamingObservationBinding
	streamEpoch             string
	workflowEpoch           string
	freshnessEpoch          string
	expiryEpoch             string
	privacyPartition        string
	parentLineageSHA256     string
	budgetReservationSHA256 string
}

func (call QualifiedCall) CallSiteID() string      { return call.callSiteID }
func (call QualifiedCall) SourceSHA256() string    { return call.sourceSHA256 }
func (call QualifiedCall) Capability() string      { return call.capability }
func (call QualifiedCall) ArgumentsSHA256() string { return call.argumentsSHA256 }
func (call QualifiedCall) ResourceSHA256() string  { return call.resourceSHA256 }

func (call QualifiedCall) CanonicalArguments() json.RawMessage {
	return append(json.RawMessage(nil), call.canonicalArguments...)
}

func (call QualifiedCall) valid() bool {
	return digestPattern.MatchString(call.callSiteID) && digestPattern.MatchString(call.sourceSHA256) &&
		capabilityPattern.MatchString(call.capability) && len(call.canonicalArguments) != 0 &&
		digestPattern.MatchString(call.argumentsSHA256) && digestPattern.MatchString(call.resourceSHA256) &&
		call.dynamicOccurrence == 1 && call.binding.Capability == call.capability &&
		digestPattern.MatchString(call.binding.SpecSHA256) && legalityTokenPattern.MatchString(call.binding.HandlerIdentity) &&
		digestPattern.MatchString(call.binding.PlanSHA256) && digestPattern.MatchString(call.binding.GrantPolicySHA256) &&
		legalityTokenPattern.MatchString(call.streamEpoch) && legalityTokenPattern.MatchString(call.workflowEpoch) &&
		legalityTokenPattern.MatchString(call.freshnessEpoch) && legalityTokenPattern.MatchString(call.expiryEpoch) &&
		legalityTokenPattern.MatchString(call.privacyPartition) && digestPattern.MatchString(call.parentLineageSHA256) &&
		digestPattern.MatchString(call.budgetReservationSHA256)
}

func (call QualifiedCall) clone() QualifiedCall {
	call.canonicalArguments = append([]byte(nil), call.canonicalArguments...)
	return call
}

// ObservationClaim is the body-free identity presented at the unchanged dynamic
// Python Host-call boundary. Ready is state, not identity. This pure predicate does
// not mutate one-shot state; the Track E consumer must claim atomically.
type ObservationClaim struct {
	CallSiteID              string
	SourceSHA256            string
	DynamicOccurrence       uint32
	ArgumentsSHA256         string
	Capability              string
	SpecSHA256              string
	HandlerIdentity         string
	PlanSHA256              string
	GrantPolicySHA256       string
	StreamEpoch             string
	WorkflowEpoch           string
	FreshnessEpoch          string
	ExpiryEpoch             string
	PrivacyPartition        string
	ParentLineageSHA256     string
	BudgetReservationSHA256 string
	Ready                   bool
}

func (call QualifiedCall) ExpectedObservationClaim() ObservationClaim {
	return ObservationClaim{
		CallSiteID: call.callSiteID, SourceSHA256: call.sourceSHA256,
		DynamicOccurrence: call.dynamicOccurrence, ArgumentsSHA256: call.argumentsSHA256,
		Capability: call.capability, SpecSHA256: call.binding.SpecSHA256,
		HandlerIdentity: call.binding.HandlerIdentity, PlanSHA256: call.binding.PlanSHA256,
		GrantPolicySHA256: call.binding.GrantPolicySHA256, StreamEpoch: call.streamEpoch,
		WorkflowEpoch: call.workflowEpoch, FreshnessEpoch: call.freshnessEpoch,
		ExpiryEpoch: call.expiryEpoch, PrivacyPartition: call.privacyPartition,
		ParentLineageSHA256: call.parentLineageSHA256, Ready: true,
		BudgetReservationSHA256: call.budgetReservationSHA256,
	}
}

// CanPreissue is the only v0 join between verified Guest occurrence facts and the
// sealed Host capability contract. It starts no work and has no side effects.
func CanPreissue(verified VerifiedAnalysis, plan *capability.Plan, callSiteID string, context PreissueContext) Decision {
	analysis, err := verified.Analysis()
	if err != nil {
		return rejected(RejectUnverifiedAnalysis)
	}
	if plan == nil {
		return rejected(RejectCapabilityPlanMissing)
	}
	reasons := make([]RejectionReason, 0, 4)
	if analysis.CapabilityPlanSHA256 != plan.Identity() {
		reasons = append(reasons, RejectCapabilityPlanMismatch)
	}
	site, found := findCallSite(analysis.CallSites, callSiteID)
	if !found {
		reasons = append(reasons, RejectCallSiteMissing)
	}
	if found && !site.NecessarilyReached {
		reasons = append(reasons, RejectCallNotNecessarilyReached)
	}
	if !context.valid() {
		reasons = append(reasons, RejectFrozenContextInvalid)
	}
	if context.RemainingPhysicalReads == 0 {
		reasons = append(reasons, RejectSpeculationBudgetExhausted)
	}
	if !found {
		return rejected(reasons...)
	}
	qualification, qualified := plan.PreDispatch(site.Capability)
	if !qualified || !qualification.Eligible() {
		reasons = append(reasons, RejectCapabilityUnqualified)
	}
	binding, bound := plan.StreamingObservationBinding(site.Capability)
	if !bound {
		reasons = append(reasons, RejectObservationBindingMissing)
	}
	if !site.ArgumentsCanonical || !validCanonicalArguments(site.CanonicalArguments) {
		reasons = append(reasons, RejectCanonicalArgumentsInvalid)
	}
	var resourceSHA string
	if qualified && site.ArgumentsCanonical {
		resourceSHA, err = resourceIdentity(qualification.Contract().Resource, site.CanonicalArguments)
		if err != nil {
			reasons = append(reasons, RejectResourceArgumentMissing)
		}
	}
	if len(reasons) != 0 {
		return rejected(reasons...)
	}
	argumentsDigest := sha256.Sum256(site.CanonicalArguments)
	call := QualifiedCall{
		callSiteID: site.ID, sourceSHA256: analysis.SourceSHA256, capability: site.Capability,
		canonicalArguments: append([]byte(nil), site.CanonicalArguments...),
		argumentsSHA256:    "sha256:" + hex.EncodeToString(argumentsDigest[:]), resourceSHA256: resourceSHA,
		dynamicOccurrence: site.DynamicOccurrence, binding: binding,
		streamEpoch: context.StreamEpoch, workflowEpoch: context.WorkflowEpoch,
		freshnessEpoch: context.FreshnessEpoch, expiryEpoch: context.ExpiryEpoch,
		privacyPartition: context.PrivacyPartition, parentLineageSHA256: context.ParentLineageSHA256,
		budgetReservationSHA256: context.BudgetReservationSHA256,
	}
	if !call.valid() {
		return rejected(RejectQualifiedCallInvalid)
	}
	return Decision{allowed: true, call: &call}
}

func (call QualifiedCall) ClaimIdentitySHA256() string {
	if !call.valid() {
		return ""
	}
	encoded, err := json.Marshal(call.ExpectedObservationClaim())
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(append([]byte("pysolate.observation-claim.v0\x00"), encoded...))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func CanClaimStagedObservation(call QualifiedCall, claim ObservationClaim) Decision {
	if !call.valid() {
		return rejected(RejectQualifiedCallInvalid)
	}
	if !claim.Ready {
		return rejected(RejectObservationNotReady)
	}
	expected := call.ExpectedObservationClaim()
	if claim != expected {
		return rejected(RejectObservationIdentityMismatch)
	}
	return Decision{allowed: true}
}

// Coalescing and durable caching are explicit shared questions, but capability-plan
// v5 intentionally carries neither contract. They therefore fail closed rather than
// inferring shareability from read-only/idempotent metadata.
func CanCoalesce(call QualifiedCall) Decision {
	if !call.valid() {
		return rejected(RejectQualifiedCallInvalid)
	}
	return rejected(RejectCoalescingContractMissing)
}

func CanCache(call QualifiedCall) Decision {
	if !call.valid() {
		return rejected(RejectQualifiedCallInvalid)
	}
	return rejected(RejectCacheContractMissing)
}

type BackendRequirement string

const (
	BackendUnknown  BackendRequirement = "unknown"
	BackendPysolate BackendRequirement = "pysolate"
	BackendNative   BackendRequirement = "native"
)

type BackendDecision struct {
	Backend  BackendRequirement
	Decision Decision
}

// RequiredBackend does not infer placement from capability name or effect class.
// Overlay v0 has no canonical backend requirement, so every verified program remains
// unknown until Track G measures and adds a Host-owned contract.
func RequiredBackend(verified VerifiedAnalysis) BackendDecision {
	if _, err := verified.Analysis(); err != nil {
		return BackendDecision{Backend: BackendUnknown, Decision: rejected(RejectUnverifiedAnalysis)}
	}
	return BackendDecision{Backend: BackendUnknown, Decision: rejected(RejectBackendContractMissing)}
}

func findCallSite(sites []CallSite, id string) (CallSite, bool) {
	index := sort.Search(len(sites), func(index int) bool { return sites[index].ID >= id })
	if index == len(sites) || sites[index].ID != id {
		return CallSite{}, false
	}
	return sites[index], true
}

func resourceIdentity(reference capability.ResourceReference, raw json.RawMessage) (string, error) {
	var arguments map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&arguments); err != nil {
		return "", err
	}
	var key []byte
	if reference.Argument != "" {
		value, ok := arguments[reference.Argument]
		if !ok {
			return "", ErrInvalidAnalysis
		}
		key = value
	} else {
		key, _ = json.Marshal(reference.Constant)
	}
	digest := sha256.Sum256(append(append([]byte("pysolate.logical-read-resource.v0\x00"+reference.Namespace+"\x00"), key...), 0))
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func rejected(reasons ...RejectionReason) Decision {
	unique := make(map[RejectionReason]struct{}, len(reasons))
	filtered := make([]RejectionReason, 0, len(reasons))
	for _, reason := range reasons {
		if reason == "" {
			continue
		}
		if _, exists := unique[reason]; exists {
			continue
		}
		unique[reason] = struct{}{}
		filtered = append(filtered, reason)
	}
	sort.Slice(filtered, func(i, j int) bool { return filtered[i] < filtered[j] })
	return Decision{rejections: filtered}
}

package prepareddataset

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

var (
	ErrNoPreparedContract = errors.New("no prepared-data contract")
	ErrDecisionRejected   = errors.New("prepared-data decision rejected")
	ErrClaimMismatch      = errors.New("sealed final source does not preserve prepared occurrence")
)

// DecisionContext is Host-observed state that must equal the sealed contract.
// It is not populated from Python metadata or syntax facts.
type DecisionContext struct {
	WorkspaceRootSHA256     string
	FileSHA256              string
	BodySHA256              string
	ArtifactSHA256          string
	ExecutionProfileSHA256  string
	ImportClosureSHA256     string
	RunIdentity             string
	PrivacyPartition        string
	BudgetReservationSHA256 string
	FileBytes               uint64
	BodyBytes               uint64
	CostUnits               uint32
}

type PreparationDecision struct {
	Allowed             bool
	PreparationIdentity string
	ContractIdentity    string
	facts               NumpyLoadFacts
	contract            PreparedDataContract
}

// ClaimDecision is the only result that contains a sealed final-source digest.
// The digest is deliberately excluded from PreparationIdentity.
type ClaimDecision struct {
	Allowed             bool
	PreparationIdentity string
	ClaimIdentity       string
	FinalSourceSHA256   string
	CallSite            semantic.CallSite
}

func Decide(contract *PreparedDataContract, facts NumpyLoadFacts, plan *capability.Plan, context DecisionContext) (PreparationDecision, error) {
	if contract == nil || !contract.Valid() {
		return PreparationDecision{}, ErrNoPreparedContract
	}
	if err := facts.Validate(); err != nil {
		return PreparationDecision{}, ErrDecisionRejected
	}
	declaration := contract.declaration
	if declaration.StreamEpoch != facts.StreamEpoch || declaration.AdmittedPrefixSHA256 != facts.AdmittedPrefixSHA256 ||
		declaration.Span != facts.CallSite.Span || declaration.DynamicOccurrence != facts.CallSite.DynamicOccurrence ||
		!bytes.Equal(declaration.CanonicalArguments, facts.CallSite.CanonicalArguments) {
		return PreparationDecision{}, ErrDecisionRejected
	}
	if err := validateDecisionContext(declaration, context); err != nil {
		return PreparationDecision{}, err
	}
	if plan == nil || plan.Identity() == "" || plan.Identity() != declaration.CapabilityPlanSHA256 {
		return PreparationDecision{}, ErrDecisionRejected
	}
	qualification, ok := plan.PreDispatch(declaration.Capability)
	if !ok || !qualification.Eligible() {
		return PreparationDecision{}, ErrDecisionRejected
	}
	preDispatch := qualification.Contract()
	if preDispatch.Resource.Namespace != PreparedResourceNamespace || preDispatch.Resource.Argument != "path" || preDispatch.Resource.Constant != "" ||
		preDispatch.Freshness != declaration.Freshness || preDispatch.Unclaimed != declaration.Unclaimed ||
		preDispatch.Privacy != capability.PreDispatchPrivacyExactPartition || preDispatch.Coalescing != capability.PreDispatchCoalescingForbidden ||
		preDispatch.MaxResultBytes != declaration.MaxResultBytes || preDispatch.CostUnits != declaration.CostUnits {
		return PreparationDecision{}, ErrDecisionRejected
	}
	identity, err := preparationIdentity(declaration, facts.CallSite)
	if err != nil {
		return PreparationDecision{}, ErrDecisionRejected
	}
	return PreparationDecision{Allowed: true, PreparationIdentity: identity, ContractIdentity: contract.Identity(), facts: facts, contract: *contract}, nil
}

func validateDecisionContext(declaration HostPreparedDataDeclaration, context DecisionContext) error {
	if !validDigest(context.WorkspaceRootSHA256) || !validDigest(context.FileSHA256) || !validDigest(context.BodySHA256) ||
		!validDigest(context.ArtifactSHA256) || !validDigest(context.ExecutionProfileSHA256) || !validDigest(context.ImportClosureSHA256) ||
		!validIdentity(context.RunIdentity) || !validIdentity(context.PrivacyPartition) || !validDigest(context.BudgetReservationSHA256) ||
		context.FileBytes == 0 || context.BodyBytes == 0 || context.CostUnits == 0 {
		return ErrDecisionRejected
	}
	if context.WorkspaceRootSHA256 != declaration.WorkspaceRootSHA256 || context.FileSHA256 != declaration.FileSHA256 ||
		context.BodySHA256 != declaration.BodySHA256 || context.ArtifactSHA256 != declaration.ArtifactSHA256 ||
		context.ExecutionProfileSHA256 != declaration.ExecutionProfileSHA256 || context.ImportClosureSHA256 != declaration.ImportClosureSHA256 ||
		context.RunIdentity != declaration.RunIdentity || context.PrivacyPartition != declaration.PrivacyPartition ||
		context.BudgetReservationSHA256 != declaration.BudgetReservationSHA256 || context.FileBytes != declaration.MaxFileBytes ||
		context.BodyBytes != declaration.MaxBodyBytes || context.CostUnits != declaration.CostUnits {
		return ErrDecisionRejected
	}
	return nil
}

func preparationIdentity(declaration HostPreparedDataDeclaration, callSite semantic.CallSite) (string, error) {
	payload := struct {
		SchemaVersion        string              `json:"schema_version"`
		ContractIdentity     string              `json:"contract_identity"`
		StreamEpoch          string              `json:"stream_epoch"`
		AdmittedPrefixSHA256 string              `json:"admitted_prefix_sha256"`
		Span                 semantic.SourceSpan `json:"span"`
		CanonicalArguments   json.RawMessage     `json:"canonical_arguments"`
		DynamicOccurrence    uint32              `json:"dynamic_occurrence"`
	}{
		SchemaVersion: ContractSchemaVersion, ContractIdentity: digestDeclaration(declaration), StreamEpoch: declaration.StreamEpoch,
		AdmittedPrefixSHA256: declaration.AdmittedPrefixSHA256, Span: callSite.Span,
		CanonicalArguments: append(json.RawMessage(nil), callSite.CanonicalArguments...), DynamicOccurrence: callSite.DynamicOccurrence,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return digestBytes(encoded), nil
}

func digestDeclaration(declaration HostPreparedDataDeclaration) string {
	encoded, _ := json.Marshal(declaration)
	return digestBytes(encoded)
}

func (decision PreparationDecision) PhysicalCallSite() (semantic.CallSite, error) {
	if !decision.Allowed || decision.PreparationIdentity == "" || decision.contract.Identity() == "" {
		return semantic.CallSite{}, ErrDecisionRejected
	}
	callSite := decision.facts.CallSite
	callSite.Capability = decision.contract.declaration.Capability
	arguments, err := json.Marshal(map[string]any{"path": decision.contract.declaration.ResourcePath})
	if err != nil {
		return semantic.CallSite{}, ErrDecisionRejected
	}
	callSite.CanonicalArguments = arguments
	return callSite, nil
}

func (decision PreparationDecision) Claim(finalSource string, verified semantic.VerifiedAnalysis) (ClaimDecision, error) {
	analysis, err := verified.Analysis()
	overlay, overlayErr := analysisOverlay(finalSource)
	if err != nil || overlayErr != nil || analysis.SourceSHA256 != digestText(overlay) {
		return ClaimDecision{}, ErrClaimMismatch
	}
	return decision.claimWithCallSites(finalSource, analysis.CallSites)
}

func (decision PreparationDecision) claimWithCallSites(finalSource string, callSites []semantic.CallSite) (ClaimDecision, error) {
	if !decision.Allowed || decision.contract.Identity() == "" || finalSource == "" ||
		len(finalSource) <= len(decision.facts.SourcePrefix) || !strings.HasPrefix(finalSource, decision.facts.SourcePrefix) {
		return ClaimDecision{}, ErrClaimMismatch
	}
	finalDigest := digestText(finalSource)
	finalFacts, err := factsFromCallSites(finalSource, decision.facts.StreamEpoch, finalDigest, callSites)
	if err != nil || !sameOccurrence(finalFacts.CallSite, decision.facts.CallSite) {
		return ClaimDecision{}, ErrClaimMismatch
	}
	claimPayload := struct {
		SchemaVersion       string `json:"schema_version"`
		PreparationIdentity string `json:"preparation_identity"`
		FinalSourceSHA256   string `json:"final_source_sha256"`
	}{ContractSchemaVersion, decision.PreparationIdentity, finalDigest}
	encoded, err := json.Marshal(claimPayload)
	if err != nil {
		return ClaimDecision{}, ErrClaimMismatch
	}
	return ClaimDecision{Allowed: true, PreparationIdentity: decision.PreparationIdentity, ClaimIdentity: digestBytes(encoded), FinalSourceSHA256: finalDigest, CallSite: finalFacts.CallSite}, nil
}

func sameOccurrence(left, right semantic.CallSite) bool {
	return left.Span == right.Span && left.Capability == right.Capability &&
		left.DynamicOccurrence == right.DynamicOccurrence && left.NecessarilyReached == right.NecessarilyReached &&
		left.ArgumentsCanonical && right.ArgumentsCanonical && bytes.Equal(left.CanonicalArguments, right.CanonicalArguments)
}

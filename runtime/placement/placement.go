package placement

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"

	runtimeconfig "github.com/bkmashiro/agent-python-runtime/runtime"
)

const (
	DecisionSchemaVersion = "pysolate.placement-decision.v1"
	AnalyzerStaticV1      = "static-v1"
)

var (
	ErrInvalidPolicy      = errors.New("invalid placement policy")
	ErrBackendUnavailable = errors.New("selected execution backend unavailable")
)

type Status string

const (
	StatusSelected    Status = "selected"
	StatusUnavailable Status = "unavailable"
)

type Reason string

const (
	ReasonQualifiedPlainShard   Reason = "qualified_plain_shard"
	ReasonRequiredNativeFeature Reason = "required_native_feature"
	ReasonNoQualifiedShard      Reason = "no_qualified_shard"
	ReasonSourceIndeterminate   Reason = "source_indeterminate"
	ReasonNativeStateDependency Reason = "native_state_dependency"
	ReasonModelRiskSignal       Reason = "model_risk_signal"
	ReasonNativeUnavailable     Reason = "native_unavailable"
	ReasonPysolateUnavailable   Reason = "pysolate_unavailable"
	ReasonL2NotStartedPromotion Reason = "l2_not_started_promotion"
)

type Policy struct {
	AnalyzerVersion   string
	PysolateAvailable bool
	NativeAvailable   bool
	PlainShard        runtimeconfig.ShardProfile
}

type Decision struct {
	SchemaVersion    string                         `json:"schema_version"`
	Status           Status                         `json:"status"`
	Backend          runtimeconfig.ExecutionBackend `json:"backend,omitempty"`
	Reason           Reason                         `json:"reason"`
	AnalyzerVersion  string                         `json:"analyzer_version"`
	RequestSHA256    string                         `json:"request_sha256"`
	ShardID          string                         `json:"shard_id,omitempty"`
	ShardSHA256      string                         `json:"shard_sha256,omitempty"`
	StateClass       runtimeconfig.NativeStateClass `json:"state_class"`
	ParentDecisionID string                         `json:"parent_decision_id,omitempty"`
	Identity         string                         `json:"identity"`
}

type decisionIdentity struct {
	SchemaVersion    string                         `json:"schema_version"`
	Status           Status                         `json:"status"`
	Backend          runtimeconfig.ExecutionBackend `json:"backend,omitempty"`
	Reason           Reason                         `json:"reason"`
	AnalyzerVersion  string                         `json:"analyzer_version"`
	RequestSHA256    string                         `json:"request_sha256"`
	ShardID          string                         `json:"shard_id,omitempty"`
	ShardSHA256      string                         `json:"shard_sha256,omitempty"`
	StateClass       runtimeconfig.NativeStateClass `json:"state_class"`
	ParentDecisionID string                         `json:"parent_decision_id,omitempty"`
}

func Analyze(request runtimeconfig.RunRequest, state runtimeconfig.NativeStateClass, modelRisk bool, policy Policy) (Decision, error) {
	if policy.AnalyzerVersion != AnalyzerStaticV1 || policy.PlainShard.ID() != "plain" || policy.PlainShard.Identity() == "" || !state.Valid() {
		return Decision{}, ErrInvalidPolicy
	}
	requestDigest, err := runtimeconfig.RunRequestSHA256(request)
	if err != nil {
		return Decision{}, err
	}
	nativeReason := Reason("")
	if state != runtimeconfig.StatePortableValue {
		nativeReason = ReasonNativeStateDependency
	} else if modelRisk {
		nativeReason = ReasonModelRiskSignal
	} else if len(request.Requirements) != 0 {
		if err := runtimeconfig.ValidateRunRequirements(request.Requirements); err != nil {
			return Decision{}, err
		}
		nativeReason = ReasonRequiredNativeFeature
	} else {
		imports, inferErr := runtimeconfig.InferStaticImportRoots(request.Code)
		if inferErr != nil {
			nativeReason = ReasonSourceIndeterminate
		} else if !importsQualified(imports, policy.PlainShard.QualifiedImports()) ||
			(request.Compatibility != nil && (request.Compatibility.Profile != policy.PlainShard.ExecutionProfileID() || !sameStrings(imports, request.Compatibility.Imports))) {
			nativeReason = ReasonNoQualifiedShard
		}
	}
	if nativeReason != "" {
		if !policy.NativeAvailable {
			return makeDecision(StatusUnavailable, "", ReasonNativeUnavailable, requestDigest, state, policy, ""), nil
		}
		return makeDecision(StatusSelected, runtimeconfig.BackendNativeSandbox, nativeReason, requestDigest, state, policy, ""), nil
	}
	if !policy.PysolateAvailable {
		if policy.NativeAvailable {
			return makeDecision(StatusSelected, runtimeconfig.BackendNativeSandbox, ReasonPysolateUnavailable, requestDigest, state, policy, ""), nil
		}
		return makeDecision(StatusUnavailable, "", ReasonNativeUnavailable, requestDigest, state, policy, ""), nil
	}
	return makeDecision(StatusSelected, runtimeconfig.BackendPysolateWASM, ReasonQualifiedPlainShard, requestDigest, state, policy, ""), nil
}

func importsQualified(imports, qualified []string) bool {
	allowed := make(map[string]struct{}, len(qualified))
	for _, module := range qualified {
		allowed[module] = struct{}{}
	}
	for _, module := range imports {
		if _, ok := allowed[module]; !ok {
			return false
		}
	}
	return true
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	copyRight := append([]string(nil), right...)
	sort.Strings(copyRight)
	for index := range left {
		if left[index] != copyRight[index] {
			return false
		}
	}
	return true
}

func makeDecision(status Status, backend runtimeconfig.ExecutionBackend, reason Reason, requestDigest string, state runtimeconfig.NativeStateClass, policy Policy, parent string) Decision {
	identityDoc := decisionIdentity{DecisionSchemaVersion, status, backend, reason, policy.AnalyzerVersion, requestDigest, "", "", state, parent}
	if backend == runtimeconfig.BackendPysolateWASM {
		identityDoc.ShardID = policy.PlainShard.ID()
		identityDoc.ShardSHA256 = policy.PlainShard.Identity()
	}
	encoded, _ := json.Marshal(identityDoc)
	digest := sha256.Sum256(encoded)
	return Decision{
		SchemaVersion: identityDoc.SchemaVersion, Status: status, Backend: backend, Reason: reason,
		AnalyzerVersion: policy.AnalyzerVersion, RequestSHA256: requestDigest, ShardID: identityDoc.ShardID,
		ShardSHA256: identityDoc.ShardSHA256, StateClass: state, ParentDecisionID: parent,
		Identity: "sha256:" + hex.EncodeToString(digest[:]),
	}
}

type Plan struct {
	Decision         Decision
	ParentDecisionID string
}

type Backend interface {
	Execute(context.Context, Plan, []byte) ([]byte, error)
}

type Orchestrator struct {
	Policy   Policy
	Pysolate Backend
	Native   Backend
}

type Result struct {
	Payload   []byte
	Decision  Decision
	Promotion *runtimeconfig.ExecutionOutcome
}

func (orchestrator Orchestrator) Execute(ctx context.Context, raw []byte, state runtimeconfig.NativeStateClass, modelRisk bool) (Result, error) {
	if ctx == nil {
		return Result{}, errors.New("nil execution context")
	}
	request, err := runtimeconfig.DecodeRunRequest(raw)
	if err != nil {
		return Result{}, err
	}
	decision, err := Analyze(request, state, modelRisk, orchestrator.Policy)
	if err != nil {
		return Result{}, err
	}
	if decision.Status == StatusUnavailable {
		return Result{Decision: decision}, ErrBackendUnavailable
	}
	selected := orchestrator.Pysolate
	if decision.Backend == runtimeconfig.BackendNativeSandbox {
		selected = orchestrator.Native
	}
	if selected == nil {
		return Result{Decision: decision}, ErrBackendUnavailable
	}
	payload, executeErr := selected.Execute(ctx, Plan{Decision: decision}, raw)
	if executeErr == nil {
		return Result{Payload: append([]byte(nil), payload...), Decision: decision}, nil
	}
	if decision.Backend != runtimeconfig.BackendPysolateWASM {
		return Result{Decision: decision}, executeErr
	}
	var unsupported *runtimeconfig.UnsupportedRunError
	if !errors.As(executeErr, &unsupported) {
		return Result{Decision: decision}, executeErr
	}
	outcome, outcomeErr := runtimeconfig.NewUnsupportedOutcome(raw, executeErr)
	if outcomeErr != nil || outcome.Validate() != nil || orchestrator.Native == nil || !orchestrator.Policy.NativeAvailable {
		return Result{Decision: decision}, executeErr
	}
	child := makeDecision(StatusSelected, runtimeconfig.BackendNativeSandbox, ReasonL2NotStartedPromotion, decision.RequestSHA256, state, orchestrator.Policy, decision.Identity)
	payload, err = orchestrator.Native.Execute(ctx, Plan{Decision: child, ParentDecisionID: decision.Identity}, raw)
	if err != nil {
		return Result{Decision: child, Promotion: &outcome}, err
	}
	return Result{Payload: append([]byte(nil), payload...), Decision: child, Promotion: &outcome}, nil
}

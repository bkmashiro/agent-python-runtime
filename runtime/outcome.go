package runtime

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
)

const maxRequiredFeatures = 8

type RequiredFeature string

const (
	// BrowserRuntime means page rendering, DOM/JavaScript execution, browser
	// session state, and UI automation. Semantic Host tools such as web_search
	// and bounded web_fetch are capabilities, not runtime requirements.
	RequiredFeatureBrowserRuntime        RequiredFeature = "browser_runtime"
	RequiredFeatureDaemon                RequiredFeature = "daemon"
	RequiredFeatureDynamicPackageInstall RequiredFeature = "dynamic_package_install"
	RequiredFeatureNativeExtension       RequiredFeature = "native_extension"
	RequiredFeatureNativeThreads         RequiredFeature = "native_threads"
	RequiredFeaturePOSIX                 RequiredFeature = "posix"
	RequiredFeatureShell                 RequiredFeature = "shell"
	RequiredFeatureSubprocess            RequiredFeature = "subprocess"
)

type OutcomeKind string

type EscalationReason string

type WorkspaceDisposition string

type EffectDisposition string

const (
	OutcomeRuntimeUnsupported OutcomeKind = "runtime_unsupported"

	EscalationReasonRequiredFeatures EscalationReason = "required_features_unsupported"

	WorkspaceNotStarted WorkspaceDisposition = "not_started"
	EffectsNotStarted   EffectDisposition    = "not_started"
)

var (
	ErrInvalidRunRequirements  = errors.New("invalid run requirements")
	ErrInvalidExecutionOutcome = errors.New("invalid execution outcome")
)

// UnsupportedRunError is authored by the Host admission boundary. Guest error
// text must never be converted into this type.
type UnsupportedRunError struct {
	Code             OutcomeKind
	RequiredFeatures []RequiredFeature
}

func (failure *UnsupportedRunError) Error() string {
	if failure == nil {
		return "runtime requirements unsupported"
	}
	return fmt.Sprintf("runtime requirements unsupported (%d required features)", len(failure.RequiredFeatures))
}

func (failure *UnsupportedRunError) validate() error {
	if failure == nil || failure.Code != OutcomeRuntimeUnsupported {
		return ErrInvalidRunRequirements
	}
	return validateRequiredFeatures(failure.RequiredFeatures, true)
}

// AdmitRunRequirements performs compatibility admission before workspace use,
// capability Broker creation, or Guest checkout. Current Pysolate intentionally
// supports none of these ambient/native feature classes.
func AdmitRunRequirements(request RunRequest) error {
	if len(request.Requirements) == 0 {
		return nil
	}
	if err := ValidateRunRequirements(request.Requirements); err != nil {
		return err
	}
	required := append([]RequiredFeature(nil), request.Requirements...)
	sort.Slice(required, func(i, j int) bool { return required[i] < required[j] })
	return &UnsupportedRunError{Code: OutcomeRuntimeUnsupported, RequiredFeatures: required}
}

// ValidateRunRequirements validates untrusted compatibility declarations. An
// empty list means the request declares no need for an unsupported feature.
func ValidateRunRequirements(features []RequiredFeature) error {
	if len(features) == 0 {
		return nil
	}
	return validateRequiredFeatures(features, false)
}

type OutcomeEvidence struct {
	RequestSHA256 string `json:"request_sha256"`
}

type ExecutionOutcome struct {
	SchemaVersion        uint32               `json:"schema_version"`
	Kind                 OutcomeKind          `json:"kind"`
	EscalationRequired   bool                 `json:"escalation_required"`
	EscalationReason     EscalationReason     `json:"escalation_reason"`
	RequiredFeatures     []RequiredFeature    `json:"required_features"`
	WorkspaceDisposition WorkspaceDisposition `json:"workspace_disposition"`
	EffectDisposition    EffectDisposition    `json:"effect_disposition"`
	Evidence             OutcomeEvidence      `json:"evidence"`
}

func NewUnsupportedOutcome(rawRequest []byte, runErr error) (ExecutionOutcome, error) {
	var unsupported *UnsupportedRunError
	if !errors.As(runErr, &unsupported) || unsupported.validate() != nil || len(rawRequest) == 0 {
		return ExecutionOutcome{}, ErrInvalidExecutionOutcome
	}
	digest := sha256.Sum256(rawRequest)
	outcome := ExecutionOutcome{
		SchemaVersion:        1,
		Kind:                 OutcomeRuntimeUnsupported,
		EscalationRequired:   true,
		EscalationReason:     EscalationReasonRequiredFeatures,
		RequiredFeatures:     append([]RequiredFeature(nil), unsupported.RequiredFeatures...),
		WorkspaceDisposition: WorkspaceNotStarted,
		EffectDisposition:    EffectsNotStarted,
		Evidence:             OutcomeEvidence{RequestSHA256: fmt.Sprintf("sha256:%x", digest[:])},
	}
	if err := outcome.Validate(); err != nil {
		return ExecutionOutcome{}, err
	}
	return outcome, nil
}

func (outcome ExecutionOutcome) Validate() error {
	if outcome.SchemaVersion != 1 || outcome.Kind != OutcomeRuntimeUnsupported || !outcome.EscalationRequired ||
		outcome.EscalationReason != EscalationReasonRequiredFeatures || outcome.WorkspaceDisposition != WorkspaceNotStarted ||
		outcome.EffectDisposition != EffectsNotStarted || !sha256DigestPattern.MatchString(outcome.Evidence.RequestSHA256) {
		return ErrInvalidExecutionOutcome
	}
	if err := validateRequiredFeatures(outcome.RequiredFeatures, true); err != nil {
		return ErrInvalidExecutionOutcome
	}
	return nil
}

func DecodeExecutionOutcome(data []byte) (ExecutionOutcome, error) {
	if err := rejectDuplicateBoundedJSON(data); err != nil {
		return ExecutionOutcome{}, ErrInvalidExecutionOutcome
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var outcome ExecutionOutcome
	if err := decoder.Decode(&outcome); err != nil {
		return ExecutionOutcome{}, ErrInvalidExecutionOutcome
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ExecutionOutcome{}, ErrInvalidExecutionOutcome
	}
	if err := outcome.Validate(); err != nil {
		return ExecutionOutcome{}, err
	}
	return outcome, nil
}

func validateRequiredFeatures(features []RequiredFeature, requireSorted bool) error {
	if len(features) == 0 || len(features) > maxRequiredFeatures {
		return ErrInvalidRunRequirements
	}
	seen := make(map[RequiredFeature]struct{}, len(features))
	var previous RequiredFeature
	for index, feature := range features {
		if !validRequiredFeature(feature) {
			return ErrInvalidRunRequirements
		}
		if _, exists := seen[feature]; exists {
			return ErrInvalidRunRequirements
		}
		if requireSorted && index > 0 && previous >= feature {
			return ErrInvalidRunRequirements
		}
		seen[feature] = struct{}{}
		previous = feature
	}
	return nil
}

func validRequiredFeature(feature RequiredFeature) bool {
	switch feature {
	case RequiredFeatureBrowserRuntime, RequiredFeatureDaemon, RequiredFeatureDynamicPackageInstall,
		RequiredFeatureNativeExtension, RequiredFeatureNativeThreads, RequiredFeaturePOSIX,
		RequiredFeatureShell, RequiredFeatureSubprocess:
		return true
	default:
		return false
	}
}

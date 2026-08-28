package sourcepatch

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"

	"github.com/bkmashiro/agent-python-runtime/runtime/passregistration"
)

const (
	SchemaVersion                                               = "pysolate.source-pass-patch.v1"
	PureScalarCSEName                     passregistration.Name = "pure_scalar_cse"
	PureScalarCSEVersion                                        = "pysolate.pure-scalar-cse-pass.v1"
	PureScalarCSEConfigSHA256                                   = "sha256:48884671a1121cfb21c2b19f79faec1530342a6175ab515c0011ce2074e3057f"
	PureScalarFoldName                    passregistration.Name = "pure_scalar_fold"
	PureScalarFoldVersion                                       = "pysolate.pure-scalar-fold-pass.v1"
	PureScalarFoldConfigSHA256                                  = "sha256:ebd1a5f88e49f6170044cc146ad394b9967aa5aad5443f9d2734c86c98b130c6"
	SplitPhaseCapabilityCallsName         passregistration.Name = "split_phase_capability_calls"
	SplitPhaseCapabilityCallsVersion                            = "pysolate.split-phase-capability-calls-pass.v1"
	SplitPhaseCapabilityCallsConfigSHA256                       = "sha256:d89ffaccc0315d3fb8f24036e693a778c13565f6a8cdb7c31799e8559c89cc63"
	PLMCapabilityCallsName                passregistration.Name = "plm_capability_calls"
	PLMCapabilityCallsVersion                                   = "pysolate.plm-capability-calls-pass.v1"
	PLMCapabilityCallsConfigSHA256                              = "sha256:18861b30178031a491a1728dbb37d5476ffaff5f3f915934b8a338d4542da0fb"
	DataLocalNumpySumName                 passregistration.Name = "data_local_numpy_sum"
	DataLocalNumpySumVersion                                    = "pysolate.data-local-numpy-sum-pass.v2"
	DataLocalNumpySumConfigSHA256                               = "sha256:391f84660ff12c489c3275ae9073622d1baf4ea6b54a629b51804b331a1b4c7b"
)

var (
	ErrInvalidPatch   = errors.New("invalid source pass patch")
	digestPattern     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

type Transformer interface {
	TransformSourcePass(context.Context, []byte) ([]byte, error)
}

type Request struct {
	PassName              passregistration.Name  `json:"pass_name"`
	PassVersion           string                 `json:"pass_version"`
	RegistrationSHA256    string                 `json:"registration_sha256"`
	Source                string                 `json:"source"`
	CapabilityProjections []CapabilityProjection `json:"capability_projections,omitempty"`
}

type CapabilityProjection struct {
	Capability  string   `json:"capability"`
	Module      string   `json:"module"`
	Method      string   `json:"method"`
	Arguments   []string `json:"arguments"`
	ResultField string   `json:"result_field"`
}

type Patch struct {
	SchemaVersion         string                 `json:"schema_version"`
	Status                string                 `json:"status"`
	PassName              passregistration.Name  `json:"pass_name"`
	PassVersion           string                 `json:"pass_version"`
	RegistrationSHA256    string                 `json:"registration_sha256"`
	OriginalSourceSHA256  string                 `json:"original_source_sha256"`
	OriginalASTSHA256     string                 `json:"original_ast_sha256"`
	DerivedSource         string                 `json:"derived_source"`
	DerivedSourceSHA256   string                 `json:"derived_source_sha256"`
	DerivedASTSHA256      string                 `json:"derived_ast_sha256"`
	ReplacementCount      uint32                 `json:"replacement_count"`
	CapabilityProjections []CapabilityProjection `json:"capability_projections,omitempty"`
}

type PureScalarCSE struct {
	registration passregistration.Registration
}

type PureScalarFold struct {
	registration passregistration.Registration
}

type SplitPhaseCapabilityCalls struct {
	registration passregistration.Registration
}

type PLMCapabilityCalls struct {
	registration passregistration.Registration
}

type DataLocalNumpySum struct {
	registration passregistration.Registration
}

func NewPureScalarCSE(analyzerSHA256 string) (PureScalarCSE, error) {
	definition, err := passregistration.Define(
		PureScalarCSEName, PureScalarCSEVersion, passregistration.StageWholeProgramPatch,
		passregistration.ExecutionPatch, passregistration.PatchBindings(),
	)
	if err != nil {
		return PureScalarCSE{}, err
	}
	registration, err := definition.Register(analyzerSHA256, PureScalarCSEConfigSHA256)
	if err != nil {
		return PureScalarCSE{}, err
	}
	return PureScalarCSE{registration: registration}, nil
}

func (pass PureScalarCSE) Registration() passregistration.Registration {
	return pass.registration
}

func NewPureScalarFold(analyzerSHA256 string) (PureScalarFold, error) {
	definition, err := passregistration.Define(
		PureScalarFoldName, PureScalarFoldVersion, passregistration.StageWholeProgramPatch,
		passregistration.ExecutionPatch, passregistration.PatchBindings(),
	)
	if err != nil {
		return PureScalarFold{}, err
	}
	registration, err := definition.Register(analyzerSHA256, PureScalarFoldConfigSHA256)
	if err != nil {
		return PureScalarFold{}, err
	}
	return PureScalarFold{registration: registration}, nil
}

func (pass PureScalarFold) Registration() passregistration.Registration {
	return pass.registration
}

func NewSplitPhaseCapabilityCalls(analyzerSHA256 string) (SplitPhaseCapabilityCalls, error) {
	definition, err := passregistration.Define(
		SplitPhaseCapabilityCallsName, SplitPhaseCapabilityCallsVersion, passregistration.StageWholeProgramPatch,
		passregistration.ExecutionPatch, passregistration.PatchBindings(),
	)
	if err != nil {
		return SplitPhaseCapabilityCalls{}, err
	}
	registration, err := definition.Register(analyzerSHA256, SplitPhaseCapabilityCallsConfigSHA256)
	if err != nil {
		return SplitPhaseCapabilityCalls{}, err
	}
	return SplitPhaseCapabilityCalls{registration: registration}, nil
}

func (pass SplitPhaseCapabilityCalls) Registration() passregistration.Registration {
	return pass.registration
}

func (pass SplitPhaseCapabilityCalls) HostScheduled() bool { return true }

func NewPLMCapabilityCalls(analyzerSHA256 string) (PLMCapabilityCalls, error) {
	definition, err := passregistration.Define(
		PLMCapabilityCallsName, PLMCapabilityCallsVersion, passregistration.StageWholeProgramPatch,
		passregistration.ExecutionPatch, passregistration.PatchBindings(),
	)
	if err != nil {
		return PLMCapabilityCalls{}, err
	}
	registration, err := definition.Register(analyzerSHA256, PLMCapabilityCallsConfigSHA256)
	if err != nil {
		return PLMCapabilityCalls{}, err
	}
	return PLMCapabilityCalls{registration: registration}, nil
}

func (pass PLMCapabilityCalls) Registration() passregistration.Registration {
	return pass.registration
}

func (pass PLMCapabilityCalls) HostScheduled() bool { return true }

func NewDataLocalNumpySum(analyzerSHA256 string) (DataLocalNumpySum, error) {
	definition, err := passregistration.Define(
		DataLocalNumpySumName, DataLocalNumpySumVersion, passregistration.StageWholeProgramPatch,
		passregistration.ExecutionPatch, passregistration.PatchBindings(),
	)
	if err != nil {
		return DataLocalNumpySum{}, err
	}
	registration, err := definition.Register(analyzerSHA256, DataLocalNumpySumConfigSHA256)
	if err != nil {
		return DataLocalNumpySum{}, err
	}
	return DataLocalNumpySum{registration: registration}, nil
}

func (pass DataLocalNumpySum) Registration() passregistration.Registration { return pass.registration }
func (pass DataLocalNumpySum) ValueSlotBound() bool                        { return true }

func (pass PureScalarFold) Transform(ctx context.Context, transformer Transformer, source string) (Patch, error) {
	return transform(ctx, transformer, source, pass.registration, PureScalarFoldName, nil)
}

func (pass PureScalarCSE) Transform(ctx context.Context, transformer Transformer, source string) (Patch, error) {
	return transform(ctx, transformer, source, pass.registration, PureScalarCSEName, nil)
}

func (pass SplitPhaseCapabilityCalls) Transform(ctx context.Context, transformer Transformer, source string, projections []CapabilityProjection) (Patch, error) {
	if !validCapabilityProjections(projections) {
		return Patch{}, ErrInvalidPatch
	}
	return transform(ctx, transformer, source, pass.registration, SplitPhaseCapabilityCallsName, projections)
}

func (pass PLMCapabilityCalls) Transform(ctx context.Context, transformer Transformer, source string, projections []CapabilityProjection) (Patch, error) {
	if !validCapabilityProjections(projections) {
		return Patch{}, ErrInvalidPatch
	}
	return transform(ctx, transformer, source, pass.registration, PLMCapabilityCallsName, projections)
}

func (pass DataLocalNumpySum) Transform(ctx context.Context, transformer Transformer, source string) (Patch, error) {
	return transform(ctx, transformer, source, pass.registration, DataLocalNumpySumName, nil)
}

func transform(ctx context.Context, transformer Transformer, source string, registration passregistration.Registration, expectedName passregistration.Name, projections []CapabilityProjection) (Patch, error) {
	if ctx == nil || transformer == nil || source == "" || registration.Name() != expectedName {
		return Patch{}, ErrInvalidPatch
	}
	request, err := json.Marshal(Request{
		PassName: registration.Name(), PassVersion: registration.Version(),
		RegistrationSHA256: registration.IdentitySHA256(), Source: source,
		CapabilityProjections: cloneCapabilityProjections(projections),
	})
	if err != nil {
		return Patch{}, err
	}
	payload, err := transformer.TransformSourcePass(ctx, request)
	if err != nil {
		return Patch{}, err
	}
	patch, err := Decode(payload)
	if err != nil || patch.Validate(source, registration) != nil || !equalCapabilityProjections(patch.CapabilityProjections, projections) {
		return Patch{}, ErrInvalidPatch
	}
	return patch, nil
}

func Decode(raw []byte) (Patch, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var patch Patch
	if err := decoder.Decode(&patch); err != nil {
		return Patch{}, ErrInvalidPatch
	}
	return patch, nil
}

func (patch Patch) Validate(source string, registration passregistration.Registration) error {
	if patch.SchemaVersion != SchemaVersion || patch.PassName != registration.Name() ||
		patch.PassVersion != registration.Version() || patch.RegistrationSHA256 != registration.IdentitySHA256() ||
		patch.OriginalSourceSHA256 != digest([]byte(source)) || !digestPattern.MatchString(patch.OriginalASTSHA256) {
		return ErrInvalidPatch
	}
	if registration.Name() == SplitPhaseCapabilityCallsName || registration.Name() == PLMCapabilityCallsName {
		if !validCapabilityProjections(patch.CapabilityProjections) {
			return ErrInvalidPatch
		}
	} else if len(patch.CapabilityProjections) != 0 {
		return ErrInvalidPatch
	}
	switch patch.Status {
	case "applied":
		if patch.ReplacementCount == 0 || patch.DerivedSource == "" ||
			patch.DerivedSourceSHA256 != digest([]byte(patch.DerivedSource)) || !digestPattern.MatchString(patch.DerivedASTSHA256) {
			return ErrInvalidPatch
		}
	case "not_applicable":
		if patch.ReplacementCount != 0 || patch.DerivedSource != "" || patch.DerivedSourceSHA256 != "" || patch.DerivedASTSHA256 != "" {
			return ErrInvalidPatch
		}
	default:
		return ErrInvalidPatch
	}
	return nil
}

func validCapabilityProjections(projections []CapabilityProjection) bool {
	if len(projections) == 0 || len(projections) > 64 {
		return false
	}
	seen := make(map[string]struct{}, len(projections))
	last := ""
	for _, projection := range projections {
		key := projection.Module + "." + projection.Method
		if projection.Capability == "" || len(projection.Capability) > 128 ||
			!identifierPattern.MatchString(projection.Module) || !identifierPattern.MatchString(projection.Method) ||
			len(projection.Arguments) > 16 || len(projection.ResultField) > 128 || last != "" && key <= last {
			return false
		}
		if _, exists := seen[key]; exists {
			return false
		}
		seen[key] = struct{}{}
		last = key
		argumentNames := make(map[string]struct{}, len(projection.Arguments))
		for _, argument := range projection.Arguments {
			if !identifierPattern.MatchString(argument) {
				return false
			}
			if _, exists := argumentNames[argument]; exists {
				return false
			}
			argumentNames[argument] = struct{}{}
		}
	}
	return true
}

func cloneCapabilityProjections(projections []CapabilityProjection) []CapabilityProjection {
	cloned := make([]CapabilityProjection, len(projections))
	for index, projection := range projections {
		cloned[index] = projection
		cloned[index].Arguments = append([]string(nil), projection.Arguments...)
	}
	return cloned
}

func equalCapabilityProjections(left, right []CapabilityProjection) bool {
	leftRaw, leftErr := json.Marshal(left)
	rightRaw, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftRaw, rightRaw)
}

func (patch Patch) Applied() bool {
	return patch.Status == "applied"
}

func digest(value []byte) string {
	digest := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(digest[:])
}

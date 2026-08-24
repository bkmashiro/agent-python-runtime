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
	SchemaVersion                                           = "pysolate.source-pass-patch.v1"
	PureScalarCSEName                 passregistration.Name = "pure_scalar_cse"
	PureScalarCSEVersion                                    = "pysolate.pure-scalar-cse-pass.v1"
	PureScalarCSEConfigSHA256                               = "sha256:48884671a1121cfb21c2b19f79faec1530342a6175ab515c0011ce2074e3057f"
	PureScalarFoldName                passregistration.Name = "pure_scalar_fold"
	PureScalarFoldVersion                                   = "pysolate.pure-scalar-fold-pass.v1"
	PureScalarFoldConfigSHA256                              = "sha256:ebd1a5f88e49f6170044cc146ad394b9967aa5aad5443f9d2734c86c98b130c6"
	SplitPhaseSourcesReadName         passregistration.Name = "split_phase_sources_read"
	SplitPhaseSourcesReadVersion                            = "pysolate.split-phase-sources-read-pass.v1"
	SplitPhaseSourcesReadConfigSHA256                       = "sha256:2a402e54fb4a0dc196737b03d8f2e03c9b1a8509bd729563b1f24f95a0ae1d7f"
	DataLocalNumpySumName             passregistration.Name = "data_local_numpy_sum"
	DataLocalNumpySumVersion                                = "pysolate.data-local-numpy-sum-pass.v1"
	DataLocalNumpySumConfigSHA256                           = "sha256:2de3ebe7ec484955cec783ca0fbbf091598d8217bff9f7cc95d9a241fbbeac64"
)

var (
	ErrInvalidPatch = errors.New("invalid source pass patch")
	digestPattern   = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

type Transformer interface {
	TransformSourcePass(context.Context, []byte) ([]byte, error)
}

type Request struct {
	PassName           passregistration.Name `json:"pass_name"`
	PassVersion        string                `json:"pass_version"`
	RegistrationSHA256 string                `json:"registration_sha256"`
	Source             string                `json:"source"`
}

type Patch struct {
	SchemaVersion        string                `json:"schema_version"`
	Status               string                `json:"status"`
	PassName             passregistration.Name `json:"pass_name"`
	PassVersion          string                `json:"pass_version"`
	RegistrationSHA256   string                `json:"registration_sha256"`
	OriginalSourceSHA256 string                `json:"original_source_sha256"`
	OriginalASTSHA256    string                `json:"original_ast_sha256"`
	DerivedSource        string                `json:"derived_source"`
	DerivedSourceSHA256  string                `json:"derived_source_sha256"`
	DerivedASTSHA256     string                `json:"derived_ast_sha256"`
	ReplacementCount     uint32                `json:"replacement_count"`
}

type PureScalarCSE struct {
	registration passregistration.Registration
}

type PureScalarFold struct {
	registration passregistration.Registration
}

type SplitPhaseSourcesRead struct {
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

func NewSplitPhaseSourcesRead(analyzerSHA256 string) (SplitPhaseSourcesRead, error) {
	definition, err := passregistration.Define(
		SplitPhaseSourcesReadName, SplitPhaseSourcesReadVersion, passregistration.StageWholeProgramPatch,
		passregistration.ExecutionPatch, passregistration.PatchBindings(),
	)
	if err != nil {
		return SplitPhaseSourcesRead{}, err
	}
	registration, err := definition.Register(analyzerSHA256, SplitPhaseSourcesReadConfigSHA256)
	if err != nil {
		return SplitPhaseSourcesRead{}, err
	}
	return SplitPhaseSourcesRead{registration: registration}, nil
}

func (pass SplitPhaseSourcesRead) Registration() passregistration.Registration {
	return pass.registration
}

func (pass SplitPhaseSourcesRead) HostScheduled() bool { return true }

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
	return transform(ctx, transformer, source, pass.registration, PureScalarFoldName)
}

func (pass PureScalarCSE) Transform(ctx context.Context, transformer Transformer, source string) (Patch, error) {
	return transform(ctx, transformer, source, pass.registration, PureScalarCSEName)
}

func (pass SplitPhaseSourcesRead) Transform(ctx context.Context, transformer Transformer, source string) (Patch, error) {
	return transform(ctx, transformer, source, pass.registration, SplitPhaseSourcesReadName)
}

func (pass DataLocalNumpySum) Transform(ctx context.Context, transformer Transformer, source string) (Patch, error) {
	return transform(ctx, transformer, source, pass.registration, DataLocalNumpySumName)
}

func transform(ctx context.Context, transformer Transformer, source string, registration passregistration.Registration, expectedName passregistration.Name) (Patch, error) {
	if ctx == nil || transformer == nil || source == "" || registration.Name() != expectedName {
		return Patch{}, ErrInvalidPatch
	}
	request, err := json.Marshal(Request{
		PassName: registration.Name(), PassVersion: registration.Version(),
		RegistrationSHA256: registration.IdentitySHA256(), Source: source,
	})
	if err != nil {
		return Patch{}, err
	}
	payload, err := transformer.TransformSourcePass(ctx, request)
	if err != nil {
		return Patch{}, err
	}
	patch, err := Decode(payload)
	if err != nil || patch.Validate(source, registration) != nil {
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

func (patch Patch) Applied() bool {
	return patch.Status == "applied"
}

func digest(value []byte) string {
	digest := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(digest[:])
}

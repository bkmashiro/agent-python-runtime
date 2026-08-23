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
	SchemaVersion                                    = "pysolate.source-pass-patch.v1"
	PureScalarCSEName          passregistration.Name = "pure_scalar_cse"
	PureScalarCSEVersion                             = "pysolate.pure-scalar-cse-pass.v1"
	PureScalarCSEConfigSHA256                        = "sha256:b5bf35fb21f37dabf3b63a92026ce9192d7f7106e73c5533744bc5653df9c48e"
	PureScalarFoldName         passregistration.Name = "pure_scalar_fold"
	PureScalarFoldVersion                            = "pysolate.pure-scalar-fold-pass.v1"
	PureScalarFoldConfigSHA256                       = "sha256:21fd8ed45356fe42c788e176fd0a5bdd61fe14c6375091bf030857e031403ebd"
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

func (pass PureScalarFold) Transform(ctx context.Context, transformer Transformer, source string) (Patch, error) {
	return transform(ctx, transformer, source, pass.registration, PureScalarFoldName)
}

func (pass PureScalarCSE) Transform(ctx context.Context, transformer Transformer, source string) (Patch, error) {
	return transform(ctx, transformer, source, pass.registration, PureScalarCSEName)
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

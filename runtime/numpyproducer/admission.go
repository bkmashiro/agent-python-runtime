package numpyproducer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"regexp"

	"github.com/bkmashiro/agent-python-runtime/internal/publicationauth"
	"github.com/bkmashiro/agent-python-runtime/runtime/numpycodec"
	passregistration "github.com/bkmashiro/agent-python-runtime/runtime/passregistration"
	"github.com/bkmashiro/agent-python-runtime/runtime/resultblob"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

const (
	DeclarationSchemaVersion  = "pysolate.numpy-producer-declaration.v1"
	AdmissionSchemaVersion    = "pysolate.numpy-producer-admission.v1"
	OperationArangeAffineI64  = "arange_affine_i64_v1"
	OperationZerosF64         = "zeros_f64_v1"
	OperationAffineF64        = "affine_f64_v1"
	OperationSumI64           = "sum_i64_v1"
	OperationMatmulF64        = "matmul_f64_v1"
	MaxExecutionResponseBytes = 16 * 1024 * 1024
)

var (
	ErrDeclaration  = errors.New("invalid numpy producer declaration")
	ErrSourcePolicy = errors.New("numpy producer source policy mismatch")
	ErrBinding      = errors.New("numpy producer binding mismatch")
	ErrAnalysis     = errors.New("numpy producer analysis mismatch")
	ErrExecution    = errors.New("numpy producer execution not publishable")
	digestPattern   = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

type PublicationGuard = resultblob.PublicationGuard

type DeclarationInput struct {
	Operation     string
	Rows          uint64
	Cols          uint64
	InputElements uint64
	Start         int64
	Step          int64
	Add           int64
}

type Declaration struct {
	SchemaVersion  string `json:"schema_version"`
	Operation      string `json:"operation"`
	Rows           uint64 `json:"rows"`
	Cols           uint64 `json:"cols"`
	Start          int64  `json:"start"`
	Stop           int64  `json:"stop"`
	Step           int64  `json:"step"`
	Add            int64  `json:"add"`
	DType          string `json:"dtype"`
	Order          string `json:"order"`
	NumPyVersion   string `json:"numpy_version"`
	BodyBytes      uint64 `json:"body_bytes"`
	InputElements  uint64 `json:"input_elements"`
	IdentitySHA256 string `json:"identity_sha256"`
}

type declarationIdentity struct {
	SchemaVersion string `json:"schema_version"`
	Operation     string `json:"operation"`
	Rows          uint64 `json:"rows"`
	Cols          uint64 `json:"cols"`
	Start         int64  `json:"start"`
	Stop          int64  `json:"stop"`
	Step          int64  `json:"step"`
	Add           int64  `json:"add"`
	DType         string `json:"dtype"`
	Order         string `json:"order"`
	NumPyVersion  string `json:"numpy_version"`
	BodyBytes     uint64 `json:"body_bytes"`
	InputElements uint64 `json:"input_elements"`
}

type Bindings struct {
	ArtifactSHA256         string `json:"artifact_sha256"`
	ExecutionProfileID     string `json:"execution_profile_id"`
	ExecutionProfileSHA256 string `json:"execution_profile_sha256"`
	ImportClosureSHA256    string `json:"import_closure_sha256"`
	CapabilityPlanSHA256   string `json:"capability_plan_sha256"`
}

type Admission struct {
	SchemaVersion           string              `json:"schema_version"`
	Operation               string              `json:"operation"`
	DeclarationSHA256       string              `json:"declaration_sha256"`
	SourceSHA256            string              `json:"source_sha256"`
	ASTSHA256               string              `json:"ast_sha256"`
	AnalysisSHA256          string              `json:"analysis_sha256"`
	InputsSHA256            string              `json:"inputs_sha256"`
	ArtifactSHA256          string              `json:"artifact_sha256"`
	ExecutionProfileID      string              `json:"execution_profile_id"`
	ExecutionProfileSHA256  string              `json:"execution_profile_sha256"`
	ImportClosureSHA256     string              `json:"import_closure_sha256"`
	CapabilityPlanSHA256    string              `json:"capability_plan_sha256"`
	PassConfigSHA256        string              `json:"pass_config_sha256"`
	PassRegistrationSHA256  string              `json:"pass_registration_sha256"`
	OutputName              string              `json:"output_name"`
	RegionSpan              semantic.SourceSpan `json:"region_span"`
	AllowedImports          []string            `json:"allowed_imports"`
	NoExternalInputs        bool                `json:"no_external_inputs"`
	AnalyzerUnknownObserved bool                `json:"analyzer_unknown_observed"`
	IdentitySHA256          string              `json:"identity_sha256"`
}

type admissionIdentity Admission

func SealDeclaration(input DeclarationInput) ([]byte, Declaration, error) {
	identity, ok := normalizeInput(input)
	if !ok {
		return nil, Declaration{}, ErrDeclaration
	}
	declaration := Declaration{
		SchemaVersion: identity.SchemaVersion, Operation: identity.Operation, Rows: identity.Rows, Cols: identity.Cols,
		Start: identity.Start, Stop: identity.Stop, Step: identity.Step, Add: identity.Add, DType: identity.DType,
		Order: identity.Order, NumPyVersion: identity.NumPyVersion, BodyBytes: identity.BodyBytes,
		InputElements: identity.InputElements, IdentitySHA256: digestJSON(identity),
	}
	raw, err := json.Marshal(declaration)
	if err != nil {
		return nil, Declaration{}, ErrDeclaration
	}
	return raw, declaration, nil
}

func DecodeDeclaration(raw []byte) (Declaration, error) {
	var declaration Declaration
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&declaration) != nil || decoder.Decode(&struct{}{}) != io.EOF || !declaration.valid() {
		return Declaration{}, ErrDeclaration
	}
	canonical, _ := json.Marshal(declaration)
	if !bytes.Equal(canonical, raw) {
		return Declaration{}, ErrDeclaration
	}
	return declaration, nil
}

func (declaration Declaration) valid() bool {
	if declaration.SchemaVersion != DeclarationSchemaVersion || declaration.Order != "C" || declaration.NumPyVersion != "1.26.0b1" ||
		!digestPattern.MatchString(declaration.IdentitySHA256) {
		return false
	}
	identity, ok := normalizeInput(DeclarationInput{
		Operation: declaration.Operation, Rows: declaration.Rows, Cols: declaration.Cols, InputElements: declaration.InputElements,
		Start: declaration.Start, Step: declaration.Step, Add: declaration.Add,
	})
	if !ok || identity.Stop != declaration.Stop || identity.DType != declaration.DType || identity.BodyBytes != declaration.BodyBytes {
		return false
	}
	return declaration.IdentitySHA256 == digestJSON(identity)
}

func validOperation(operation string) bool {
	switch operation {
	case OperationArangeAffineI64, OperationZerosF64, OperationAffineF64, OperationSumI64, OperationMatmulF64:
		return true
	default:
		return false
	}
}

func normalizeInput(input DeclarationInput) (declarationIdentity, bool) {
	operation := input.Operation
	if operation == "" {
		operation = OperationArangeAffineI64
	}
	identity := declarationIdentity{SchemaVersion: DeclarationSchemaVersion, Operation: operation, Order: "C", NumPyVersion: "1.26.0b1"}
	switch operation {
	case OperationArangeAffineI64:
		if input.InputElements != 0 || input.Rows == 0 || input.Cols == 0 || input.Step == 0 ||
			input.Rows > numpycodec.MaxDimension || input.Cols > numpycodec.MaxDimension || input.Rows > numpycodec.MaxBodyBytes/8/input.Cols {
			return declarationIdentity{}, false
		}
		elements := input.Rows * input.Cols
		start := big.NewInt(input.Start)
		step := big.NewInt(input.Step)
		stopBig := new(big.Int).Add(start, new(big.Int).Mul(step, new(big.Int).SetUint64(elements)))
		lastBig := new(big.Int).Add(start, new(big.Int).Mul(step, new(big.Int).SetUint64(elements-1)))
		add := big.NewInt(input.Add)
		if !stopBig.IsInt64() || !new(big.Int).Add(new(big.Int).Set(start), add).IsInt64() || !new(big.Int).Add(lastBig, add).IsInt64() {
			return declarationIdentity{}, false
		}
		identity.Rows, identity.Cols, identity.Start, identity.Stop, identity.Step, identity.Add = input.Rows, input.Cols, input.Start, stopBig.Int64(), input.Step, input.Add
		identity.DType, identity.BodyBytes = "<i8", elements*8
	case OperationZerosF64, OperationAffineF64:
		if input.InputElements == 0 || input.InputElements > numpycodec.MaxBodyBytes/8 || input.Start != 0 || input.Step != 0 || input.Add != 0 ||
			!((input.Rows == 0 && input.Cols == 0) || (input.Rows == 1 && input.Cols == input.InputElements)) {
			return declarationIdentity{}, false
		}
		identity.Rows, identity.Cols, identity.InputElements = 1, input.InputElements, input.InputElements
		identity.DType, identity.BodyBytes = "<f8", input.InputElements*8
	case OperationSumI64:
		if input.InputElements == 0 || input.InputElements > numpycodec.MaxBodyBytes/8 || input.Start != 0 || input.Step != 0 || input.Add != 0 ||
			!((input.Rows == 0 && input.Cols == 0) || (input.Rows == 1 && input.Cols == 1)) {
			return declarationIdentity{}, false
		}
		identity.Rows, identity.Cols, identity.InputElements = 1, 1, input.InputElements
		identity.DType, identity.BodyBytes = "<i8", 8
	case OperationMatmulF64:
		if input.Rows == 0 || input.Rows != input.Cols || input.Rows > numpycodec.MaxDimension || input.Rows > numpycodec.MaxBodyBytes/8/input.Rows ||
			input.Start != 0 || input.Step != 0 || input.Add != 0 || (input.InputElements != 0 && input.InputElements != input.Rows*input.Rows) {
			return declarationIdentity{}, false
		}
		identity.Rows, identity.Cols, identity.InputElements = input.Rows, input.Cols, input.Rows*input.Rows
		identity.DType, identity.BodyBytes = "<f8", input.Rows*input.Rows*8
	default:
		return declarationIdentity{}, false
	}
	return identity, true
}

func RenderSource(declaration Declaration) (string, error) {
	if !declaration.valid() {
		return "", ErrDeclaration
	}
	var producer string
	switch declaration.Operation {
	case OperationArangeAffineI64:
		producer = fmt.Sprintf("start = %d\nstop = %d\nstep = %d\nadd = %d\narray = (np.arange(start, stop, step, dtype=np.int64) + np.int64(add)).reshape((%d, %d)).copy(order='C')", declaration.Start, declaration.Stop, declaration.Step, declaration.Add, declaration.Rows, declaration.Cols)
	case OperationZerosF64:
		producer = fmt.Sprintf("array = np.zeros((%d,), dtype=np.float64).copy(order='C')", declaration.InputElements)
	case OperationAffineF64:
		producer = fmt.Sprintf("base = np.arange(%d, dtype=np.float64)\narray = (base * 1.5 + 2.0).copy(order='C')", declaration.InputElements)
	case OperationSumI64:
		producer = fmt.Sprintf("base = np.arange(%d, dtype=np.int64)\narray = np.asarray([np.sum(base)], dtype=np.int64).copy(order='C')", declaration.InputElements)
	case OperationMatmulF64:
		producer = fmt.Sprintf("base = np.arange(%d, dtype=np.float64).reshape((%d, %d))\narray = (base @ base.T).copy(order='C')", declaration.InputElements, declaration.Rows, declaration.Cols)
	default:
		return "", ErrDeclaration
	}
	return fmt.Sprintf(`import base64
import hashlib
import numpy as np
assert np.__version__ == '1.26.0b1'
%s
assert array.dtype.str == '%s'
assert array.flags.c_contiguous
body = array.tobytes(order='C')
result = {
    "schema_version": "pysolate.numpy-ndarray-producer-value.v1",
    "dtype": array.dtype.str,
    "shape": list(array.shape),
    "order": "C",
    "c_contiguous": bool(array.flags.c_contiguous),
    "nbytes": len(body),
    "body_sha256": "sha256:" + hashlib.sha256(body).hexdigest(),
    "body_base64": base64.b64encode(body).decode("ascii"),
}`, producer, declaration.DType), nil
}

// Admit requires an opaque analysis minted by the concrete exact-Guest analyzer.
// The analysis may conservatively report unknown native effects; that fact is
// recorded, never upgraded into a purity certificate.
func Admit(declarationRaw []byte, source string, verified semantic.VerifiedAnalysis, bindings Bindings) (Admission, error) {
	analysis, err := verified.Analysis()
	if err != nil {
		return Admission{}, ErrAnalysis
	}
	return admitAnalysis(declarationRaw, source, analysis, bindings)
}

func admitAnalysis(declarationRaw []byte, source string, analysis semantic.Analysis, bindings Bindings) (Admission, error) {
	if !validBindings(bindings) {
		return Admission{}, ErrBinding
	}
	declaration, err := DecodeDeclaration(declarationRaw)
	if err != nil {
		return Admission{}, err
	}
	expectedSource, _ := RenderSource(declaration)
	if source != expectedSource {
		return Admission{}, ErrSourcePolicy
	}
	analysisSHA, _, err := analysis.Identity()
	if err != nil || analysis.SourceSHA256 != Digest([]byte(source)) || analysis.AnalyzerSHA256 != semantic.AnalyzerIdentity() ||
		analysis.ArtifactSHA256 != bindings.ArtifactSHA256 || analysis.ExecutionProfileSHA256 != bindings.ExecutionProfileSHA256 ||
		analysis.ImportClosureSHA256 != bindings.ImportClosureSHA256 || analysis.CapabilityPlanSHA256 != bindings.CapabilityPlanSHA256 ||
		analysis.ModuleEffects.MayPublish || analysis.ModuleEffects.MayObserveLive || analysis.ModuleEffects.MaySuspend || len(analysis.CallSites) != 0 {
		return Admission{}, ErrAnalysis
	}
	passConfigSHA256 := Digest([]byte("pysolate.numpy-producer-pass.v1\x00" + declaration.IdentitySHA256))
	registration, err := passregistration.New(
		passregistration.PreparedPureRegion, passregistration.PreparedPureRegionVersion,
		passregistration.SemanticAnalyzerSHA256, passConfigSHA256,
		passregistration.ExecutionPatch, passregistration.PatchBindings(),
	)
	if err != nil {
		return Admission{}, ErrBinding
	}
	admission := Admission{
		SchemaVersion: AdmissionSchemaVersion, Operation: declaration.Operation, DeclarationSHA256: declaration.IdentitySHA256,
		SourceSHA256: analysis.SourceSHA256, ASTSHA256: analysis.ASTSHA256, AnalysisSHA256: analysisSHA,
		InputsSHA256: Digest(declarationRaw), ArtifactSHA256: bindings.ArtifactSHA256,
		ExecutionProfileID: bindings.ExecutionProfileID, ExecutionProfileSHA256: bindings.ExecutionProfileSHA256,
		ImportClosureSHA256: bindings.ImportClosureSHA256, CapabilityPlanSHA256: bindings.CapabilityPlanSHA256,
		PassConfigSHA256: passConfigSHA256, PassRegistrationSHA256: registration.IdentitySHA256(),
		OutputName: "array", RegionSpan: analysis.ModuleSpan,
		AllowedImports: []string{"base64", "hashlib", "numpy"}, NoExternalInputs: true,
		AnalyzerUnknownObserved: analysis.ModuleEffects.MayBeUnknown,
	}
	identity := admission
	identity.IdentitySHA256 = ""
	admission.IdentitySHA256 = digestJSON(admissionIdentity(identity))
	return admission, nil
}

func (admission Admission) Validate() error {
	if !validOperation(admission.Operation) || admission.SchemaVersion != AdmissionSchemaVersion ||
		admission.ExecutionProfileID != "numpy-core" || admission.OutputName != "array" || !admission.NoExternalInputs ||
		!admission.AnalyzerUnknownObserved || len(admission.AllowedImports) != 3 || admission.AllowedImports[0] != "base64" ||
		admission.AllowedImports[1] != "hashlib" || admission.AllowedImports[2] != "numpy" ||
		admission.RegionSpan.StartLine == 0 || admission.RegionSpan.EndLine < admission.RegionSpan.StartLine {
		return ErrBinding
	}
	for _, value := range []string{
		admission.DeclarationSHA256, admission.SourceSHA256, admission.ASTSHA256, admission.AnalysisSHA256,
		admission.InputsSHA256, admission.ArtifactSHA256, admission.ExecutionProfileSHA256,
		admission.ImportClosureSHA256, admission.CapabilityPlanSHA256, admission.PassConfigSHA256,
		admission.PassRegistrationSHA256, admission.IdentitySHA256,
	} {
		if !digestPattern.MatchString(value) {
			return ErrBinding
		}
	}
	identity := admission
	identity.IdentitySHA256 = ""
	if admission.IdentitySHA256 != digestJSON(admissionIdentity(identity)) {
		return ErrBinding
	}
	return nil
}

func validBindings(bindings Bindings) bool {
	return bindings.ExecutionProfileID == "numpy-core" && digestPattern.MatchString(bindings.ArtifactSHA256) &&
		digestPattern.MatchString(bindings.ExecutionProfileSHA256) && digestPattern.MatchString(bindings.ImportClosureSHA256) &&
		digestPattern.MatchString(bindings.CapabilityPlanSHA256)
}

type executionResponse struct {
	Status        string          `json:"status"`
	Error         json.RawMessage `json:"error"`
	Result        json.RawMessage `json:"result"`
	ResultPresent bool            `json:"result_present"`
	Metrics       struct {
		CapabilityCalls uint64 `json:"capability_calls"`
	} `json:"metrics"`
	SourceContract struct {
		ModelSourceSHA256 string `json:"model_source_sha256"`
	} `json:"source_contract"`
}

func ValidateExecutionResponse(execution VerifiedExecution, admission Admission) (json.RawMessage, PublicationGuard, error) {
	if len(execution.response) == 0 || !executionPropertiesMatch(execution.properties, admission) {
		return nil, PublicationGuard{}, ErrExecution
	}
	return validateExecutionResponse(execution.response, admission, true)
}

func validateExecutionResponse(raw []byte, admission Admission, authorityBound bool) (json.RawMessage, PublicationGuard, error) {
	if !authorityBound || len(raw) == 0 || len(raw) > MaxExecutionResponseBytes || admission.Validate() != nil {
		return nil, PublicationGuard{}, ErrExecution
	}
	var response executionResponse
	if json.Unmarshal(raw, &response) != nil || response.Status != "ok" || !bytes.Equal(response.Error, []byte("null")) ||
		!response.ResultPresent || len(response.Result) == 0 || bytes.Equal(response.Result, []byte("null")) ||
		response.Metrics.CapabilityCalls != 0 || response.SourceContract.ModelSourceSHA256 != admission.SourceSHA256 {
		return nil, PublicationGuard{}, ErrExecution
	}
	return append(json.RawMessage(nil), response.Result...), resultblob.NewPublicationGuard(publicationauth.Mint()), nil
}

func Digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func digestJSON(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return Digest(raw)
}

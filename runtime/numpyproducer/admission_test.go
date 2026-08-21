package numpyproducer

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/internal/publicationauth"
	"github.com/bkmashiro/agent-python-runtime/runtime/numpycodec"
	"github.com/bkmashiro/agent-python-runtime/runtime/semantic"
)

const (
	digestA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	digestB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func validDeclaration(t *testing.T) ([]byte, Declaration, string) {
	t.Helper()
	raw, declaration, err := SealDeclaration(DeclarationInput{Rows: 2, Cols: 3, Start: 1, Step: 2, Add: 5})
	if err != nil {
		t.Fatal(err)
	}
	source, err := RenderSource(declaration)
	if err != nil {
		t.Fatal(err)
	}
	return raw, declaration, source
}

func testBindings() Bindings {
	return Bindings{
		ArtifactSHA256: digestA, ExecutionProfileID: "numpy-core", ExecutionProfileSHA256: digestB,
		ImportClosureSHA256: digestA, CapabilityPlanSHA256: digestB,
	}
}

func testAnalysis(source string, bindings Bindings) semantic.Analysis {
	return semantic.Analysis{
		SchemaVersion: semantic.AnalysisSchemaVersion,
		SourceSHA256:  Digest([]byte(source)), ASTSHA256: digestA, AnalyzerSHA256: semantic.AnalyzerIdentity(),
		ArtifactSHA256: bindings.ArtifactSHA256, ExecutionProfileSHA256: bindings.ExecutionProfileSHA256,
		ImportClosureSHA256: bindings.ImportClosureSHA256, CapabilityPlanSHA256: bindings.CapabilityPlanSHA256,
		ModuleSpan:    semantic.SourceSpan{StartLine: 1, StartColumn: 0, EndLine: 20, EndColumn: 1},
		ModuleEffects: semantic.EffectSummary{MayBeUnknown: true}, Functions: []semantic.FunctionSummary{},
		Barriers: []semantic.Barrier{}, CallSiteCoverage: "positive_only", CallSites: []semantic.CallSite{},
		CandidateRegionCoverage: "module_top_level_complete", CandidateRegions: []semantic.CandidateRegion{},
	}
}

func TestExactDeclarationSourceAndAnalysisAreAdmitted(t *testing.T) {
	raw, declaration, source := validDeclaration(t)
	decoded, err := DecodeDeclaration(raw)
	if err != nil || decoded != declaration {
		t.Fatalf("decoded=%+v err=%v", decoded, err)
	}
	bindings := testBindings()
	admission, err := admitAnalysis(raw, source, testAnalysis(source, bindings), bindings)
	if err != nil {
		t.Fatal(err)
	}
	if admission.DeclarationSHA256 != declaration.IdentitySHA256 || admission.SourceSHA256 != Digest([]byte(source)) ||
		admission.Operation != OperationArangeAffineI64 || admission.OutputName != "array" || !admission.NoExternalInputs ||
		admission.AnalyzerUnknownObserved != true || admission.ExecutionProfileID != "numpy-core" {
		t.Fatalf("admission=%+v", admission)
	}
	for _, required := range []string{"np.__version__ == '1.26.0b1'", "dtype=np.int64", ".reshape((2, 3))", ".copy(order='C')", "body_base64"} {
		if !strings.Contains(source, required) {
			t.Fatalf("source missing %q: %s", required, source)
		}
	}
}

func TestFrozenCampaignProducerClassesAreClosedAndBounded(t *testing.T) {
	cases := []struct {
		input     DeclarationInput
		operation string
		dtype     string
		bodyBytes uint64
		needle    string
	}{
		{DeclarationInput{Operation: OperationZerosF64, InputElements: 8192}, OperationZerosF64, "<f8", 65536, "np.zeros((8192,)"},
		{DeclarationInput{Operation: OperationAffineF64, InputElements: 131072}, OperationAffineF64, "<f8", 1048576, "base * 1.5 + 2.0"},
		{DeclarationInput{Operation: OperationSumI64, InputElements: 1048576}, OperationSumI64, "<i8", 8, "np.sum(base)"},
		{DeclarationInput{Operation: OperationMatmulF64, Rows: 256, Cols: 256}, OperationMatmulF64, "<f8", 524288, "base @ base.T"},
	}
	for _, candidate := range cases {
		raw, declaration, err := SealDeclaration(candidate.input)
		if err != nil {
			t.Fatalf("%s seal: %v", candidate.operation, err)
		}
		source, err := RenderSource(declaration)
		if err != nil || declaration.Operation != candidate.operation || declaration.DType != candidate.dtype ||
			declaration.BodyBytes != candidate.bodyBytes || !strings.Contains(source, candidate.needle) {
			t.Fatalf("%s declaration=%+v source=%q err=%v", candidate.operation, declaration, source, err)
		}
		if decoded, err := DecodeDeclaration(raw); err != nil || decoded != declaration {
			t.Fatalf("%s decode=%+v err=%v", candidate.operation, decoded, err)
		}
	}
	for _, input := range []DeclarationInput{
		{Operation: "generic_numpy", InputElements: 1},
		{Operation: OperationAffineF64, InputElements: numpycodec.MaxBodyBytes/8 + 1},
		{Operation: OperationSumI64, InputElements: 0},
		{Operation: OperationMatmulF64, Rows: 256, Cols: 255},
	} {
		if _, _, err := SealDeclaration(input); !errors.Is(err, ErrDeclaration) {
			t.Fatalf("invalid declaration admitted: %+v err=%v", input, err)
		}
	}
}

func TestDeclarationAndSourcePolicyRejectsDrift(t *testing.T) {
	raw, declaration, source := validDeclaration(t)
	bindings := testBindings()
	analysis := testAnalysis(source, bindings)
	for name, changed := range map[string]string{
		"random": source + "\nnp.random.rand()", "time": source + "\nimport time", "file": source + "\nopen('/tmp/x','wb')",
		"dynamic_import": source + "\n__import__('os')", "object_dtype": strings.Replace(source, "dtype=np.int64", "dtype=object", 1),
		"unknown_call": source + "\ncallback(array)", "external_input": strings.Replace(source, "stop = 13", "stop = inputs['stop']", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := admitAnalysis(raw, changed, analysis, bindings); !errors.Is(err, ErrSourcePolicy) {
				t.Fatalf("err=%v", err)
			}
		})
	}
	for _, input := range []DeclarationInput{
		{Rows: 0, Cols: 3, Start: 1, Step: 2},
		{Rows: 2, Cols: 3, Start: 1, Step: 0},
		{Rows: numpycodec.MaxBodyBytes, Cols: 2, Start: 1, Step: 1},
		{Rows: 2, Cols: 3, Start: 1<<62 - 1, Step: 1<<62 - 1},
	} {
		if _, _, err := SealDeclaration(input); !errors.Is(err, ErrDeclaration) {
			t.Fatalf("input=%+v err=%v", input, err)
		}
	}
	for _, invalid := range [][]byte{
		append([]byte(" "), raw...),
		bytes.Replace(raw, []byte(`"operation":"arange_affine_i64_v1"`), []byte(`"operation":"unknown"`), 1),
		bytes.Replace(raw, []byte(declaration.IdentitySHA256), []byte(digestB), 1),
	} {
		if _, err := DecodeDeclaration(invalid); !errors.Is(err, ErrDeclaration) {
			t.Fatalf("invalid=%s err=%v", invalid, err)
		}
	}
}

func TestFrozenAdversarialProducerSourcesRemainOutsideClosedLanguage(t *testing.T) {
	raw, _, _ := validDeclaration(t)
	bindings := testBindings()
	sources := []string{
		"import numpy as np\nvalue = np.asarray([object()], dtype=object)\nresult = 1\n",
		"import numpy as np\nbase = np.arange(64, dtype=np.float64).reshape((8, 8))\nvalue = base[:, ::2]\nresult = int(value.size)\n",
		"import numpy as np\nvalue = np.asfortranarray(np.arange(64, dtype=np.float64).reshape((8, 8)))\nresult = int(value.size)\n",
		"import numpy as np\nvalue = np.random.default_rng().random(8)\nresult = float(value[0])\n",
	}
	for _, source := range sources {
		analysis := testAnalysis(source, bindings)
		if _, err := admitAnalysis(raw, source, analysis, bindings); !errors.Is(err, ErrSourcePolicy) {
			t.Fatalf("frozen adversarial producer admitted: %v", err)
		}
	}
}

func TestAdmissionRejectsAnalysisAndBindingDrift(t *testing.T) {
	raw, _, source := validDeclaration(t)
	bindings := testBindings()
	base := testAnalysis(source, bindings)
	mutations := []func(*semantic.Analysis){
		func(a *semantic.Analysis) { a.SourceSHA256 = digestB },
		func(a *semantic.Analysis) { a.ArtifactSHA256 = digestB },
		func(a *semantic.Analysis) { a.ExecutionProfileSHA256 = digestA },
		func(a *semantic.Analysis) { a.ImportClosureSHA256 = digestB },
		func(a *semantic.Analysis) { a.CapabilityPlanSHA256 = digestA },
		func(a *semantic.Analysis) { a.AnalyzerSHA256 = digestB },
		func(a *semantic.Analysis) { a.ModuleEffects.MayPublish = true },
		func(a *semantic.Analysis) {
			a.CallSites = []semantic.CallSite{{ID: digestA, Span: semantic.SourceSpan{StartLine: 1, EndLine: 1}, Capability: "x.y", ControlRegionID: digestA, CanonicalArguments: json.RawMessage(`[]`)}}
		},
	}
	for index, mutate := range mutations {
		analysis := base
		mutate(&analysis)
		if _, err := admitAnalysis(raw, source, analysis, bindings); err == nil {
			t.Fatalf("mutation %d admitted", index)
		}
	}
	profileDrift := bindings
	profileDrift.ExecutionProfileID = "base"
	if _, err := admitAnalysis(raw, source, base, profileDrift); !errors.Is(err, ErrBinding) {
		t.Fatalf("profile drift err=%v", err)
	}
}

func TestExportedAdmitRejectsZeroVerifiedAnalysis(t *testing.T) {
	raw, declaration, _ := validDeclaration(t)
	source, _ := RenderSource(declaration)
	if _, err := Admit(raw, source, semantic.VerifiedAnalysis{}, testBindings()); !errors.Is(err, ErrAnalysis) {
		t.Fatalf("zero verified analysis err=%v", err)
	}
}

func TestFabricatedAdmissionWithoutOpaqueProvenanceIsRejected(t *testing.T) {
	raw, _, source := validDeclaration(t)
	bindings := testBindings()
	admission, err := admitAnalysis(raw, source, testAnalysis(source, bindings), bindings)
	if err != nil {
		t.Fatal(err)
	}
	admission.provenance = publicationauth.Token{}
	if !errors.Is(admission.Validate(), ErrBinding) {
		t.Fatal("fabricated admission validated")
	}
	response := map[string]any{
		"schema_version": "pysolate.agent-response.v1", "status": "ok", "result_present": true,
		"result": map[string]any{"ok": true}, "error": nil,
		"metrics":         map[string]any{"capability_calls": 0},
		"source_contract": map[string]any{"model_source_sha256": admission.SourceSHA256},
	}
	encoded, _ := json.Marshal(response)
	if _, guard, err := validateExecutionResponse(encoded, admission, "run-1", true); !errors.Is(err, ErrExecution) || guard != (ProducerAuthority{}) {
		t.Fatalf("fabricated admission guard=%+v err=%v", guard, err)
	}
}

func TestExecutionResponseMustBeSuccessfulAuthorityBoundAndSourceBound(t *testing.T) {
	raw, _, source := validDeclaration(t)
	bindings := testBindings()
	admission, err := admitAnalysis(raw, source, testAnalysis(source, bindings), bindings)
	if err != nil {
		t.Fatal(err)
	}
	response := map[string]any{
		"status": "ok", "error": nil, "result_present": true, "result": map[string]any{"schema_version": numpycodec.ProducerValueSchemaVersion},
		"metrics":         map[string]any{"capability_calls": 0},
		"source_contract": map[string]any{"model_source_sha256": admission.SourceSHA256},
	}
	encoded, _ := json.Marshal(response)
	if _, guard, err := validateExecutionResponse(encoded, admission, "run-1", false); !errors.Is(err, ErrExecution) || guard != (ProducerAuthority{}) {
		t.Fatalf("unbound execution guard=%+v err=%v", guard, err)
	}
	result, guard, err := validateExecutionResponse(encoded, admission, "run-1", true)
	if err != nil || len(result) == 0 || guard == (ProducerAuthority{}) {
		t.Fatalf("result=%s guard=%+v err=%v", result, guard, err)
	}
	for name, mutate := range map[string]func(map[string]any){
		"error":          func(v map[string]any) { v["status"] = "error" },
		"missing_result": func(v map[string]any) { v["result_present"] = false },
		"effect":         func(v map[string]any) { v["metrics"] = map[string]any{"capability_calls": 1} },
		"source":         func(v map[string]any) { v["source_contract"] = map[string]any{"model_source_sha256": digestB} },
	} {
		t.Run(name, func(t *testing.T) {
			copyValue := map[string]any{}
			for key, value := range response {
				copyValue[key] = value
			}
			mutate(copyValue)
			encoded, _ := json.Marshal(copyValue)
			if _, guard, err := validateExecutionResponse(encoded, admission, "run-1", true); err == nil || guard != (ProducerAuthority{}) {
				t.Fatalf("guard=%+v err=%v", guard, err)
			}
		})
	}
}

package runtime

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestCompareSourceCompatibilityObservesOnlyStaticImportRoots(t *testing.T) {
	request := sourceCompatibilityRequest(t, `
# import subprocess
message = "import socket"
import json.decoder as decoder
import base64, \
    hashlib
from statistics import (
    mean,
)
if inputs.get("csv"):
    import csv
result = decoder.JSONDecoder().decode("1")
`, []string{"statistics", "json.decoder", "csv", "base64", "hashlib"})
	result := CompareSourceCompatibility(request, sourceCompatibilityProfile(t, "agent_runtime", "base64", "csv", "hashlib", "json", "statistics", "sys"))
	if result.Status() != SourceCompatible || result.SyntaxChecked() {
		t.Fatalf("status=%q syntax_checked=%v", result.Status(), result.SyntaxChecked())
	}
	want := []string{"base64", "csv", "hashlib", "json", "statistics"}
	if got := result.ObservedImports(); !reflect.DeepEqual(got, want) {
		t.Fatalf("observed=%v want=%v", got, want)
	}
	if len(result.UndeclaredImports()) != 0 || len(result.UnqualifiedImports()) != 0 || len(result.IndeterminateReasons()) != 0 {
		t.Fatalf("unexpected result: %s", mustCompatibilityJSON(t, result))
	}
}

func TestCompareSourceCompatibilityMarksDynamicAndRelativeImportsIndeterminate(t *testing.T) {
	for name, code := range map[string]string{
		"dunder import": `result = __import__(inputs["module"])`,
		"dunder alias":  `loader = __import__; result = loader(inputs["module"])`,
		"import module": `result = import_module(inputs["module"])`,
		"eval":          `result = eval(inputs["expression"])`,
		"exec":          `exec(inputs["code"])`,
		"relative":      `from .helpers import run\nresult = run()`,
	} {
		t.Run(name, func(t *testing.T) {
			request := sourceCompatibilityRequest(t, code, []string{"json"})
			result := CompareSourceCompatibility(request, sourceCompatibilityProfile(t, "agent_runtime", "json", "sys"))
			if result.Status() != SourceIndeterminate || len(result.IndeterminateReasons()) == 0 {
				t.Fatalf("result=%s", mustCompatibilityJSON(t, result))
			}
		})
	}
}

func TestCompareSourceCompatibilityMakesKnownMismatchUnsupported(t *testing.T) {
	request := sourceCompatibilityRequest(t, "import subprocess\nimport json\nresult = 1", []string{"json"})
	result := CompareSourceCompatibility(request, sourceCompatibilityProfile(t, "agent_runtime", "json", "sys"))
	if result.Status() != SourceUnsupported {
		t.Fatalf("result=%s", mustCompatibilityJSON(t, result))
	}
	if got := result.UndeclaredImports(); !reflect.DeepEqual(got, []string{"subprocess"}) {
		t.Fatalf("undeclared=%v", got)
	}
	if got := result.UnqualifiedImports(); !reflect.DeepEqual(got, []string{"subprocess"}) {
		t.Fatalf("unqualified=%v", got)
	}
}

func TestCompareSourceCompatibilityIsExplicitlyNotSyntaxValidation(t *testing.T) {
	request := sourceCompatibilityRequest(t, "result =", []string{})
	result := CompareSourceCompatibility(request, sourceCompatibilityProfile(t, "agent_runtime", "json", "sys"))
	if result.Status() != SourceCompatible || result.SyntaxChecked() {
		t.Fatalf("result=%s", mustCompatibilityJSON(t, result))
	}
}

func TestCompatibilityResultIsCanonicalAndReturnsDefensiveCopies(t *testing.T) {
	request := sourceCompatibilityRequest(t, "import json\nresult = 1", []string{"json"})
	profile := sourceCompatibilityProfile(t, "agent_runtime", "json", "sys")
	first := CompareSourceCompatibility(request, profile)
	second := CompareSourceCompatibility(request, profile)
	imports := first.ObservedImports()
	imports[0] = "mutated"
	if got := first.ObservedImports(); !reflect.DeepEqual(got, []string{"json"}) {
		t.Fatalf("mutable imports=%v", got)
	}
	if first.SourceSHA256() == "" || first.EvidenceSHA256() == "" || first.EvidenceSHA256() != second.EvidenceSHA256() {
		t.Fatalf("digests source=%q evidence=%q second=%q", first.SourceSHA256(), first.EvidenceSHA256(), second.EvidenceSHA256())
	}
	if first.ArtifactSHA256() == "" || first.ManifestSHA256() == "" || first.Validate() != nil {
		t.Fatalf("bound identity artifact=%q manifest=%q validation=%v", first.ArtifactSHA256(), first.ManifestSHA256(), first.Validate())
	}
	tampered := first
	tampered.status = SourceIndeterminate
	if tampered.Validate() == nil {
		t.Fatal("tampered immutable result validated")
	}
	var document map[string]any
	if err := json.Unmarshal([]byte(mustCompatibilityJSON(t, first)), &document); err != nil {
		t.Fatal(err)
	}
	if document["schema_version"] != float64(1) || document["analyzer"] != "conservative-python-imports-v1" || document["syntax_checked"] != false {
		t.Fatalf("document=%v", document)
	}
	for _, field := range []string{"declared_imports", "observed_imports", "undeclared_imports", "unqualified_imports", "indeterminate_reasons"} {
		if document[field] == nil {
			t.Fatalf("%s serialized as null: %v", field, document)
		}
	}
}

func TestCompareSourceCompatibilityRetainsKnownImportsAfterLexicalAmbiguity(t *testing.T) {
	request := sourceCompatibilityRequest(t, "message = 'unterminated\nimport math\n", []string{"json"})
	result := CompareSourceCompatibility(request, sourceCompatibilityProfile(t, "json"))
	if result.Status() != SourceUnsupported || !reflect.DeepEqual(result.ObservedImports(), []string{"math"}) ||
		!reflect.DeepEqual(result.IndeterminateReasons(), []string{"lexically_ambiguous"}) {
		t.Fatalf("result=%s", mustCompatibilityJSON(t, result))
	}
}

func TestCompareSourceCompatibilityBoundsObservedImportEvidence(t *testing.T) {
	var source strings.Builder
	for index := 0; index < maxObservedSourceImports+10; index++ {
		source.WriteString("import module_")
		source.WriteString(strconv.Itoa(index))
		source.WriteByte('\n')
	}
	request := sourceCompatibilityRequest(t, source.String(), []string{})
	result := CompareSourceCompatibility(request, sourceCompatibilityProfile(t, "json"))
	if len(result.ObservedImports()) != maxObservedSourceImports || !reflect.DeepEqual(result.IndeterminateReasons(), []string{"import_set_too_large"}) {
		t.Fatalf("observed=%d reasons=%v", len(result.ObservedImports()), result.IndeterminateReasons())
	}
}

func sourceCompatibilityRequest(t *testing.T, code string, imports []string) RunRequest {
	t.Helper()
	request := RunRequest{
		RunID:         "source-compatibility",
		Code:          code,
		Inputs:        json.RawMessage(`{}`),
		Compatibility: &CompatibilityDeclaration{Profile: "base", Imports: imports},
	}
	if err := ValidateCompatibilityDeclaration(request.Compatibility); err != nil {
		t.Fatal(err)
	}
	return request
}

func sourceCompatibilityProfile(t *testing.T, qualified ...string) ExecutionProfile {
	t.Helper()
	profile, err := NewExecutionProfile("base", qualified)
	if err != nil {
		t.Fatal(err)
	}
	identity := VerifiedArtifactIdentity{
		ProfileID: "base", ArtifactSHA256: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ManifestSHA256: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		ImportRoots: qualified, QualifiedImportRoots: qualified,
	}
	bound, err := profile.BindVerifiedArtifact(identity)
	if err != nil {
		t.Fatal(err)
	}
	return bound
}

func mustCompatibilityJSON(t *testing.T, result CompatibilityResult) string {
	t.Helper()
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

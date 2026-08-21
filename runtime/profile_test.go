package runtime

import (
	"errors"
	"strings"
	"testing"
)

func TestCompatibilityDeclarationIsBoundedAndStrict(t *testing.T) {
	request, err := DecodeRunRequest([]byte(`{"run_id":"run-1","code":"import json\nresult=1","inputs":{},"compatibility":{"profile":"base","imports":["json"]}}`))
	if err != nil || request.Compatibility == nil {
		t.Fatalf("request=%+v err=%v", request, err)
	}
	for name, raw := range map[string]string{
		"null":              `{"run_id":"r","code":"result=1","inputs":{},"compatibility":null}`,
		"empty profile":     `{"run_id":"r","code":"result=1","inputs":{},"compatibility":{"profile":"","imports":[]}}`,
		"duplicate imports": `{"run_id":"r","code":"result=1","inputs":{},"compatibility":{"profile":"base","imports":["json","json"]}}`,
		"dotted import":     `{"run_id":"r","code":"result=1","inputs":{},"compatibility":{"profile":"base","imports":["json.decoder"]}}`,
		"unknown field":     `{"run_id":"r","code":"result=1","inputs":{},"compatibility":{"profile":"base","imports":[],"install":true}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeRunRequest([]byte(raw)); err == nil {
				t.Fatal("invalid compatibility declaration accepted")
			}
		})
	}
}

func TestRunCompatibilityRequiresExactStaticImports(t *testing.T) {
	profile, err := NewExecutionProfile("base", []string{"json", "math"})
	if err != nil {
		t.Fatal(err)
	}
	admitted := RunRequest{Code: "import json, math\nresult = 1", Compatibility: &CompatibilityDeclaration{Profile: "base", Imports: []string{"json", "math"}}}
	if err := EvaluateRunCompatibility(admitted, &profile); err != nil {
		t.Fatal(err)
	}
	for name, request := range map[string]RunRequest{
		"missing declaration": {Code: admitted.Code, Compatibility: &CompatibilityDeclaration{Profile: "base", Imports: []string{"json"}}},
		"dynamic import":      {Code: "result=__import__('json')", Compatibility: &CompatibilityDeclaration{Profile: "base", Imports: []string{"json"}}},
		"late import":         {Code: "result=1\nimport json", Compatibility: &CompatibilityDeclaration{Profile: "base", Imports: []string{"json"}}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := EvaluateRunCompatibility(request, &profile); err == nil {
				t.Fatal("unsafe source admitted")
			}
		})
	}
}

func TestExecutionProfileAdmissionIsHostOwned(t *testing.T) {
	profile, err := NewExecutionProfile("base", []string{"json"})
	if err != nil {
		t.Fatal(err)
	}
	request := RunRequest{Code: "import json", Compatibility: &CompatibilityDeclaration{Profile: "base", Imports: []string{"json"}}}
	if !errors.Is(EvaluateRunCompatibility(request, nil), ErrExecutionProfileUnsupported) {
		t.Fatal("unbound profile accepted")
	}
	request.Compatibility.Imports = []string{"subprocess"}
	if !errors.Is(EvaluateRunCompatibility(request, &profile), ErrExecutionProfileUnsupported) {
		t.Fatal("unsupported import accepted")
	}
	if err := EvaluateRunCompatibility(RunRequest{}, nil); err != nil {
		t.Fatalf("import-free legacy request rejected: %v", err)
	}
}

func TestExecutionProfileAcceptsOnlyNamedArtifactProfiles(t *testing.T) {
	for _, id := range []string{"base", "attrs-770", "numpy-core"} {
		if _, err := NewExecutionProfile(id, []string{"json"}); err != nil {
			t.Fatalf("profile %q rejected: %v", id, err)
		}
	}
	if _, err := NewExecutionProfile("custom", []string{"json"}); err == nil {
		t.Fatal("unknown profile accepted")
	}
}

func TestExecutionProfileRejectsInvalidPolicy(t *testing.T) {
	for _, test := range []struct {
		id      string
		imports []string
	}{
		{"", []string{"json"}}, {"custom", []string{"json"}}, {"base", nil},
		{"base", []string{"json", "json"}}, {"base", []string{"json.*"}},
		{"base", []string{strings.Repeat("a", maxImportRootLength+1)}},
	} {
		if _, err := NewExecutionProfile(test.id, test.imports); err == nil {
			t.Fatalf("invalid policy accepted: %+v", test)
		}
	}
}

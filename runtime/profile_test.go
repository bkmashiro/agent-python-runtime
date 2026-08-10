package runtime

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestCompatibilityDeclarationIsBoundedAndStrict(t *testing.T) {
	request, err := DecodeRunRequest([]byte(`{"run_id":"run-1","code":"import json\nresult=1","inputs":{},"compatibility":{"profile":"base","imports":["json","statistics"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if request.Compatibility == nil || request.Compatibility.Profile != "base" || len(request.Compatibility.Imports) != 2 {
		t.Fatalf("compatibility=%+v", request.Compatibility)
	}
	for name, raw := range map[string]string{
		"null":              `{"run_id":"r","code":"result=1","inputs":{},"compatibility":null}`,
		"empty profile":     `{"run_id":"r","code":"result=1","inputs":{},"compatibility":{"profile":"","imports":[]}}`,
		"bad profile":       `{"run_id":"r","code":"result=1","inputs":{},"compatibility":{"profile":"../base","imports":[]}}`,
		"null imports":      `{"run_id":"r","code":"result=1","inputs":{},"compatibility":{"profile":"base","imports":null}}`,
		"duplicate imports": `{"run_id":"r","code":"result=1","inputs":{},"compatibility":{"profile":"base","imports":["json","json"]}}`,
		"bad import":        `{"run_id":"r","code":"result=1","inputs":{},"compatibility":{"profile":"base","imports":["../json"]}}`,
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

func TestAdmitRunCompatibilityRejectsUndeclaredStaticImport(t *testing.T) {
	profile, err := NewExecutionProfile("base", []string{"json", "math"})
	if err != nil {
		t.Fatal(err)
	}
	request, err := DecodeRunRequest([]byte(`{"run_id":"run","code":"import json, math\nresult=1","inputs":{},"compatibility":{"profile":"base","imports":["json"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	result, err := EvaluateRunCompatibility(request, &profile)
	var sourceErr *SourceCompatibilityError
	if !errors.As(err, &sourceErr) || !errors.Is(err, ErrExecutionProfileUnsupported) || !errors.Is(err, ErrSourceCompatibilityUnsupported) || result.Status() != SourceUnsupported {
		t.Fatalf("result=%v err=%v", result.Status(), err)
	}
	if got := result.UndeclaredImports(); !reflect.DeepEqual(got, []string{"math"}) {
		t.Fatalf("undeclared=%v", got)
	}
}

func TestAdmitRunCompatibilityRejectsDynamicImportAsUnsupported(t *testing.T) {
	profile, err := NewExecutionProfile("base", []string{"json"})
	if err != nil {
		t.Fatal(err)
	}
	request, err := DecodeRunRequest([]byte(`{"run_id":"run","code":"result=__import__(inputs['module'])","inputs":{},"compatibility":{"profile":"base","imports":["json"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	result, err := EvaluateRunCompatibility(request, &profile)
	var sourceErr *SourceCompatibilityError
	if !errors.As(err, &sourceErr) || !errors.Is(err, ErrSourceCompatibilityUnsupported) || result.Status() != SourceUnsupported {
		t.Fatalf("result=%v err=%v", result.Status(), err)
	}
}

func TestExecutionProfileAdmissionIsHostBoundAndFailClosed(t *testing.T) {
	profile, err := NewExecutionProfile("base", []string{"json", "math", "statistics"})
	if err != nil {
		t.Fatal(err)
	}
	request := RunRequest{Code: "import json, math\nresult = 1", Compatibility: &CompatibilityDeclaration{Profile: "base", Imports: []string{"json", "math"}}}
	if err := AdmitRunCompatibility(request, &profile); err != nil {
		t.Fatalf("declared imports rejected: %v", err)
	}
	for name, test := range map[string]struct {
		request RunRequest
		profile *ExecutionProfile
	}{
		"unbound Host profile": {request: request},
		"wrong profile":        {request: RunRequest{Compatibility: &CompatibilityDeclaration{Profile: "numpy-core", Imports: []string{"json"}}}, profile: &profile},
		"unsupported import":   {request: RunRequest{Compatibility: &CompatibilityDeclaration{Profile: "base", Imports: []string{"subprocess"}}}, profile: &profile},
	} {
		t.Run(name, func(t *testing.T) {
			err := AdmitRunCompatibility(test.request, test.profile)
			if !errors.Is(err, ErrExecutionProfileUnsupported) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	if err := AdmitRunCompatibility(RunRequest{}, nil); err != nil {
		t.Fatalf("legacy request without a declaration was rejected: %v", err)
	}
}

func TestExecutionProfileRejectsInvalidHostPolicy(t *testing.T) {
	for _, test := range []struct {
		name    string
		id      string
		imports []string
	}{
		{"empty ID", "", []string{"json"}},
		{"unknown ID", "custom", []string{"json"}},
		{"empty import set", "base", nil},
		{"duplicate imports", "base", []string{"json", "json"}},
		{"bad import", "base", []string{"json.*"}},
		{"qualified root", "base", []string{"json.decoder"}},
		{"oversized root", "base", []string{strings.Repeat("a", maxImportRootLength+1)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewExecutionProfile(test.id, test.imports); err == nil {
				t.Fatal("invalid Host profile accepted")
			}
		})
	}
}

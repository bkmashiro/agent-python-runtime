package runtime

import (
	"errors"
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
		"unknown field":     `{"run_id":"r","code":"result=1","inputs":{},"compatibility":{"profile":"base","imports":[],"install":true}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeRunRequest([]byte(raw)); err == nil {
				t.Fatal("invalid compatibility declaration accepted")
			}
		})
	}
}

func TestExecutionProfileAdmissionIsHostBoundAndFailClosed(t *testing.T) {
	profile, err := NewExecutionProfile("base", []string{"json", "math", "statistics"})
	if err != nil {
		t.Fatal(err)
	}
	request := RunRequest{Compatibility: &CompatibilityDeclaration{Profile: "base", Imports: []string{"json", "json.decoder", "math"}}}
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
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewExecutionProfile(test.id, test.imports); err == nil {
				t.Fatal("invalid Host profile accepted")
			}
		})
	}
}

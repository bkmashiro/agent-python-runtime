package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestUnsupportedReasonAcceptsStableCaptureAndResumeCodes(t *testing.T) {
	cases := []UnsupportedReason{
		{SchemaVersion: 1, Operation: OperationCapture, Code: ReasonNotQuiescent},
		{SchemaVersion: 1, Operation: OperationCapture, Code: ReasonMutableStateUnsupported, StateClass: StateClassMutableGlobal},
		{SchemaVersion: 1, Operation: OperationCapture, Code: ReasonExternalResourceActive},
		{SchemaVersion: 1, Operation: OperationCapture, Code: ReasonLeaseConflict},
		{SchemaVersion: 1, Operation: OperationResume, Code: ReasonArtifactMismatch},
		{SchemaVersion: 1, Operation: OperationResume, Code: ReasonBaseMismatch},
		{SchemaVersion: 1, Operation: OperationResume, Code: ReasonRuntimeMismatch},
		{SchemaVersion: 1, Operation: OperationResume, Code: ReasonArchitectureMismatch},
		{SchemaVersion: 1, Operation: OperationResume, Code: ReasonCapsuleInvalid},
		{SchemaVersion: 1, Operation: OperationResume, Code: ReasonLeaseConflict},
	}
	for _, reason := range cases {
		if err := reason.Validate(); err != nil {
			t.Fatalf("valid reason %#v rejected: %v", reason, err)
		}
	}
}

func TestUnsupportedReasonRejectsUnknownOrIncoherentClaims(t *testing.T) {
	cases := []UnsupportedReason{
		{},
		{SchemaVersion: 2, Operation: OperationCapture, Code: ReasonNotQuiescent},
		{SchemaVersion: 1, Operation: Operation("delete"), Code: ReasonNotQuiescent},
		{SchemaVersion: 1, Operation: OperationCapture, Code: ReasonCode("best_effort")},
		{SchemaVersion: 1, Operation: OperationCapture, Code: ReasonMutableStateUnsupported},
		{SchemaVersion: 1, Operation: OperationCapture, Code: ReasonNotQuiescent, StateClass: StateClassMutableGlobal},
		{SchemaVersion: 1, Operation: OperationResume, Code: ReasonNotQuiescent},
		{SchemaVersion: 1, Operation: OperationCapture, Code: ReasonArtifactMismatch},
		{SchemaVersion: 1, Operation: OperationResume, Code: ReasonMutableStateUnsupported, StateClass: StateClass("python_heap")},
	}
	for _, reason := range cases {
		if err := reason.Validate(); !errors.Is(err, ErrInvalidUnsupportedReason) {
			t.Fatalf("expected invalid reason for %#v, got %v", reason, err)
		}
	}
}

func TestUnsupportedReasonJSONIsBoundedAndCarriesNoAuthority(t *testing.T) {
	reason := UnsupportedReason{
		SchemaVersion: 1,
		Operation:     OperationCapture,
		Code:          ReasonMutableStateUnsupported,
		StateClass:    StateClassWASIResource,
	}
	data, err := json.Marshal(reason)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	if len(object) != 4 {
		t.Fatalf("unexpected reason fields: %#v", object)
	}
	for _, forbidden := range []string{"session_id", "credentials", "capabilities", "payload", "message"} {
		if _, ok := object[forbidden]; ok {
			t.Fatalf("unsupported reason exposed forbidden field %q", forbidden)
		}
	}
}

func TestUnsupportedReasonSchemaFixtures(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve test file")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(filename), "../../session/v1"))
	schemaPath := filepath.Join(root, "unsupported-reason.schema.json")
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	const schemaURL = "https://agent-runtime.dev/session/v1/unsupported-reason.schema.json"
	if err := compiler.AddResource(schemaURL, document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(schemaURL)
	if err != nil {
		t.Fatal(err)
	}
	for _, validity := range []string{"valid", "invalid"} {
		paths, err := filepath.Glob(filepath.Join(root, "fixtures", validity, "*.json"))
		if err != nil {
			t.Fatal(err)
		}
		if len(paths) == 0 {
			t.Fatalf("no %s fixtures", validity)
		}
		for _, path := range paths {
			fixtureData, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var fixture any
			if err := json.Unmarshal(fixtureData, &fixture); err != nil {
				t.Fatal(err)
			}
			err = schema.Validate(fixture)
			if validity == "valid" && err != nil {
				t.Fatalf("valid fixture %s rejected: %v", filepath.Base(path), err)
			}
			if validity == "invalid" && err == nil {
				t.Fatalf("invalid fixture %s accepted", filepath.Base(path))
			}
		}
	}
}

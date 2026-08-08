package readonlyrecorder_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/verification/readonlyrecorder"
)

func TestCaptureCanonicalizesWebAndCLIObservations(t *testing.T) {
	web, err := readonlyrecorder.Capture(readonlyrecorder.Observation{Source: readonlyrecorder.SourceWeb, Locator: "https://example.invalid/dashboard", Payload: []byte(`{"status":"ok","items":[{"id":1,"active":true}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	cli, err := readonlyrecorder.Capture(readonlyrecorder.Observation{Source: readonlyrecorder.SourceCLI, Locator: "fixturectl status --json", Payload: []byte(` { "items" : [ { "active" : true, "id" : 1 } ], "status" : "ok" } `)})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(web.CanonicalPayload, cli.CanonicalPayload) || web.PayloadDigest != cli.PayloadDigest {
		t.Fatalf("web=%s cli=%s", web.CanonicalPayload, cli.CanonicalPayload)
	}
	if web.SourceIdentityDigest == cli.SourceIdentityDigest {
		t.Fatal("distinct source identities collapsed")
	}
}

func TestCaptureRejectsSensitiveFieldsValuesLocatorsAndDuplicateKeys(t *testing.T) {
	observations := []readonlyrecorder.Observation{
		{Source: readonlyrecorder.SourceWeb, Locator: "https://example.invalid", Payload: []byte(`{"password":"hidden"}`)},
		{Source: readonlyrecorder.SourceCLI, Locator: "fixturectl", Payload: []byte(`{"message":"Bearer abcdefghijklmnop"}`)},
		{Source: readonlyrecorder.SourceWeb, Locator: "https://example.invalid?api_key=hidden", Payload: []byte(`{"status":"ok"}`)},
		{Source: readonlyrecorder.SourceCLI, Locator: "fixturectl", Payload: []byte(`{"status":"ok","status":"changed"}`)},
	}
	for index, observation := range observations {
		if _, err := readonlyrecorder.Capture(observation); !errors.Is(err, readonlyrecorder.ErrSensitiveObservation) && !errors.Is(err, readonlyrecorder.ErrInvalidObservation) {
			t.Fatalf("case=%d err=%v", index, err)
		}
	}
}

func TestInferProducesValueFreeUntrustedReadOnlyContract(t *testing.T) {
	recording, err := readonlyrecorder.Capture(readonlyrecorder.Observation{Source: readonlyrecorder.SourceWeb, Locator: "https://example.invalid/profile", Payload: []byte(`{"display_name":"Alice","score":7,"tags":["admin","reader"]}`)})
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := readonlyrecorder.Infer(recording)
	if err != nil {
		t.Fatal(err)
	}
	if err := candidate.Validate(); err != nil {
		t.Fatal(err)
	}
	if candidate.Trust != readonlyrecorder.TrustUntrusted || !candidate.Authority.ReadOnly || candidate.Authority.Credentials || candidate.Authority.ToolExposure || candidate.Authority.Commit {
		t.Fatalf("candidate=%+v", candidate)
	}
	encoded, _ := json.Marshal(candidate)
	for _, leaked := range [][]byte{[]byte("Alice"), []byte("admin"), []byte("reader")} {
		if bytes.Contains(encoded, leaked) {
			t.Fatalf("candidate leaked sampled value %q: %s", leaked, encoded)
		}
	}
}

func TestCandidateRejectsAuthorityEscalation(t *testing.T) {
	recording, _ := readonlyrecorder.Capture(readonlyrecorder.Observation{Source: readonlyrecorder.SourceCLI, Locator: "fixturectl status", Payload: []byte(`{"status":"ok"}`)})
	candidate, _ := readonlyrecorder.Infer(recording)
	candidate.Authority.Commit = true
	if err := candidate.Validate(); !errors.Is(err, readonlyrecorder.ErrAuthorityEscalation) {
		t.Fatalf("err=%v", err)
	}
}

func TestInferEscapesLiteralWildcardObjectKey(t *testing.T) {
	candidate := contract(t, `{"*":"literal","items":[1]}`)
	paths := map[string]bool{}
	for _, field := range candidate.Fields {
		paths[field.Path] = true
	}
	if !paths["/~2"] || !paths["/items/*"] {
		t.Fatalf("fields=%+v", candidate.Fields)
	}
}

func TestCandidateRequiresRootAndRejectsSensitiveShape(t *testing.T) {
	candidate := contract(t, `{"status":"ok"}`)
	candidate.Fields = nil
	if err := candidate.Validate(); !errors.Is(err, readonlyrecorder.ErrInvalidCandidate) {
		t.Fatalf("missing root err=%v", err)
	}
	candidate = contract(t, `{"status":"ok"}`)
	candidate.Fields[1].Path = "/password"
	if err := candidate.Validate(); !errors.Is(err, readonlyrecorder.ErrInvalidCandidate) {
		t.Fatalf("sensitive path err=%v", err)
	}
}

func TestDetectDriftReportsAddedRemovedAndTypeChangedFields(t *testing.T) {
	baseline := contract(t, `{"id":1,"name":"item","legacy":true}`)
	current := contract(t, `{"id":"1","name":"item","extra":3}`)
	drift, err := readonlyrecorder.DetectDrift(baseline, current)
	if err != nil {
		t.Fatal(err)
	}
	if !drift.Changed || len(drift.Added) != 1 || drift.Added[0] != "/extra" || len(drift.Removed) != 1 || drift.Removed[0] != "/legacy" || len(drift.TypeChanged) != 1 || drift.TypeChanged[0].Path != "/id" {
		t.Fatalf("drift=%+v", drift)
	}
	if drift.Authority != (readonlyrecorder.AuthorityCeiling{ReadOnly: true}) {
		t.Fatalf("authority=%+v", drift.Authority)
	}
}

func TestInferRejectsTamperedRecording(t *testing.T) {
	recording, _ := readonlyrecorder.Capture(readonlyrecorder.Observation{Source: readonlyrecorder.SourceCLI, Locator: "fixturectl status", Payload: []byte(`{"status":"ok"}`)})
	recording.CanonicalPayload[0] = '['
	if _, err := readonlyrecorder.Infer(recording); !errors.Is(err, readonlyrecorder.ErrRecordingIntegrity) {
		t.Fatalf("err=%v", err)
	}
}

func contract(t *testing.T, payload string) readonlyrecorder.ContractCandidate {
	t.Helper()
	recording, err := readonlyrecorder.Capture(readonlyrecorder.Observation{Source: readonlyrecorder.SourceCLI, Locator: "fixturectl status", Payload: []byte(payload)})
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := readonlyrecorder.Infer(recording)
	if err != nil {
		t.Fatal(err)
	}
	return candidate
}

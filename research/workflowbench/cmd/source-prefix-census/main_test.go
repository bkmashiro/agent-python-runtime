package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
)

func testPlanDocument(t *testing.T) []byte {
	t.Helper()
	raw, err := json.Marshal(planDocument{
		SchemaVersion: "pysolate.capability-plan.v6", MaxCalls: 1,
		Capabilities: []capability.Spec{{
			Name: "fixture.read", Version: "v1", Description: "fixture", EffectClass: capability.EffectExternalRead,
			Playback: capability.PlaybackLiveOnly, HandlerIdentity: "fixture-handler",
			InputSchema:  json.RawMessage(`{"type":"object","properties":{"key":{"type":"string"}},"required":["key"],"additionalProperties":false}`),
			OutputSchema: json.RawMessage(`{"type":"object","additionalProperties":true}`),
			Python:       &capability.PythonProjection{Module: "fixture", Method: "read", Arguments: []string{"key"}},
		}},
		Grants: []capability.GrantBinding{{Capability: "fixture.read", PolicySHA256: digestBytes([]byte("grant"))}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestPlanProjectionsBindsDocumentAndEffect(t *testing.T) {
	raw := testPlanDocument(t)
	var indented bytes.Buffer
	if err := json.Indent(&indented, raw, "", "  "); err != nil {
		t.Fatal(err)
	}
	projections, effects, err := planProjections(indented.Bytes(), digestBytes(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(projections) != 1 || projections[0].Name != "fixture.read" || effects["fixture.read"] != capability.EffectExternalRead {
		t.Fatalf("projections=%+v effects=%+v", projections, effects)
	}
	if _, _, err := planProjections(raw, digestBytes([]byte("other"))); err == nil {
		t.Fatal("tampered plan identity accepted")
	}
}

func TestEventIdentityBindsTurnAndPlan(t *testing.T) {
	parent := digestBytes([]byte("parent"))
	a := eventIdentity(parent, "1", "2", digestBytes([]byte("source")), digestBytes([]byte("plan")))
	b := eventIdentity(parent, "1", "3", digestBytes([]byte("source")), digestBytes([]byte("plan")))
	if a == b || a == "" || b == "" {
		t.Fatalf("a=%q b=%q", a, b)
	}
}

func TestStrictDecodeRejectsUnknownFields(t *testing.T) {
	var request guestRequest
	if err := strictDecode([]byte(`{"run_id":"r","code":"result=1","inputs":{},"unknown":true}`), &request); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestWritePrivateUsesRestrictedMode(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private")
	path := filepath.Join(root, "evidence.json")
	if err := writePrivate(path, []byte("{}\n")); err != nil {
		t.Fatal(err)
	}
	directory, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if directory.Mode().Perm() != 0o700 || file.Mode().Perm() != 0o600 {
		t.Fatalf("directory=%#o file=%#o", directory.Mode().Perm(), file.Mode().Perm())
	}
}

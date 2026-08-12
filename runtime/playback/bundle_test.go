package playback_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/playback"
)

func TestBundleCanonicalRoundTripAndPrivacyBoundary(t *testing.T) {
	metadata := testMetadata()
	bundle, err := playback.New(metadata, []capability.TranscriptEntry{testEntry()})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := playback.Encode(bundle)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := playback.Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, err := playback.Encode(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, reencoded) || decoded.Identity == "" || decoded.Identity != bundle.Identity {
		t.Fatalf("non-canonical bundle\nfirst=%s\nsecond=%s", encoded, reencoded)
	}
	for _, forbidden := range []string{"agent-secret-source", "prompt-secret", "final-result-body", "workspace-secret", "credential-secret", "https://private.example"} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatalf("bundle leaked %q: %s", forbidden, encoded)
		}
	}
	if len(decoded.Entries) != 1 || string(decoded.Entries[0].Result) != `{"items":[]}` {
		t.Fatalf("decoded=%#v", decoded)
	}
}

func TestBundleRejectsTamperingAndAmbiguousJSON(t *testing.T) {
	bundle, err := playback.New(testMetadata(), []capability.TranscriptEntry{testEntry()})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := playback.Encode(bundle)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	mutations := map[string]func(map[string]any){
		"plan":    func(value map[string]any) { value["capability_plan_sha256"] = digest('9') },
		"grant":   func(value map[string]any) { value["grants"].([]any)[0].(map[string]any)["policy_sha256"] = digest('8') },
		"request": func(value map[string]any) { value["request_sha256"] = digest('7') },
		"order": func(value map[string]any) {
			value["entries"].([]any)[0].(map[string]any)["operation_index"] = float64(1)
		},
		"result": func(value map[string]any) {
			value["entries"].([]any)[0].(map[string]any)["result"] = map[string]any{"items": []any{"tampered"}}
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			copyBytes := append([]byte(nil), encoded...)
			var value map[string]any
			if err := json.Unmarshal(copyBytes, &value); err != nil {
				t.Fatal(err)
			}
			mutate(value)
			tampered, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := playback.Decode(tampered); err == nil {
				t.Fatalf("tampered %s accepted: %s", name, tampered)
			}
		})
	}
	if _, err := playback.Decode(append(encoded, []byte(" trailing")...)); err == nil {
		t.Fatal("trailing bytes accepted")
	}
	duplicate := []byte(strings.Replace(string(encoded), `"schema_version":`, `"schema_version":"duplicate","schema_version":`, 1))
	if _, err := playback.Decode(duplicate); err == nil {
		t.Fatal("duplicate key accepted")
	}
	invalidUTF8 := append([]byte(nil), encoded...)
	invalidUTF8[len(invalidUTF8)/2] = 0xff
	if _, err := playback.Decode(invalidUTF8); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
}

func TestBundleRejectsInvalidMetadataAndTranscript(t *testing.T) {
	metadata := testMetadata()
	metadata.ArtifactSHA256 = ""
	if _, err := playback.New(metadata, []capability.TranscriptEntry{testEntry()}); err == nil {
		t.Fatal("missing artifact identity accepted")
	}
	metadata = testMetadata()
	entry := testEntry()
	entry.ResultSHA256 = digest('3')
	if _, err := playback.New(metadata, []capability.TranscriptEntry{entry}); err == nil {
		t.Fatal("mismatched result digest accepted")
	}
	metadata = testMetadata()
	metadata.InitialWorkspaceSHA256 = digest('1')
	metadata.FinalWorkspaceSHA256 = ""
	if _, err := playback.New(metadata, nil); err == nil {
		t.Fatal("one-sided workspace identity accepted")
	}
}

func testMetadata() playback.Metadata {
	return playback.Metadata{
		CapabilityPlanSHA256: digest('a'), RequestSHA256: digest('b'), ArtifactSHA256: digest('c'),
		ExecutionProfileSHA256: digest('d'), ExpectedResultSHA256: digest('e'),
		InitialWorkspaceSHA256: digest('f'), FinalWorkspaceSHA256: digest('0'),
		Grants: []capability.GrantBinding{{Capability: "sources.demo_catalog", PolicySHA256: digest('1')}},
	}
}

func testEntry() capability.TranscriptEntry {
	return capability.TranscriptEntry{
		OperationIndex: 0, Capability: "sources.demo_catalog", Arguments: json.RawMessage(`{}`), ArgumentsSHA256: digestFor(`{}`),
		Result: json.RawMessage(`{"items":[]}`), ResultSHA256: digestFor(`{"items":[]}`),
		Evidence: capability.TransportEvidence{Kind: "http", Status: 200, MediaType: "application/json", BodyBytes: 12, BodySHA256: digest('2')},
	}
}

func digest(character byte) string { return "sha256:" + strings.Repeat(string(character), 64) }

func digestFor(value string) string {
	return playback.SHA256([]byte(value))
}

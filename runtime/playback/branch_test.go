package playback_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/playback"
)

func TestBranchManifestCanonicalRoundTripAndPrefixBinding(t *testing.T) {
	parent, err := playback.New(testMetadata(), []capability.TranscriptEntry{testEntry(), secondEntry()})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := playback.NewBranchManifest(playback.BranchMetadata{
		ParentBundleSHA256: parent.Identity, ForkOperation: 1,
		RequestSHA256: parent.RequestSHA256, ArtifactSHA256: parent.ArtifactSHA256,
		ExecutionProfileSHA256: parent.ExecutionProfileSHA256, InitialWorkspaceSHA256: parent.InitialWorkspaceSHA256,
		ChildCapabilityPlanSHA256: parent.CapabilityPlanSHA256, ChildGrants: parent.Grants,
		SuffixMode: playback.BranchOverride,
	}, parent, []capability.TranscriptEntry{overrideEntry()})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := playback.EncodeBranchManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := playback.DecodeBranchManifest(encoded)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, err := playback.EncodeBranchManifest(decoded)
	if err != nil || !bytes.Equal(encoded, reencoded) || decoded.Identity != manifest.Identity {
		t.Fatalf("decoded=%+v err=%v", decoded, err)
	}
	if err := decoded.ValidateParent(parent); err != nil {
		t.Fatal(err)
	}
	if decoded.PrefixSHA256 == "" || decoded.ParentBundleSHA256 != parent.Identity || decoded.ForkOperation != 1 {
		t.Fatalf("manifest=%+v", decoded)
	}
}

func TestBranchManifestRejectsParentPrefixAndSuffixTamper(t *testing.T) {
	parent, err := playback.New(testMetadata(), []capability.TranscriptEntry{testEntry(), secondEntry()})
	if err != nil {
		t.Fatal(err)
	}
	base := playback.BranchMetadata{
		ParentBundleSHA256: parent.Identity, ForkOperation: 1,
		RequestSHA256: parent.RequestSHA256, ArtifactSHA256: parent.ArtifactSHA256,
		ExecutionProfileSHA256: parent.ExecutionProfileSHA256, InitialWorkspaceSHA256: parent.InitialWorkspaceSHA256,
		ChildCapabilityPlanSHA256: parent.CapabilityPlanSHA256, ChildGrants: parent.Grants,
		SuffixMode: playback.BranchOverride,
	}
	if _, err := playback.NewBranchManifest(base, parent, nil); err == nil {
		t.Fatal("empty override suffix accepted")
	}
	base.ForkOperation = 2
	if _, err := playback.NewBranchManifest(base, parent, []capability.TranscriptEntry{overrideEntry()}); err == nil {
		t.Fatal("fork outside transcript accepted")
	}
	base.ForkOperation = 1
	manifest, err := playback.NewBranchManifest(base, parent, []capability.TranscriptEntry{overrideEntry()})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := playback.EncodeBranchManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(map[string]any){
		"parent": func(value map[string]any) { value["parent_bundle_sha256"] = digest('7') },
		"prefix": func(value map[string]any) { value["prefix_sha256"] = digest('8') },
		"plan":   func(value map[string]any) { value["child_capability_plan_sha256"] = digest('9') },
		"grant": func(value map[string]any) {
			value["child_grants"].([]any)[0].(map[string]any)["policy_sha256"] = digest('6')
		},
		"suffix-result": func(value map[string]any) {
			value["suffix_entries"].([]any)[0].(map[string]any)["result"] = map[string]any{"items": []any{}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			var candidate map[string]any
			if err := json.Unmarshal(encoded, &candidate); err != nil {
				t.Fatal(err)
			}
			mutate(candidate)
			tampered, err := json.Marshal(candidate)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := playback.DecodeBranchManifest(tampered); err == nil {
				t.Fatalf("tampered manifest accepted: %s", tampered)
			}
		})
	}
	if _, err := playback.DecodeBranchManifest(append(encoded, []byte(" trailing")...)); err == nil {
		t.Fatal("trailing data accepted")
	}
	duplicate := []byte(strings.Replace(string(encoded), `"schema_version":`, `"schema_version":"duplicate","schema_version":`, 1))
	if _, err := playback.DecodeBranchManifest(duplicate); err == nil {
		t.Fatal("duplicate key accepted")
	}
	invalid := append([]byte(nil), encoded...)
	invalid[len(invalid)/2] = 0xff
	if _, err := playback.DecodeBranchManifest(invalid); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}

	t.Run("in-memory-self-identity", func(t *testing.T) {
		tampered := manifest
		tampered.SuffixEntries = append([]capability.TranscriptEntry(nil), manifest.SuffixEntries...)
		tampered.SuffixEntries[0].Result = json.RawMessage(`{"items":[]}`)
		tampered.SuffixEntries[0].ResultSHA256 = playback.SHA256(tampered.SuffixEntries[0].Result)
		tampered.SuffixEntries[0].Evidence.BodyBytes = uint32(len(tampered.SuffixEntries[0].Result))
		tampered.SuffixEntries[0].Evidence.BodySHA256 = tampered.SuffixEntries[0].ResultSHA256
		if err := tampered.Validate(); err == nil || tampered.ValidateParent(parent) == nil {
			t.Fatal("mutated in-memory manifest retained stale lineage identity")
		}
	})

	t.Run("in-memory-parent-self-identity", func(t *testing.T) {
		tamperedParent := parent
		tamperedParent.ExpectedResultSHA256 = digest('5')
		if err := manifest.ValidateParent(tamperedParent); err == nil {
			t.Fatal("mutated in-memory parent retained stale Bundle identity")
		}
		if _, err := playback.NewBranchManifest(base, tamperedParent, []capability.TranscriptEntry{overrideEntry()}); err == nil {
			t.Fatal("mutated in-memory parent authored a branch")
		}
	})
}

func secondEntry() capability.TranscriptEntry {
	entry := testEntry()
	entry.OperationIndex = 1
	entry.Capability = "sources.benchmark_manifest"
	entry.Result = json.RawMessage(`{"cases":[],"schema_version":"pysolate.benchmark-manifest.v1","suite_id":"suite","title":"Suite","version":"1"}`)
	entry.ResultSHA256 = playback.SHA256(entry.Result)
	return entry
}

func overrideEntry() capability.TranscriptEntry {
	entry := secondEntry()
	entry.Result = json.RawMessage(`{"cases":[],"schema_version":"pysolate.benchmark-manifest.v1","suite_id":"alternate","title":"Alternate","version":"1"}`)
	entry.ResultSHA256 = playback.SHA256(entry.Result)
	entry.Evidence = capability.TransportEvidence{Kind: "branch_override", Status: 200, MediaType: "application/json", BodyBytes: uint32(len(entry.Result)), BodySHA256: playback.SHA256(entry.Result)}
	return entry
}

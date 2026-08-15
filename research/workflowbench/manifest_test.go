package workflowbench

import (
	"encoding/json"
	"testing"
)

func TestGenerateManifestIsSeededShuffledAndCoversTreatments(t *testing.T) {
	first, err := GenerateManifest(20260815, testIdentity())
	if err != nil {
		t.Fatal(err)
	}
	second, err := GenerateManifest(20260815, testIdentity())
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, _ := EncodeManifest(first)
	secondJSON, _ := EncodeManifest(second)
	if string(firstJSON) != string(secondJSON) {
		t.Fatal("same seed produced different manifest")
	}
	other, err := GenerateManifest(20260816, testIdentity())
	if err != nil {
		t.Fatal(err)
	}
	otherJSON, _ := EncodeManifest(other)
	if string(firstJSON) == string(otherJSON) {
		t.Fatal("different seed did not change submission order")
	}
	classes := map[string]int{}
	negativeDimensions := map[string]int{}
	for index, task := range first.Tasks {
		if task.SubmissionOrder != uint32(index+1) {
			t.Fatalf("order=%d task=%+v", index+1, task)
		}
		classes[task.Class]++
		if task.NegativeDimension != "" {
			negativeDimensions[task.NegativeDimension]++
		}
	}
	for _, class := range []string{"preissue", "declared_parallel", "coalesced", "retained_reuse", "near_match", "ordinary"} {
		if classes[class] == 0 {
			t.Fatalf("missing class %s: %v", class, classes)
		}
	}
	for _, dimension := range []string{"arguments", "freshness", "resource", "privacy", "authority", "source", "artifact", "workflow"} {
		if negativeDimensions[dimension] != 1 {
			t.Fatalf("negative dimension %s count=%d", dimension, negativeDimensions[dimension])
		}
	}
	decoded, err := DecodeManifest(firstJSON)
	if err != nil || decoded.Validate() != nil {
		t.Fatalf("decode err=%v", err)
	}
	var raw map[string]any
	_ = json.Unmarshal(firstJSON, &raw)
	raw["private_body"] = "secret"
	changed, _ := json.Marshal(raw)
	if _, err := DecodeManifest(changed); err == nil {
		t.Fatal("unknown body field admitted")
	}
}

func TestManifestMutationBreaksSealAndIdentity(t *testing.T) {
	manifest, err := GenerateManifest(20260815, testIdentity())
	if err != nil {
		t.Fatal(err)
	}
	manifest.Tasks[0].Nodes[0].BoundaryIdentitySHA256 = testDigest("changed")
	if manifest.Validate() == nil {
		t.Fatal("mutated manifest seal admitted")
	}
	invalid := testIdentity()
	invalid.ArtifactSHA256 = "sha256:bad"
	if _, err := GenerateManifest(1, invalid); err == nil {
		t.Fatal("invalid runtime identity admitted")
	}
}

func testIdentity() RuntimeIdentity {
	return RuntimeIdentity{
		SourceCommit:           "0123456789abcdef0123456789abcdef01234567",
		ArtifactSHA256:         testDigest("artifact"),
		ExecutionProfileSHA256: testDigest("profile"),
		CapabilityPlanSHA256:   testDigest("plan"),
		HarnessSHA256:          testDigest("harness"),
	}
}

func testDigest(seed string) string {
	value := make([]byte, 64)
	for index := range value {
		value[index] = "0123456789abcdef"[(index+len(seed))%16]
	}
	return "sha256:" + string(value)
}

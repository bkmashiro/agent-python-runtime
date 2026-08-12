package operator_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bkmashiro/agent-python-runtime/research/operator"
	"github.com/bkmashiro/agent-python-runtime/runtime/capability"
	"github.com/bkmashiro/agent-python-runtime/runtime/playback"
)

func TestInspectCompareAndDAGUseSemanticBoundedOutput(t *testing.T) {
	parent := testBundle(t, "ok", `{"answer":"parent"}`)
	manifest, err := playback.NewBranchManifest(playback.BranchMetadata{
		ParentBundleSHA256: parent.Identity, ForkOperation: 0, RequestSHA256: parent.RequestSHA256,
		ArtifactSHA256: parent.ArtifactSHA256, ExecutionProfileSHA256: parent.ExecutionProfileSHA256,
		ChildCapabilityPlanSHA256: parent.CapabilityPlanSHA256, ChildGrants: parent.Grants,
		SuffixMode: playback.BranchOverride,
	}, parent, []capability.TranscriptEntry{testEntry(`{"answer":"child"}`, "branch_override")})
	if err != nil {
		t.Fatal(err)
	}
	child := testChildBundle(t, parent, manifest, "ok", `{"answer":"child"}`)
	summary := operator.InspectBundle(parent, 8)
	if summary.Status != "ok" || summary.SourceCalls != 1 || summary.Capabilities[0] != "sources.demo_catalog" || summary.BundleSHA256 == "" {
		t.Fatalf("summary=%+v", summary)
	}
	comparison := operator.CompareBundles(parent, child, 8)
	if comparison.SameResult || !comparison.SamePlan || comparison.CallDifferences != 1 {
		t.Fatalf("comparison=%+v", comparison)
	}
	dag, err := operator.ExportBranchDAG(parent, []operator.ChildRelation{{Manifest: manifest, Child: child}}, 8)
	if err != nil || len(dag.Nodes) != 2 || len(dag.Edges) != 1 || dag.Edges[0].ForkOperation != 0 {
		t.Fatalf("dag=%+v err=%v", dag, err)
	}
}

func TestDAGRejectsUnrelatedChildBundle(t *testing.T) {
	parent := testBundle(t, "ok", `{"answer":"parent"}`)
	manifest, err := playback.NewBranchManifest(playback.BranchMetadata{
		ParentBundleSHA256: parent.Identity, ForkOperation: 0, RequestSHA256: parent.RequestSHA256,
		ArtifactSHA256: parent.ArtifactSHA256, ExecutionProfileSHA256: parent.ExecutionProfileSHA256,
		ChildCapabilityPlanSHA256: parent.CapabilityPlanSHA256, ChildGrants: parent.Grants,
		SuffixMode: playback.BranchOverride,
	}, parent, []capability.TranscriptEntry{testEntry(`{"answer":"child"}`, "branch_override")})
	if err != nil {
		t.Fatal(err)
	}
	unrelated := testBundle(t, "ok", `{"answer":"different"}`)
	if _, err := operator.ExportBranchDAG(parent, []operator.ChildRelation{{Manifest: manifest, Child: unrelated}}, 8); err == nil {
		t.Fatal("unrelated child Bundle was accepted as branch lineage")
	}
}

func testBundle(t *testing.T, status, result string) playback.Bundle {
	t.Helper()
	metadata := playback.Metadata{
		CapabilityPlanSHA256: digest('a'), RequestSHA256: digest('b'), ArtifactSHA256: digest('c'),
		ExecutionProfileSHA256: digest('d'), ExpectedStatus: status, ExpectedResultSHA256: playback.SHA256([]byte(result)),
		Grants: []capability.GrantBinding{{Capability: "sources.demo_catalog", PolicySHA256: digest('e')}},
	}
	bundle, err := playback.New(metadata, []capability.TranscriptEntry{testEntry(result, "http")})
	if err != nil {
		t.Fatal(err)
	}
	return bundle
}

func testChildBundle(t *testing.T, parent playback.Bundle, manifest playback.BranchManifest, status, result string) playback.Bundle {
	t.Helper()
	resultBody := json.RawMessage(result)
	resultSHA256 := playback.SHA256(resultBody)
	bundle, err := playback.New(playback.Metadata{
		CapabilityPlanSHA256: manifest.ChildCapabilityPlanSHA256, RequestSHA256: manifest.RequestSHA256,
		ArtifactSHA256: manifest.ArtifactSHA256, ExecutionProfileSHA256: manifest.ExecutionProfileSHA256,
		ExpectedStatus: status, ExpectedResultSHA256: resultSHA256,
		InitialWorkspaceSHA256: parent.InitialWorkspaceSHA256, FinalWorkspaceSHA256: parent.FinalWorkspaceSHA256,
		Grants: manifest.ChildGrants,
	}, manifest.SuffixEntries)
	if err != nil {
		t.Fatal(err)
	}
	return bundle
}

func testEntry(result, kind string) capability.TranscriptEntry {
	raw := json.RawMessage(result)
	evidence := capability.TransportEvidence{Kind: kind, Status: 200, MediaType: "application/json", BodyBytes: uint32(len(raw)), BodySHA256: playback.SHA256(raw)}
	return capability.TranscriptEntry{
		OperationIndex: 0, Capability: "sources.demo_catalog", Arguments: json.RawMessage(`{}`), ArgumentsSHA256: playback.SHA256([]byte(`{}`)),
		Result: raw, ResultSHA256: playback.SHA256(raw), Evidence: evidence,
	}
}

func digest(character byte) string { return "sha256:" + strings.Repeat(string(character), 64) }
